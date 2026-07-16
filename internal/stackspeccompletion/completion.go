// Package stackspeccompletion binds a losslessly read v1 StackSpec to one
// complete, explicit v2 candidate and resolves it through the governed
// Architecture v2 service. It deliberately sits above stackspecmigration:
// the migration projection remains architecture-only and cannot compile.
package stackspeccompletion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

// Resolver is the exact architecture resolution boundary used by the CLI.
// Production callers pass *architecturev2.Service; the interface only keeps
// reconciliation tests independent from the embedded bundle generator.
type Resolver interface {
	Resolve(architecturev2.ResolveInput) (architecturev2.Result, error)
}

// Input contains the losslessly classified legacy document and a complete v2
// candidate supplied by the operator. TargetKitProfile is always explicit;
// context remains locality/hardware migration input only.
type Input struct {
	Legacy           stackspecmigration.Document
	Candidate        []byte
	TargetKitProfile stackspecmigration.KitProfile
}

// Result is produced only after reconciliation and governed CUE resolution.
// CanonicalStackSpec is deterministic JSON for the explicit candidate. It is
// CUE-valid but is intentionally not described as CUE-default-expanded.
type Result struct {
	Status                   stackspecmigration.MigrationStatus
	CandidateSourceSHA256    string
	CanonicalStackSpec       map[string]any
	CanonicalStackSpecBytes  []byte
	CanonicalStackSpecSHA256 string
	PlanHash                 string
	GeneratorEligible        bool
	Report                   stackspecmigration.Report
	ArchitectureProjection   *stackspecmigration.NormalizedArchitecture
}

// Error carries the same structured blocker that was appended to Result.Report.
type Error struct {
	Blocker stackspecmigration.Blocker
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "StackSpec completion is blocked"
	}
	if e.Cause != nil {
		return fmt.Sprintf("StackSpec completion is blocked: %s: %v", e.Blocker.Message, e.Cause)
	}
	return "StackSpec completion is blocked: " + e.Blocker.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Complete accepts no partial overlays. Candidate must be a complete explicit
