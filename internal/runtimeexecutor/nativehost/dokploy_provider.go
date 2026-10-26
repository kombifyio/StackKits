package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

const dokployProviderSecretRef = "secret://platform/dokploy/provider-bootstrap"
const dokployProviderDirectory = ".stackkit/runtime/dokploy-provider"

type dokployProviderCustody struct {
	Version       string                                `json:"version"`
	OwnerRef      string                                `json:"ownerRef"`
	SiteRef       string                                `json:"siteRef"`
	NodeRef       string                                `json:"nodeRef"`
	LocalEndpoint string                                `json:"localEndpoint"`
	Config        platformdeploy.DokployBootstrapConfig `json:"config"`
}

func (s dokployProviderCustody) httpConfig() (platformdeploy.HTTPConfig, error) {
	cfg := s.Config
	cfg.Endpoint, cfg.BaseURL = s.LocalEndpoint, s.LocalEndpoint
	connection, err := cfg.HTTPConfig()
	if err != nil {
		return connection, err
	}
	endpoint, _ := url.Parse(connection.BaseURL)
	if endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "::1" {
		return connection, errors.New("Dokploy node bootstrap binding requires a private literal loopback endpoint; provide --local-endpoint for the existing node API transport")
	}
	return connection, nil
}

// DokployProviderPreparation is a secret-free prerequisite result. It does
// not attest an installed server version, UI login, routing, health or apply.
type DokployProviderPreparation struct {
	APIVersion            string `json:"apiVersion"`
	SiteRef               string `json:"siteRef"`
	NodeRef               string `json:"nodeRef"`
	ProjectName           string `json:"projectName,omitempty"`
	ComposeID             string `json:"composeId,omitempty"`
	PhysicalProject       string `json:"physicalProject,omitempty"`
	ComposeSHA256         string `json:"composeSHA256,omitempty"`
	Provider              string `json:"provider"`
	ProviderVersion       string `json:"providerVersion"`
	RequiredServerVersion string `json:"requiredServerVersion"`
	LoginPolicy           string `json:"loginPolicy"`
	Status                string `json:"status"`
	OpenTofu              string `json:"openTofu,omitempty"`
}

func dokployPreparation(state dokployProviderCustody) DokployProviderPreparation {
	return DokployProviderPreparation{APIVersion: "stackkit.dokploy-provider-preparation/v1", SiteRef: state.SiteRef, NodeRef: state.NodeRef,
		Provider: "vanillauys/dokploy", ProviderVersion: architecturev2renderer.DokployProviderVersion,
		RequiredServerVersion: "0.30.8", LoginPolicy: "tinyauth-pocketid-outer-protection-plus-dokploy-local-owner", Status: "bootstrap-api-bound"}
}

// BindDokployProviderBootstrap ports the existing qpb.6 bootstrap identity into
// encrypted node custody. It authenticates first and never rotates a key,
// creates another owner/project, overwrites platform.json or repairs a foreign
// platform by replacement. The local owner account remains upstream custody.
func BindDokployProviderBootstrap(ctx context.Context, workspace, localEndpoint string) (DokployProviderPreparation, error) {
	var empty DokployProviderPreparation
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return empty, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return empty, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(dokployProviderDirectory)
	if err != nil {
		return empty, err
	}
	defer lock.Release()
	cfg, err := platformdeploy.LoadOwnerDokployBootstrapConfig(workspace)
	if err != nil {
		return empty, err
	}
	chosenEndpoint := localEndpoint
	if chosenEndpoint == "" {
		connection, _ := cfg.HTTPConfig()
		chosenEndpoint = connection.BaseURL
	}
	state := dokployProviderCustody{Version: "stackkit.dokploy-provider-custody/v1", OwnerRef: owner.OwnerRef, SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, LocalEndpoint: chosenEndpoint, Config: cfg}
	prior, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, dokployProviderSecretRef)
	existing := err == nil
	if err == nil {
		defer clear(prior)
		var held dokployProviderCustody
		if json.Unmarshal(prior, &held) != nil {
			return empty, errors.New("Dokploy bootstrap custody is invalid")
		}
		if localEndpoint == "" {
			state.LocalEndpoint = held.LocalEndpoint
		}
		if held != state {
			return empty, errors.New("Dokploy bootstrap binding changed; preserve node credentials and use an explicit identity transition")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	httpConfig, err := state.httpConfig()
	if err != nil {
		return empty, err
	}
	if err := platformdeploy.NewDokployAdapter(httpConfig).RequireProviderEnvironment(ctx, cfg.ProjectID); err != nil {
		return empty, err
	}
	if existing {
		return dokployPreparation(state), nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return empty, err
	}
	defer clear(raw)
	if err := localevidence.StoreEncryptedLocalIssuedSecret(workspace, dokployProviderSecretRef, raw); err != nil {
		return empty, err
	}
	return dokployPreparation(state), nil
}

// LoadOwnerDokployProviderConfig is the consumer seam for the standard
// OpenTofu executor. No environment-key or plaintext-bootstrap fallback exists
// after binding. Callers must keep the returned key out of roots and output.
func LoadOwnerDokployProviderConfig(ctx context.Context, workspace, site, node string) (platformdeploy.HTTPConfig, error) {
	state, err := loadDokployProviderCustody(workspace)
	if err != nil {
		return platformdeploy.HTTPConfig{}, err
	}
	if state.SiteRef != site || state.NodeRef != node {
		return platformdeploy.HTTPConfig{}, errors.New("Dokploy provider requested another node's bootstrap identity")
	}
	cfg, err := state.httpConfig()
	if err != nil {
		return cfg, err
	}
	err = platformdeploy.NewDokployAdapter(cfg).RequireProviderEnvironment(ctx, state.Config.ProjectID)
	return cfg, err
}

func loadDokployProviderCustody(workspace string) (dokployProviderCustody, error) {
	var state dokployProviderCustody
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return state, err
	}
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, dokployProviderSecretRef)
	if err != nil {
		return state, err
	}
	defer clear(raw)
	if json.Unmarshal(raw, &state) != nil || state.Version != "stackkit.dokploy-provider-custody/v1" || state.OwnerRef != owner.OwnerRef ||
		state.SiteRef != owner.Binding.SiteRef || state.NodeRef != owner.Binding.NodeRef {
		return state, errors.New("Dokploy provider bootstrap differs from the exact local owner node")
	}
	source, err := platformdeploy.LoadOwnerDokployBootstrapConfig(workspace)
	if err != nil || source != state.Config {
		return state, errors.New("the active platform bootstrap differs from encrypted Dokploy custody; use an explicit identity transition")
	}
	return state, nil
}

