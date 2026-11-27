package nativehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"gopkg.in/yaml.v3"
)

const (
	// RestoreDrillOperationLabel tags every isolated recovery resource with the owning drill ID.
	RestoreDrillOperationLabel = "io.stackkit.restore-drill.operation"
	// RestoreDrillRoleLabel marks temporary isolated-clone containers, volumes, and networks.
	RestoreDrillRoleLabel = "io.stackkit.restore-drill.role"
	// RestoreDrillWorkloadLabel records the drilled workload identifier on clone resources.
	RestoreDrillWorkloadLabel = "io.stackkit.restore-drill.workload"
	restoreDrillRoleClone     = "isolated-clone"
)

// ClonePreparationError categorizes a fail-closed preflight or clone-rendering failure.
type ClonePreparationError struct {
	Code  string
	Cause error
}

func (e *ClonePreparationError) Error() string {
	if e == nil || e.Cause == nil {
		return "isolated clone preparation failed"
	}
	return e.Cause.Error()
}

func (e *ClonePreparationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsolatedCloneVolumeSpec describes one persistent volume mapped between live custody and an isolated clone.
type IsolatedCloneVolumeSpec struct {
	ComponentRef string
	VolumeRef    string
	LogicalName  string
	LiveName     string
	CloneName    string
	Target       string
	Backup       bool
	ReadOnly     bool
}

// IsolatedCloneComponentSpec captures resolved component metadata required for clone verification and receipt evidence.
type IsolatedCloneComponentSpec struct {
	ID               string
	Role             string
	Lifecycle        string
	HealthFailure    string
	ImageRef         string
	ImageDigest      string
	DependsOn        []string
	HealthKind       string
	HealthPath       string
	HealthPort       int
	PostgresDatabase string
	PostgresUser     string
}

// IsolatedClonePreparation holds the verified, isolated Compose bundle and metadata for an application recovery drill.
type IsolatedClonePreparation struct {
	WorkloadRef             string
	ModuleRef               string
	Release                 string
	SiteRef                 string
	NodeRef                 string
	LiveProject             string
	CloneProject            string
	CloneDirectory          string
	ComposePath             string
	EnvironmentPath         string
	EntryComponent          string
	EntryHealthPath         string
	EntryHealthPort         int
	DatabaseComponent       string
	DatabaseImage           string
	DatabaseName            string
	DatabaseUser            string
	HasRestoreActivationEnv bool
	InternalNetworks        []string
	HealthNetwork           string
	ConfigFilesVerified     []string
	SecretSlotsVerified     []string
	Components              []IsolatedCloneComponentSpec
	Volumes                 []IsolatedCloneVolumeSpec
	Unknowns                []string
}

// PrepareIsolatedCloneCompose validates live workload config/key/dependency custody without mutating live files
// or external IdP state, verifies absence of prohibited outbound notifications, and writes an isolated clone
// Compose definition into cloneDir.
func PrepareIsolatedCloneCompose(
	ctx context.Context,
	workspaceRoot, cloneProject, cloneDir, drillID string,
	deployment SelectedPaaSWorkloadDeployment,
) (IsolatedClonePreparation, error) {
	if runtime.GOOS != "linux" {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "unsupported_workload",
			Cause: fmt.Errorf("isolated application recovery requires Linux (current OS: %s)", runtime.GOOS),
		}
	}
	if strings.TrimSpace(cloneProject) == "" || strings.TrimSpace(cloneDir) == "" || strings.TrimSpace(drillID) == "" {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "isolation_violation",
			Cause: errors.New("isolated clone preparation requires non-empty cloneProject, cloneDir, and drillID"),
		}
	}
	opsIfc, err := NewOSStandaloneComposeWorkloadOperations(workspaceRoot)
	if err != nil {
		return IsolatedClonePreparation{}, &ClonePreparationError{Code: "missing_config", Cause: err}
	}
	ops, ok := opsIfc.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "unsupported_workload",
			Cause: errors.New("unexpected standalone Compose operations implementation"),
		}
	}
	if deployment.RuntimeAdapter.ID != standaloneComposeAdapterRef || deployment.RuntimeAdapter.ModuleRef != standaloneComposeModuleRef {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "unsupported_workload",
			Cause: fmt.Errorf("isolated application recovery supports %s deployments only (got %q)", standaloneComposeAdapterRef, deployment.RuntimeAdapter.ID),
		}
	}

	project, err := ops.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return IsolatedClonePreparation{}, classifyClonePrepareError(err)
	}
	if cloneProject == project.name {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "isolation_violation",
			Cause: fmt.Errorf("isolated clone project %q matches live project %q", cloneProject, project.name),
		}
	}
	if err := ops.verifyPersisted(project); err != nil {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "missing_config",
			Cause: fmt.Errorf("live application config/runtime custody check failed: %w", err),
		}
	}

	dbComp, dbName, dbUser, err := validateIsolatedPostgreSQLBundle(project.bundle)
	if err != nil {
		return IsolatedClonePreparation{}, err
	}
	if err := checkProhibitedOutboundNotifications(project.bundle); err != nil {
		return IsolatedClonePreparation{}, err
	}

	var doc standaloneComposeDocument
	if err := yaml.Unmarshal(project.compose, &doc); err != nil {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "missing_config",
			Cause: fmt.Errorf("parse live standalone Compose document: %w", err),
		}
	}

	internalNets, healthNet, err := rewriteDocumentForIsolatedClone(&doc, project.bundle, cloneProject, drillID)
	if err != nil {
		return IsolatedClonePreparation{}, err
	}
	cloneCompose, err := yaml.Marshal(doc)
	if err != nil {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "missing_config",
			Cause: fmt.Errorf("marshal isolated clone Compose document: %w", err),
		}
	}

	cloneEnv := appendRestoreActivationEnv(project.environment, bundleHasRestoreActivation(project.bundle))
	cloneProjectState := standaloneComposeProject{
		name:        cloneProject,
		directory:   cloneDir,
		compose:     cloneCompose,
		environment: cloneEnv,
		configFiles: project.configFiles,
		bundle:      project.bundle,
		entry:       project.entry,
	}
	if err := ops.persistFiles(cloneProjectState, standaloneComposeProjectFiles(cloneProjectState)); err != nil {
		return IsolatedClonePreparation{}, &ClonePreparationError{
			Code:  "missing_config",
			Cause: fmt.Errorf("persist isolated clone Compose project: %w", err),
		}
	}

	prep := buildClonePreparationResult(project, cloneProject, cloneDir, dbComp, dbName, dbUser, internalNets, healthNet)
	return prep, nil
}