// StackSpec v2, must preserve every deterministic v1 projection binding, and
// must pass the same Service.Resolve path used by the resolve CLI/API.
func Complete(resolver Resolver, input Input) (Result, error) {
	result := Result{}
	if input.Legacy.Version != stackspecmigration.SourceVersionV1 || input.Legacy.Legacy == nil {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.legacy-not-v1",
			Field:          "legacy.apiVersion",
			Message:        "completion requires a losslessly read v1 StackSpec; an arbitrary or already-v2 document cannot enter the legacy completion stage",
			RequiredInputs: []string{"SourceVersionV1", "losslessly read v1 StackSpec"},
		}, nil)
	}
	target := stackspecmigration.KitProfile(strings.TrimSpace(string(input.TargetKitProfile)))
	if !isCanonicalTarget(target) {
		return block(result, stackspecmigration.Blocker{
			Code:                 "completion.target-kit-required",
			Field:                "options.targetKitProfile",
			Message:              "completion requires an explicit canonical target KitProfile; context never selects it",
			RequiredInputs:       []string{"targetKitProfile"},
			SuggestedKitProfiles: canonicalTargets(),
		}, nil)
	}
	if resolver == nil {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.resolver-unavailable",
			Field:          "completion",
			Message:        "the embedded Architecture v2 resolver is unavailable",
			RequiredInputs: []string{"embedded Architecture v2 authority"},
		}, nil)
	}

	projection, report, migrationErr := stackspecmigration.MigrateDocument(input.Legacy, stackspecmigration.Options{TargetKitProfile: target})
	result.Report = report
	if migrationErr == nil {
		result.ArchitectureProjection = &projection
	} else if !completionCanResolve(report.Blockers, target) {
		return result, migrationErr
	}

	candidateDocument, err := stackspecmigration.Read(input.Candidate)
	if err != nil {
		return block(result, readBlocker(err), err)
	}
	if candidateDocument.Version != stackspecmigration.SourceVersionV2Alpha1 || candidateDocument.V2 == nil {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.candidate-not-v2",
			Field:          "completion.apiVersion",
			Message:        "completion input must be a complete apiVersion stackkit/v2alpha1 StackSpec",
			RequiredInputs: []string{"apiVersion", "kind", "kit.slug"},
		}, nil)
	}
	if candidateDocument.V2.KitProfile != target {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.target-kit-mismatch",
			Field:          "completion.kit.slug",
			Message:        fmt.Sprintf("completion kit.slug %q does not match explicit target KitProfile %q", candidateDocument.V2.KitProfile, target),
			RequiredInputs: []string{"kit.slug", "targetKitProfile"},
		}, nil)
	}

	candidate, canonical, err := decodeCanonicalCandidate(input.Candidate)
	if err != nil {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.candidate-invalid",
			Field:          "completion",
			Message:        "completion input is not a canonical explicit YAML/JSON object",
			RequiredInputs: []string{"one explicit StackSpec v2 document without aliases or duplicate fields"},
		}, err)
	}
	if blocker := validateExplicitCompletion(candidate, target, input.Legacy.Legacy); blocker != nil {
		return block(result, *blocker, nil)
	}
	if result.ArchitectureProjection != nil {
		if blocker := validateProjectionBindings(candidate, *result.ArchitectureProjection); blocker != nil {
			return block(result, *blocker, nil)
		}
	} else if blocker := validateAmbiguousBindings(candidate, target, input.Legacy.Legacy); blocker != nil {
		return block(result, *blocker, nil)
	}

	resolved, err := resolver.Resolve(architecturev2.ResolveInput{
		StackSpec:        input.Candidate,
		TargetKitProfile: target,
	})
	if err != nil {
		return block(result, stackspecmigration.Blocker{
			Code:           "completion.cue-resolution-failed",
			Field:          "completion",
			Message:        "explicit completion candidate was rejected by the governed Architecture v2 CUE resolver",
			RequiredInputs: []string{"CUE-valid StackSpec v2 matching the embedded authority"},
		}, err)
	}

	result.Status = stackspecmigration.MigrationStatusCompleted
	result.CandidateSourceSHA256 = sha256Digest(input.Candidate)
	result.CanonicalStackSpec = candidate
	result.CanonicalStackSpecBytes = canonical
	result.CanonicalStackSpecSHA256 = sha256Digest(canonical)
	result.PlanHash = resolved.PlanHash
	result.GeneratorEligible = generationReady(resolved.Plan)
	completed, unresolved := completedReport(
		result.Report,
		result.CandidateSourceSHA256,
		result.CanonicalStackSpecSHA256,
		resolved.PlanHash,
	)
	result.Report = completed
	if unresolved != nil {
		result.Status = stackspecmigration.MigrationStatusBlocked
		return block(result, *unresolved, nil)
	}
	return result, nil
}

func completedReport(report stackspecmigration.Report, sourceSHA, canonicalSHA, planHash string) (stackspecmigration.Report, *stackspecmigration.Blocker) {
	for _, blocker := range report.Blockers {
		report.Decisions = append(report.Decisions, stackspecmigration.Decision{
			Code:   "completion.explicit-intent-satisfied",
			Field:  blocker.Field,
			From:   blocker.Code,
			To:     "explicit-v2",
			Reason: "the candidate supplied the previously non-derivable intent and passed governed CUE resolution",
		})
	}
	manual := make([]stackspecmigration.ManualAction, 0, len(report.ManualActions))
	for _, action := range report.ManualActions {
		if completionActionSatisfied(action.Code) {
			report.Decisions = append(report.Decisions, stackspecmigration.Decision{
				Code:   "completion.manual-action-satisfied",
				Field:  strings.Join(action.Fields, ","),
				From:   action.Code,
				To:     "explicit-v2",
				Reason: "the complete candidate supplied this intent and passed governed CUE resolution",
			})
			continue
		}
		manual = append(manual, action)
	}
	report.ManualActions = manual
	report.Blockers = []stackspecmigration.Blocker{}
	for index := range report.Warnings {
		if report.Warnings[index].RequiredAction == "" {
			continue
		}
		if !completionWarningActionSatisfied(report.Warnings[index].Code) {
			report.Status = stackspecmigration.MigrationStatusBlocked
			report.RequiresExplicitAcceptance = true
			return report, &stackspecmigration.Blocker{
				Code:           "completion.warning-action-unresolved",
				Field:          report.Warnings[index].Field,
				Message:        "completion cannot claim success while an independent warning action remains unresolved",
				RequiredInputs: []string{report.Warnings[index].RequiredAction},
			}
		}
		report.Decisions = append(report.Decisions, stackspecmigration.Decision{
			Code:   "completion.warning-action-satisfied",
			Field:  report.Warnings[index].Field,
			From:   report.Warnings[index].RequiredAction,
			To:     "explicit-v2",
			Reason: "the operator supplied and CUE-resolved a complete explicit v2 candidate",
		})
		report.Warnings[index].RequiredAction = ""
	}
	for _, action := range report.ManualActions {
		report.Status = stackspecmigration.MigrationStatusBlocked
		report.RequiresExplicitAcceptance = true
		return report, &stackspecmigration.Blocker{
			Code:           "completion.manual-action-unresolved",
			Field:          "report.manualActions",
			Message:        "completion cannot claim success while an independent manual action remains unresolved",
			RequiredInputs: append([]string(nil), action.Fields...),
		}
	}
	report.Decisions = append(report.Decisions,
		stackspecmigration.Decision{
			Code:   "completion.candidate-source-bound",
			Field:  "completion.source.sha256",
			To:     sourceSHA,
			Reason: "the exact operator-supplied completion document is bound to this completed migration",
		},
		stackspecmigration.Decision{
			Code:   "completion.canonical-spec-bound",
			Field:  "completion.canonicalStackSpecSHA256",
			From:   sourceSHA,
			To:     canonicalSHA,
			Reason: "deterministic JSON represents the explicit candidate without claiming CUE default expansion",
		},
	)
	report.Decisions = append(report.Decisions, stackspecmigration.Decision{
		Code:   "completion.cue-resolved",
		Field:  "resolvedPlan.planHash",
		To:     planHash,
		Reason: "explicit v2 candidate resolved through the governed embedded Architecture v2 authority",
	})
	report.Status = stackspecmigration.MigrationStatusCompleted
	report.RequiresExplicitAcceptance = false
	return report, nil
}