var dokployNodeProject = regexp.MustCompile(`^stackkit-[a-z0-9][a-z0-9-]{0,125}$`)

// PrepareDokployProviderWorkload produces an import-bound root for an exact
// already-deployed service. No OpenTofu process or API mutation runs here.
// This bounded adoption slice deliberately refuses fresh creates, remote
// servers and Compose-manager transitions until their custody bridge lands.
func PrepareDokployProviderWorkload(ctx context.Context, workspace, project, composeID string) (DokployProviderPreparation, error) {
	var empty DokployProviderPreparation
	state, err := loadDokployProviderCustody(workspace)
	if err != nil {
		return empty, err
	}
	if !dokployNodeProject.MatchString(project) || state.NodeRef == "" {
		return empty, errors.New("Dokploy preparation requires an exact node workload project")
	}
	// A suffix check alone would accept another node's logical workload.
	suffix := "-" + state.NodeRef
	if len(project) <= len("stackkit-")+len(suffix) || project[len(project)-len(suffix):] != suffix {
		return empty, errors.New("Dokploy workload project is outside the bound node")
	}
	if state.Config.ServerID != "" {
		return empty, errors.New("Dokploy provider preparation currently admits only the platform's own host; remote server parity is pending")
	}
	cfg, err := LoadOwnerDokployProviderConfig(ctx, workspace, state.SiteRef, state.NodeRef)
	if err != nil {
		return empty, err
	}
	observed, err := platformdeploy.NewDokployAdapter(cfg).RequireProviderCompose(ctx, platformdeploy.DeploymentRef{Platform: "dokploy", AppName: project, ExternalID: composeID})
	if err != nil {
		return empty, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return empty, err
	}
	relative := ".stackkit/runtime/applications/" + project + "/compose.yaml"
	file, err := view.Open(relative)
	if err != nil {
		return empty, errors.New("Dokploy preparation requires the existing generated node workload Compose source")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return empty, errors.New("Dokploy Compose source exceeds the bounded workload schema")
	}
	defer clear(raw)
	if string(raw) != observed.ComposeFile {
		return empty, errors.New("Dokploy raw Compose differs from the generated node source; reconcile it before provider preparation")
	}
	path, err := filepath.Abs(filepath.Join(workspace, filepath.FromSlash(relative)))
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	config, err := architecturev2renderer.RenderDokployWorkloadOpenTofu(architecturev2renderer.DokployWorkloadSpec{ProjectName: project,
		EnvironmentID: cfg.EnvironmentID, ComposeID: composeID, AppName: observed.AppName, ComposePath: path, ComposeSHA256: digest})
	if err != nil {
		return empty, err
	}
	result := dokployPreparation(state)
	result.ProjectName, result.ComposeID, result.PhysicalProject, result.ComposeSHA256 = project, composeID, observed.AppName, digest
	result.Status, result.OpenTofu = "provider-root-prepared-not-applied", string(config)
	return result, nil
}