func classifyClonePrepareError(err error) *ClonePreparationError {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "secret"), strings.Contains(msg, "custody"), strings.Contains(msg, "owner identity"):
		return &ClonePreparationError{Code: "missing_key", Cause: err}
	case strings.Contains(msg, "offline model"), strings.Contains(msg, "media library"), strings.Contains(msg, "identity provider"):
		return &ClonePreparationError{Code: "missing_dependency", Cause: err}
	case strings.Contains(msg, "kombify notify"):
		return &ClonePreparationError{Code: "prohibited_outbound_notification", Cause: err}
	default:
		return &ClonePreparationError{Code: "missing_config", Cause: err}
	}
}

func validateIsolatedPostgreSQLBundle(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) (architecturev2renderer.ApplicationDeliveryComponentDescriptor, string, string, error) {
	if bundle.OwnApplication {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
			Code:  "unsupported_workload",
			Cause: errors.New("isolated application recovery drill supports catalog PostgreSQL workloads only"),
		}
	}
	if len(bundle.Components) == 0 {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
			Code:  "missing_dependency",
			Cause: errors.New("workload bundle defines no components"),
		}
	}

	byID := make(map[string]architecturev2renderer.ApplicationDeliveryComponentDescriptor, len(bundle.Components))
	var pgDatabases []architecturev2renderer.ApplicationDeliveryComponentDescriptor
	for _, component := range bundle.Components {
		if component.ID == "" {
			return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
				Code:  "missing_dependency",
				Cause: errors.New("workload component has empty ID"),
			}
		}
		if _, dup := byID[component.ID]; dup {
			return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
				Code:  "missing_dependency",
				Cause: fmt.Errorf("duplicate workload component %q", component.ID),
			}
		}
		if strings.TrimSpace(component.ImageRef) == "" || !strings.HasPrefix(component.ImageDigest, "sha256:") {
			return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
				Code:  "missing_dependency",
				Cause: fmt.Errorf("component %q lacks a digest-pinned image reference", component.ID),
			}
		}
		for _, vol := range component.Volumes {
			if vol.HostPath != "" {
				if _, err := os.Stat(vol.HostPath); err != nil {
					return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
						Code:  "missing_dependency",
						Cause: fmt.Errorf("component %q host mount %q is unavailable: %w", component.ID, vol.HostPath, err),
					}
				}
			}
		}
		byID[component.ID] = component
		if isPostgreSQLComponent(component) {
			pgDatabases = append(pgDatabases, component)
		}
	}

	if err := validateComponentDependencies(bundle.Components, byID); err != nil {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", err
	}
	if len(pgDatabases) != 1 {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
			Code:  "unsupported_workload",
			Cause: fmt.Errorf("workload %q must have exactly one PostgreSQL database component (found %d)", bundle.WorkloadRef, len(pgDatabases)),
		}
	}
	dbComp := pgDatabases[0]
	if !componentDependsOn(bundle.EntryComponent, dbComp.ID, byID) {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, "", "", &ClonePreparationError{
			Code:  "missing_dependency",
			Cause: fmt.Errorf("entry component %q does not depend on PostgreSQL component %q", bundle.EntryComponent, dbComp.ID),
		}
	}

	dbUser := strings.TrimSpace(dbComp.Environment["POSTGRES_USER"])
	if dbUser == "" {
		dbUser = "postgres"
	}
	dbName := strings.TrimSpace(dbComp.Environment["POSTGRES_DB"])
	if dbName == "" {
		dbName = dbUser
	}
	return dbComp, dbName, dbUser, nil
}