func completionActionSatisfied(code string) bool {
	switch code {
	case "projection.complete-v2-spec":
		return true
	default:
		return false
	}
}

func completionWarningActionSatisfied(code string) bool {
	switch code {
	case "context.deprecated", "context.missing":
		return true
	default:
		return false
	}
}

func completionCanResolve(blockers []stackspecmigration.Blocker, target stackspecmigration.KitProfile) bool {
	if len(blockers) == 0 {
		return true
	}
	for _, blocker := range blockers {
		switch blocker.Code {
		case "modern.topology-required", "cloud.home-requires-modern":
			if target != stackspecmigration.KitProfileModern {
				return false
			}
		case "ha.availability-contract-required":
			// A complete candidate must prove the add-on below.
		default:
			return false
		}
	}
	return true
}

func validateExplicitCompletion(candidate map[string]any, target stackspecmigration.KitProfile, legacy *models.StackSpec) *stackspecmigration.Blocker {
	required := []string{
		"apiVersion", "kind", "metadata", "source", "kit", "install", "generation",
		"system", "storage", "network", "sites", "nodes", "controlPlane",
		"capabilities", "addons", "access", "availability", "partitionPolicy",
		"workloads", "modules", "routes", "data",
	}
	missing := missingFields(candidate, required)
	if runtime := nestedString(candidate, "install", "runtime"); runtime == "docker" {
		if _, exists := candidate["container"]; !exists {
			missing = append(missing, "container")
		}
	}
	if target == stackspecmigration.KitProfileBasement || target == stackspecmigration.KitProfileModern {
		if _, exists := candidate["deviceEnrollment"]; !exists {
			missing = append(missing, "deviceEnrollment")
		}
	}
	if target == stackspecmigration.KitProfileModern {
		if _, exists := candidate["bridge"]; !exists {
			missing = append(missing, "bridge")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &stackspecmigration.Blocker{
			Code:           "completion.explicit-intent-incomplete",
			Field:          "completion",
			Message:        "completion candidate omits explicit topology, identity, data, exposure, or lifecycle decisions",
			RequiredInputs: missing,
		}
	}
	if legacy != nil && strings.EqualFold(strings.TrimSpace(legacy.StackKit), stackspecmigration.LegacyHAKitSlug) {
		return validateExplicitHA(candidate)
	}
	return nil
}

func validateExplicitHA(candidate map[string]any) *stackspecmigration.Blocker {
	availability := objectValue(candidate["availability"])
	ha := objectValue(objectValue(candidate["addons"])["ha"])
	requiredAvailability := []string{"enabled", "mode", "rpoSeconds", "rtoSeconds", "failureDomainSpread", "fencing"}
	missing := missingFields(availability, requiredAvailability)
	if enabled, _ := availability["enabled"].(bool); !enabled {
		missing = append(missing, "availability.enabled=true")
	}
	if enabled, _ := ha["enabled"].(bool); !enabled {
		missing = append(missing, "addons.ha.enabled=true")
	}
	controlPlane := objectValue(candidate["controlPlane"])
	mode := stringValue(controlPlane["mode"])
	if mode != "warm-standby" && mode != "quorum" {
		missing = append(missing, "controlPlane.mode=warm-standby|quorum")
	}
	members := stringList(controlPlane["members"])
	minimumMembers := 2
	if mode == "quorum" {
		minimumMembers = 3
	}
	if len(members) < minimumMembers {
		missing = append(missing, fmt.Sprintf("controlPlane.members(minItems=%d)", minimumMembers))
	}
	nodesByID := make(map[string]map[string]any)
	for _, node := range objectList(candidate["nodes"]) {
		nodesByID[stringValue(node["id"])] = node
	}
	failureDomains := make(map[string]struct{})
	for _, member := range members {
		node, exists := nodesByID[member]
		if !exists {
			missing = append(missing, "nodes[id="+member+"]")
			continue
		}
		if !containsString(stringList(node["roles"]), "controller") {
			missing = append(missing, "nodes[id="+member+"].roles contains controller")
		}
		domain := stringValue(node["failureDomain"])
		if domain == "" {
			missing = append(missing, "nodes[id="+member+"].failureDomain")
			continue
		}
		failureDomains[domain] = struct{}{}
	}
	spread, spreadSet := intValue(availability["failureDomainSpread"])
	if spreadSet && spread > len(failureDomains) {
		missing = append(missing, fmt.Sprintf("%d unique control-plane failure domains", spread))
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &stackspecmigration.Blocker{
		Code:           "completion.ha-intent-incomplete",
		Field:          "completion.availability",
		Message:        "legacy ha-kit completion requires explicit HA add-on, objectives, topology and fencing intent",
		RequiredInputs: missing,
	}
}

func stringList(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func intValue(value any) (int, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil
	case float64:
		return int(number), number == float64(int(number))
	case int:
		return number, true
	default:
		return 0, false
	}
}

func validateProjectionBindings(candidate map[string]any, projection stackspecmigration.NormalizedArchitecture) *stackspecmigration.Blocker {
	sites := objectList(candidate["sites"])
	if len(sites) != 1 {
		return bindingBlocker("completion.site-binding-mismatch", "completion.sites", "completion must preserve the projection's single Site binding", []string{projection.PrimarySite.ID})
	}
	if stringValue(sites[0]["id"]) != projection.PrimarySite.ID || stringValue(sites[0]["kind"]) != string(projection.PrimarySite.Kind) {
		return bindingBlocker("completion.site-binding-mismatch", "completion.sites[0]", "completion Site does not match the deterministic legacy projection", []string{projection.PrimarySite.ID, string(projection.PrimarySite.Kind)})
	}
	if authority := nestedString(candidate, "controlPlane", "authoritySiteRef"); authority != projection.AuthoritySiteRef {
		return bindingBlocker("completion.authority-binding-mismatch", "completion.controlPlane.authoritySiteRef", "completion authority does not match the deterministic legacy projection", []string{projection.AuthoritySiteRef})
	}
	for _, node := range objectList(candidate["nodes"]) {
		if stringValue(node["siteRef"]) != projection.NodeDefaults.SiteRef {
			return bindingBlocker("completion.node-site-binding-mismatch", "completion.nodes", "completion node locality does not match the deterministic legacy projection", []string{projection.NodeDefaults.SiteRef})
		}
		if projection.NodeDefaults.HardwareProfile != "" && nestedString(node, "hardware", "profile") != projection.NodeDefaults.HardwareProfile {
			return bindingBlocker("completion.hardware-binding-mismatch", "completion.nodes[*].hardware.profile", "completion hardware does not preserve the legacy Pi projection", []string{projection.NodeDefaults.HardwareProfile})
		}
	}
	return nil
}

func validateAmbiguousBindings(candidate map[string]any, target stackspecmigration.KitProfile, legacy *models.StackSpec) *stackspecmigration.Blocker {
	if target == stackspecmigration.KitProfileModern {
		if blocker := validateModernTopology(candidate); blocker != nil {
			return blocker
		}
	}
	if legacy == nil {
		return bindingBlocker("completion.legacy-missing", "legacy", "completion requires a losslessly read legacy StackSpec", []string{"v1 StackSpec"})
	}
	expectedKind, hardwareProfile, contextSet := legacyContextBinding(legacy.Context)
	if !contextSet {
		if target == stackspecmigration.KitProfileCloud {
			expectedKind = stackspecmigration.SiteKindCloud
		} else if target == stackspecmigration.KitProfileBasement {
			expectedKind = stackspecmigration.SiteKindHome
		}
	}
	if expectedKind != "" && !containsSiteKind(candidate, expectedKind) {
		return bindingBlocker("completion.context-site-binding-mismatch", "completion.sites", "completion topology discards the locality mapped from legacy context", []string{string(expectedKind)})
	}
	if target != stackspecmigration.KitProfileModern && expectedKind != siteKindForTarget(target) {
		return bindingBlocker("completion.context-target-conflict", "completion.sites", "legacy context locality conflicts with the explicit target KitProfile", []string{string(expectedKind), string(target)})
	}
	if hardwareProfile != "" && !preservesLegacyHardware(candidate, expectedKind, hardwareProfile, legacy.Nodes) {
		return bindingBlocker("completion.hardware-binding-mismatch", "completion.nodes[*].hardware.profile", "completion topology does not preserve the legacy Pi hardware binding", []string{hardwareProfile})
	}
	return nil
}

func validateModernTopology(candidate map[string]any) *stackspecmigration.Blocker {
	if !containsSiteKind(candidate, stackspecmigration.SiteKindHome) || !containsSiteKind(candidate, stackspecmigration.SiteKindCloud) {
		return bindingBlocker("completion.modern-sites-incomplete", "completion.sites", "Modern completion requires explicit home and cloud Sites", []string{"sites[kind=home]", "sites[kind=cloud]"})
	}
	authority := nestedString(candidate, "controlPlane", "authoritySiteRef")
	for _, site := range objectList(candidate["sites"]) {
		if stringValue(site["id"]) == authority && stringValue(site["kind"]) == string(stackspecmigration.SiteKindHome) {
			return nil
		}
	}
	return bindingBlocker("completion.modern-home-authority-required", "completion.controlPlane.authoritySiteRef", "Modern completion requires a home Site as control authority", []string{"home authority Site"})
}

func preservesLegacyHardware(candidate map[string]any, siteKind stackspecmigration.SiteKind, profile string, legacyNodes []models.NodeSpec) bool {
	nodes := objectList(candidate["nodes"])
	sites := siteKindsByID(candidate)
	if len(legacyNodes) > 0 {
		byID := make(map[string]map[string]any, len(nodes))
		for _, node := range nodes {
			byID[stringValue(node["id"])] = node
		}
		for _, legacyNode := range legacyNodes {
			node, exists := byID[strings.TrimSpace(legacyNode.Name)]
			if !exists || sites[stringValue(node["siteRef"])] != siteKind || nestedString(node, "hardware", "profile") != profile {
				return false
			}
		}
		return true
	}
	for _, node := range nodes {
		if sites[stringValue(node["siteRef"])] == siteKind && nestedString(node, "hardware", "profile") == profile {
			return true
		}
	}
	return false
}

func generationReady(plan resolvedplan.ResolvedPlan) bool {
	readiness := objectValue(plan["executionReadiness"])
	generation := objectValue(readiness["generation"])
	return stringValue(generation["status"]) == "ready"
}

func decodeCanonicalCandidate(data []byte) (map[string]any, []byte, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return nil, nil, err
	}
	if len(root.Content) != 1 {
		return nil, nil, fmt.Errorf("completion must contain one root value")
	}
	if err := rejectAliasesDuplicatesAndNonStringKeys(root.Content[0], "completion"); err != nil {
		return nil, nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("completion contains multiple YAML documents")
		}
		return nil, nil, err
	}
	var generic any
	if err := root.Content[0].Decode(&generic); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(generic)
	if err != nil {
		return nil, nil, err
	}
	candidate, err := resolvedplan.DecodeDocument[map[string]any](encoded)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(candidate)
	if err != nil {
		return nil, nil, err
	}
	canonical = append(canonical, '\n')
	return candidate, canonical, nil
}