func isPostgreSQLComponent(c architecturev2renderer.ApplicationDeliveryComponentDescriptor) bool {
	if c.Role != "database" {
		return false
	}
	img := strings.ToLower(c.ImageRef)
	if strings.Contains(img, "postgres") || strings.Contains(img, "pgvector") {
		return true
	}
	if c.Environment["POSTGRES_DB"] != "" || c.Environment["POSTGRES_USER"] != "" {
		return true
	}
	for _, token := range c.HealthCommand {
		if strings.Contains(strings.ToLower(token), "pg_isready") {
			return true
		}
	}
	return false
}

func validateComponentDependencies(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	byID map[string]architecturev2renderer.ApplicationDeliveryComponentDescriptor,
) error {
	// 0 = unvisited, 1 = visiting, 2 = done
	state := make(map[string]int, len(components))
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return &ClonePreparationError{
				Code:  "missing_dependency",
				Cause: fmt.Errorf("cyclic dependency involving component %q", id),
			}
		case 2:
			return nil
		}
		state[id] = 1
		comp := byID[id]
		for _, dep := range comp.DependsOn {
			if _, ok := byID[dep]; !ok {
				return &ClonePreparationError{
					Code:  "missing_dependency",
					Cause: fmt.Errorf("component %q depends on missing component %q", id, dep),
				}
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, comp := range components {
		if err := visit(comp.ID); err != nil {
			return err
		}
	}
	return nil
}

func componentDependsOn(startID, targetID string, byID map[string]architecturev2renderer.ApplicationDeliveryComponentDescriptor) bool {
	if startID == targetID {
		return true
	}
	visited := map[string]bool{startID: true}
	queue := []string{startID}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, dep := range byID[curr].DependsOn {
			if dep == targetID {
				return true
			}
			if !visited[dep] {
				visited[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return false
}

var prohibitedOutboundEnvMarkers = []string{
	"EMAIL_HOST",
	"SMTP_HOST",
	"SMTP_SERVER",
	"MAIL_HOST",
	"MAILER_DSN",
	"WEBHOOK_URL",
	"NTFY_URL",
	"GOTIFY_URL",
	"SLACK_WEBHOOK",
	"DISCORD_WEBHOOK",
	"TELEGRAM_BOT_TOKEN",
	"APPRISE_URLS",
	"PUSHOVER_",
}

func checkProhibitedOutboundNotifications(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) error {
	for _, comp := range bundle.Components {
		if comp.Egress || comp.HostNetwork || comp.DockerLifecycleOwner || comp.HomeAssistantNotify != nil ||
			len(comp.LANListeners) > 0 || len(comp.PublishedTCPPorts) > 0 || len(comp.PeerNetworks) > 0 {
			return &ClonePreparationError{
				Code:  "prohibited_outbound_notification",
				Cause: fmt.Errorf("component %q declares external egress, host networking, peer network, or notification bindings incompatible with isolated recovery", comp.ID),
			}
		}
		for key, val := range comp.Environment {
			if isProhibitedOutboundEnvKey(key) && isActiveOutboundValue(val) {
				return &ClonePreparationError{
					Code:  "prohibited_outbound_notification",
					Cause: fmt.Errorf("component %q defines active outbound notification setting %q", comp.ID, key),
				}
			}
		}
		for key := range comp.SecretEnvironment {
			if isProhibitedOutboundEnvKey(key) {
				return &ClonePreparationError{
					Code:  "prohibited_outbound_notification",
					Cause: fmt.Errorf("component %q binds secret outbound notification setting %q", comp.ID, key),
				}
			}
		}
	}
	return nil
}

func isProhibitedOutboundEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range prohibitedOutboundEnvMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func isActiveOutboundValue(val string) bool {
	lower := strings.ToLower(strings.TrimSpace(val))
	switch lower {
	case "", "false", "0", "no", "off", "disabled", "none", "null", "localhost", "127.0.0.1", "::1":
		return false
	default:
		return true
	}
}

func rewriteDocumentForIsolatedClone(
	doc *standaloneComposeDocument,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	cloneProject, drillID string,
) ([]string, string, error) {
	doc.Name = cloneProject

	internalNetsMap := map[string]bool{}
	for _, comp := range bundle.Components {
		for _, netRef := range comp.NetworkRefs {
			if netRef != "" && netRef != "stackkit-routing" && netRef != "stackkit-workload-egress" && netRef != "own-private" && !strings.HasPrefix(netRef, "peer-") {
				internalNetsMap[netRef] = true
			}
		}
	}
	if len(internalNetsMap) == 0 {
		return nil, "", &ClonePreparationError{
			Code:  "isolation_violation",
			Cause: errors.New("workload defines no internal component network for clone isolation"),
		}
	}

	rebuiltNets := make(map[string]standaloneComposeNetwork, len(internalNetsMap)+1)
	internalNets := make([]string, 0, len(internalNetsMap))
	for netRef := range internalNetsMap {
		internalNets = append(internalNets, netRef)
		rebuiltNets[netRef] = standaloneComposeNetwork{Internal: true}
	}
	sort.Strings(internalNets)

	// Isolated health bridge allows host loopback probe on 127.0.0.1 while disabling outbound IP masquerading.
	rebuiltNets[standaloneComposeHealthNetwork] = standaloneComposeNetwork{
		DriverOpts: map[string]string{
			"com.docker.network.bridge.enable_ip_masquerade": "false",
		},
		Labels: map[string]string{
			RestoreDrillOperationLabel: drillID,
			RestoreDrillRoleLabel:      restoreDrillRoleClone,
		},
	}
	doc.Networks = rebuiltNets

	entry, _ := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	for name, service := range doc.Services {
		service.Restart = "no"
		service.PullPolicy = "never"
		service.DNS = []string{"127.0.0.1"}
		service.Ports = nil
		service.ExtraHosts = nil
		service.Devices = nil
		service.NetworkMode = ""

		if service.Labels == nil {
			service.Labels = map[string]string{}
		}
		for key := range service.Labels {
			if strings.HasPrefix(key, "traefik.") || strings.HasPrefix(key, "io.stackkit.route.") {
				delete(service.Labels, key)
			}
		}
		service.Labels[RestoreDrillOperationLabel] = drillID
		service.Labels[RestoreDrillRoleLabel] = restoreDrillRoleClone
		service.Labels[RestoreDrillWorkloadLabel] = bundle.WorkloadRef

		filteredNets := make([]string, 0, len(service.Networks))
		for _, netRef := range service.Networks {
			if internalNetsMap[netRef] && !slices.Contains(filteredNets, netRef) {
				filteredNets = append(filteredNets, netRef)
			}
		}
		sort.Strings(filteredNets)
		if name == bundle.EntryComponent {
			service.Ports = []string{fmt.Sprintf("127.0.0.1::%d", entry.HealthPort)}
			filteredNets = append(filteredNets, standaloneComposeHealthNetwork)
		} else if len(filteredNets) == 0 {
			filteredNets = append(filteredNets, internalNets[0])
		}
		service.Networks = filteredNets
		doc.Services[name] = service
	}

	return internalNets, standaloneComposeHealthNetwork, nil
}

func bundleHasRestoreActivation(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) bool {
	for _, comp := range bundle.Components {
		if len(comp.RestoreActivationEnvironment) > 0 {
			return true
		}
	}
	return false
}

func appendRestoreActivationEnv(base []byte, hasRestoreVar bool) []byte {
	if !hasRestoreVar {
		return base
	}
	var b strings.Builder
	b.Write(base)
	if len(base) > 0 && base[len(base)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(architecturev2renderer.RestoreActivationComposeVariable)
	b.WriteString("=true\n")
	return []byte(b.String())
}

func buildClonePreparationResult(
	project standaloneComposeProject,
	cloneProject, cloneDir string,
	dbComp architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	dbName, dbUser string,
	internalNets []string,
	healthNet string,
) IsolatedClonePreparation {
	bundle := project.bundle
	configSet := map[string]struct{}{}
	for _, cfg := range bundle.ConfigFiles {
		if cfg.Path != "" {
			configSet[cfg.Path] = struct{}{}
		}
	}
	for rel := range project.configFiles {
		configSet[rel] = struct{}{}
	}
	configList := make([]string, 0, len(configSet))
	for p := range configSet {
		configList = append(configList, p)
	}
	sort.Strings(configList)

	secretSet := map[string]struct{}{}
	for slot := range bundle.SecretRefs {
		secretSet[slot] = struct{}{}
	}
	for _, comp := range bundle.Components {
		for _, sf := range comp.SecretFiles {
			if sf.Slot != "" {
				secretSet[sf.Slot] = struct{}{}
			}
		}
		if comp.PocketIDClient != nil && !comp.PocketIDClient.Public {
			secretSet["oidc-client-secret"] = struct{}{}
		}
	}
	secretList := make([]string, 0, len(secretSet))
	for s := range secretSet {
		secretList = append(secretList, s)
	}
	sort.Strings(secretList)

	components := make([]IsolatedCloneComponentSpec, 0, len(bundle.Components))
	var volumes []IsolatedCloneVolumeSpec
	seenVols := map[string]bool{}
	hasUnbackedVolume := false
	hasPocketID := false
	for _, comp := range bundle.Components {
		spec := IsolatedCloneComponentSpec{
			ID:            comp.ID,
			Role:          comp.Role,
			Lifecycle:     comp.Lifecycle,
			HealthFailure: comp.HealthFailure,
			ImageRef:      comp.ImageRef,
			ImageDigest:   comp.ImageDigest,
			DependsOn:     append([]string(nil), comp.DependsOn...),
			HealthKind:    comp.HealthKind,
			HealthPath:    comp.HealthPath,
			HealthPort:    comp.HealthPort,
		}
		if comp.ID == dbComp.ID {
			spec.PostgresDatabase = dbName
			spec.PostgresUser = dbUser
		}
		if comp.PocketIDClient != nil || comp.HomeIdentityAccess != nil {
			hasPocketID = true
		}
		components = append(components, spec)

		for _, vol := range comp.Volumes {
			if vol.HostPath != "" || vol.SharedFromComponent != "" {
				continue
			}
			logical := comp.ID + "-" + vol.ID
			if seenVols[logical] {
				continue
			}
			seenVols[logical] = true
			if !vol.Backup {
				hasUnbackedVolume = true
			}
			volumes = append(volumes, IsolatedCloneVolumeSpec{
				ComponentRef: comp.ID,
				VolumeRef:    vol.ID,
				LogicalName:  logical,
				LiveName:     project.name + "_" + logical,
				CloneName:    cloneProject + "_" + logical,
				Target:       vol.Target,
				Backup:       vol.Backup,
				ReadOnly:     vol.ReadOnly,
			})
		}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].LogicalName < volumes[j].LogicalName })

	unknowns := []string{
		"Authenticated end-user login and application data workflow journey require Techstack consumer verification.",
	}
	if hasPocketID {
		unknowns = append(unknowns, "Pocket ID OIDC login flow is not reachable from the network-isolated recovery clone.")
	}
	if hasUnbackedVolume {
		unknowns = append(unknowns, "Non-backed-up scratch/cache volumes start empty on the isolated clone.")
	}

	return IsolatedClonePreparation{
		WorkloadRef:             bundle.WorkloadRef,
		ModuleRef:               bundle.ModuleRef,
		Release:                 bundle.Release,
		SiteRef:                 bundle.SiteRef,
		NodeRef:                 bundle.NodeRef,
		LiveProject:             project.name,
		CloneProject:            cloneProject,
		CloneDirectory:          cloneDir,
		ComposePath:             filepath.Join(cloneDir, "compose.yaml"),
		EnvironmentPath:         filepath.Join(cloneDir, standaloneComposeEnvFile),
		EntryComponent:          project.entry.ID,
		EntryHealthPath:         project.entry.HealthPath,
		EntryHealthPort:         project.entry.HealthPort,
		DatabaseComponent:       dbComp.ID,
		DatabaseImage:           composeComponentImage(dbComp),
		DatabaseName:            dbName,
		DatabaseUser:            dbUser,
		HasRestoreActivationEnv: bundleHasRestoreActivation(bundle),
		InternalNetworks:        internalNets,
		HealthNetwork:           healthNet,
		ConfigFilesVerified:     configList,
		SecretSlotsVerified:     secretList,
		Components:              components,
		Volumes:                 volumes,
		Unknowns:                unknowns,
	}
}