func rejectAliasesDuplicatesAndNonStringKeys(node *yaml.Node, path string) error {
	if node == nil {
		return fmt.Errorf("%s is empty", path)
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("%s contains a non-string mapping key", path)
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("%s contains duplicate field %q", path, key.Value)
			}
			seen[key.Value] = struct{}{}
			if err := rejectAliasesDuplicatesAndNonStringKeys(value, path+"."+key.Value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := rejectAliasesDuplicatesAndNonStringKeys(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		return fmt.Errorf("%s uses a YAML alias", path)
	case yaml.ScalarNode:
		return nil
	default:
		return fmt.Errorf("%s has unsupported YAML node kind %d", path, node.Kind)
	}
	return nil
}

func readBlocker(err error) stackspecmigration.Blocker {
	code := "completion.candidate-read-failed"
	message := err.Error()
	if typed, ok := err.(*stackspecmigration.ReadError); ok {
		code = "completion." + typed.Code
		message = typed.Message
	}
	return stackspecmigration.Blocker{
		Code:           code,
		Field:          "completion",
		Message:        message,
		RequiredInputs: []string{"complete StackSpec v2"},
	}
}

func block(result Result, blocker stackspecmigration.Blocker, cause error) (Result, error) {
	result.Report.Status = stackspecmigration.MigrationStatusBlocked
	result.Report.RequiresExplicitAcceptance = true
	if result.Report.SourceVersion == "" {
		result.Report.SourceVersion = stackspecmigration.APIVersionV1
		result.Report.TargetVersion = stackspecmigration.APIVersionV2Alpha1
		result.Report.Decisions = []stackspecmigration.Decision{}
		result.Report.Warnings = []stackspecmigration.Warning{}
		result.Report.ManualActions = []stackspecmigration.ManualAction{}
		result.Report.Blockers = []stackspecmigration.Blocker{}
	}
	result.Report.Blockers = append(result.Report.Blockers, blocker)
	return result, &Error{Blocker: blocker, Cause: cause}
}

func bindingBlocker(code, field, message string, required []string) *stackspecmigration.Blocker {
	return &stackspecmigration.Blocker{Code: code, Field: field, Message: message, RequiredInputs: required}
}

func missingFields(values map[string]any, required []string) []string {
	missing := make([]string, 0)
	for _, field := range required {
		if _, exists := values[field]; !exists {
			missing = append(missing, field)
		}
	}
	return missing
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func objectList(value any) []map[string]any {
	raw, _ := value.([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func nestedString(value map[string]any, objectKey, field string) string {
	return stringValue(objectValue(value[objectKey])[field])
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func containsSiteKind(candidate map[string]any, kind stackspecmigration.SiteKind) bool {
	for _, site := range objectList(candidate["sites"]) {
		if stringValue(site["kind"]) == string(kind) {
			return true
		}
	}
	return false
}

func siteKindsByID(candidate map[string]any) map[string]stackspecmigration.SiteKind {
	result := make(map[string]stackspecmigration.SiteKind)
	for _, site := range objectList(candidate["sites"]) {
		result[stringValue(site["id"])] = stackspecmigration.SiteKind(stringValue(site["kind"]))
	}
	return result
}

func legacyContextBinding(raw string) (stackspecmigration.SiteKind, string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(models.ContextLocal):
		return stackspecmigration.SiteKindHome, "", true
	case string(models.ContextCloud):
		return stackspecmigration.SiteKindCloud, "", true
	case string(models.ContextPi):
		return stackspecmigration.SiteKindHome, "pi", true
	default:
		return "", "", false
	}
}

func siteKindForTarget(target stackspecmigration.KitProfile) stackspecmigration.SiteKind {
	if target == stackspecmigration.KitProfileCloud {
		return stackspecmigration.SiteKindCloud
	}
	if target == stackspecmigration.KitProfileBasement {
		return stackspecmigration.SiteKindHome
	}
	return ""
}

func isCanonicalTarget(target stackspecmigration.KitProfile) bool {
	for _, candidate := range canonicalTargets() {
		if target == candidate {
			return true
		}
	}
	return false
}

func canonicalTargets() []stackspecmigration.KitProfile {
	return []stackspecmigration.KitProfile{
		stackspecmigration.KitProfileBasement,
		stackspecmigration.KitProfileCloud,
		stackspecmigration.KitProfileModern,
	}
}

func sha256Digest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
