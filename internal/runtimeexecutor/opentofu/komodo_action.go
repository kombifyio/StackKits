package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

type komodoActionIntent struct {
	Version       string                                  `json:"version"`
	Deployment    platformdeploy.HostDeployment           `json:"deployment"`
	HeldID        string                                  `json:"heldId"`
	ComposeDigest string                                  `json:"composeDigest"`
	EnvDigest     string                                  `json:"envDigest"`
	Signature     localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func newKomodoActionIntent(workspace string, deployment nativehost.SelectedPaaSWorkloadDeployment, prepared nativehost.NativeWorkloadCompose, env []byte, server, held string, prior platformdeploy.HostDeployment) (komodoActionIntent, error) {
	intent := komodoActionIntent{
		Version: "stackkit.komodo-provider-action/v1", HeldID: held,
		ComposeDigest: digestBytes(prepared.Compose), EnvDigest: digestBytes(env),
		Deployment: platformdeploy.HostDeployment{
			WorkloadRef: deployment.WorkloadRef, InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest,
			SiteRef: deployment.SiteRef, NodeRef: deployment.NodeRef, ServerID: server, Manager: "komodo",
			Deployment: platformdeploy.DeploymentRef{Platform: "komodo", AppName: prepared.ProjectName},
			Handover:   prior.Handover,
		},
	}
	var err error
	intent.Signature, err = localevidence.SignOwnerPolicyState(workspace, intent.signingBytes())
	return intent, err
}

func (i komodoActionIntent) signingBytes() []byte {
	i.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(i)
	return append([]byte("stackkit.komodo-provider-action/v1\n"), raw...)
}

// RunKomodoWorkloadAction is the narrow subprocess entry point called only by
// the generated root. It loads credentials from existing node custody, checks
// the signed recipe and held ID, and never creates/updates stack config.
func RunKomodoWorkloadAction(ctx context.Context, action, project, stackID string) error {
	if ctx == nil || (action != "deploy" && action != "destroy" && action != "verify-destroy") || !runtimeDirPattern.MatchString(project) || !komodoStackIDPattern.MatchString(stackID) {
		return errors.New("invalid Komodo provider lifecycle request")
	}
	// Leave room for bounded Core lookup, deploy retry/update observation and
	// final readback inside the outer OpenTofu command's fourteen-minute cap.
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	workspace := filepath.Clean(filepath.Join(root, "..", "..", "..", "..", ".."))
	relative, err := WorkloadRootRelativePath(project)
	if err != nil || root != filepath.Join(workspace, filepath.FromSlash(relative)) {
		return errors.New("Komodo action must run inside its exact workload root")
	}
	prefix := "STACKKIT_KOMODO_" + strings.ToUpper(strings.ReplaceAll(action, "-", "_"))
	// Select the action-scoped shared service. Credentials and signed recipe are
	// not opened until the exact parent workspace/project lease is received.
	if err := os.Setenv(actionLeaseSocketEnv, os.Getenv(prefix+"_LEASE_SOCKET")); err != nil {
		return err
	}
	lease, err := receiveActionLease(workspace, project, os.Getenv(prefix+"_CAPABILITY"))
	if err != nil {
		return err
	}
	defer lease.Close()
	marker, err := ReadRootMarker(root)
	if err != nil || !isKomodoProviderRoot(marker) || marker.ComposeProject != project {
		return errors.New("Komodo action has no exact provider root marker")
	}
	info, err := os.Lstat(filepath.Join(root, komodoActionFile))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 || info.Mode().Perm()&0077 != 0 {
		return errors.New("Komodo action intent is not a private bounded file")
	}
	raw, err := os.ReadFile(filepath.Join(root, komodoActionFile))
	if err != nil {
		return err
	}
	var intent komodoActionIntent
	if json.Unmarshal(raw, &intent) != nil || intent.Version != "stackkit.komodo-provider-action/v1" ||
		!komodoProviderRootMatches(marker, intent.Deployment) || intent.Deployment.Deployment.AppName != project || intent.Deployment.InstanceRef != marker.InstanceRef ||
		project != "stackkit-"+intent.Deployment.WorkloadRef+"-"+intent.Deployment.NodeRef {
		return errors.New("Komodo action intent differs from its root")
	}
	if err := localevidence.VerifyOwnerPolicyState(workspace, intent.signingBytes(), intent.Signature); err != nil {
		return errors.New("Komodo action intent signature is invalid")
	}
	if intent.HeldID != "" && intent.HeldID != stackID {
		return errors.New("Komodo provider changed the independently held stack ID; explicit transition is required")
	}
	if action == "deploy" {
		for file, expected := range map[string]string{ComposeFile: intent.ComposeDigest, EnvFile: intent.EnvDigest} {
			content, err := os.ReadFile(filepath.Join(filepath.Dir(root), file))
			if err != nil {
				return errors.New("Komodo action recipe file is missing")
			}
			actual := digestBytes(content)
			clear(content)
			if actual != expected {
				return errors.New("Komodo action recipe changed after preparation")
			}
		}
	}
	cfg, err := platformdeploy.LoadOwnerNativeKomodoConfig(ctx, workspace)
	if err != nil {
		return errors.New("Komodo action cannot resolve existing node custody")
	}
	if cfg.ServerID != intent.Deployment.ServerID {
		return errors.New("Komodo action server differs from its signed node")
	}
	adapter := platformdeploy.NewKomodoAdapter(cfg)
	ref := intent.Deployment.Deployment
	ref.ExternalID = stackID
	if err := adapter.RequireProviderHostStack(ctx, ref, filepath.Dir(root)); err != nil {
		return err
	}
	present, err := platformdeploy.HasOwnerHostDeployment(workspace, project)
	if err != nil {
		return err
	}
	var held platformdeploy.HostDeployment
	if present {
		held, err = platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if !komodoProviderRootMatches(marker, held) || held.Manager != "komodo" || held.Signature == nil || platformdeploy.RequireHostDeploymentManager(held, "komodo") != nil || held.Deployment.ExternalID != stackID ||
			held.ServerID != cfg.ServerID || held.InstanceRef != intent.Deployment.InstanceRef || held.SiteRef != intent.Deployment.SiteRef || held.NodeRef != intent.Deployment.NodeRef {
			return errors.New("Komodo action differs from independently held workload ownership")
		}
	} else if intent.HeldID != "" || action != "deploy" {
		return errors.New("Komodo action requires independently held workload ownership")
	}
	if action == "verify-destroy" {
		var stopped komodoTeardown
		if err := readKomodoRecord(workspace, root, "komodo-destroy.json", &stopped); err != nil {
			return errors.New("Komodo configuration deletion refused: no authenticated completed teardown; establish lifecycle coverage through verified Apply before destroying this root")
		}
		config, err := os.ReadFile(filepath.Join(root, ConfigFile))
		if err != nil {
			return err
		}
		if stopped.Version != "stackkit.komodo-provider-teardown/v1" || localevidence.VerifyOwnerPolicyState(workspace, stopped.signingBytes(), stopped.Signature) != nil || !sameKomodoRecord(stopped.Custody, held) || stopped.RootDigest != digestBytes(config) {
			return errors.New("Komodo configuration deletion refused: teardown evidence differs from the exact held custody/root")
		}
		// Authentic readback does not prove a preceding rename was durable.
		// Re-publish the exact admitted witness before configuration deletion.
		record, err := json.Marshal(stopped)
		if err != nil {
			return err
		}
		if err := writeKomodoAuthority(workspace, root, "komodo-destroy.json", record); err != nil {
			return err
		}
		return adapter.RequireProviderHostStackDown(ctx, held.Deployment, filepath.Dir(root))
	}
	if action == "destroy" {
		// Teardown retains loaded signed status/update/time metadata on every path.
		if err := adapter.DestroyProviderHostStack(ctx, held.Deployment, filepath.Dir(root)); err != nil {
			return err
		}
		config, err := os.ReadFile(filepath.Join(root, ConfigFile))
		if err != nil {
			return err
		}
		stopped := komodoTeardown{Version: "stackkit.komodo-provider-teardown/v1", Custody: held, RootDigest: digestBytes(config)}
		stopped.Signature, err = localevidence.SignOwnerPolicyState(workspace, stopped.signingBytes())
		if err != nil {
			return err
		}
		raw, err := json.Marshal(stopped)
		if err != nil {
			return err
		}
		return writeKomodoAuthority(workspace, root, "komodo-destroy.json", raw)
	}
	// The parent holds the project lock. Authenticate/open the applied source
	// before advancing custody; an interrupted helper must never erase its link.
	if present {
		source, err := komodoRecoverySource(workspace, root, intent, held)
		if err != nil {
			return err
		}
		if source != nil {
			if err := requireKomodoRetainedMaterial(workspace, *source); err != nil {
				return err
			}
		}
	}
	state := intent.Deployment
	state.Deployment = ref
	// Persist the provider-issued ID before executing its first action. An
	// interrupted deployment cannot later authorize a same-name replacement.
	state, err = witnessKomodoCustody(workspace, root, intent, state, false)
	if err != nil {
		return err
	}
	if err := saveKomodoCustody(workspace, state); err != nil {
		return err
	}
	ref.DeploymentID, err = adapter.Deploy(ctx, ref)
	if err != nil {
		return errors.New("Komodo provider deployment failed; inspect the held stack update")
	}
	ref.LastDeployed = time.Now().UTC()
	ref, err = adapter.ObserveDeployment(ctx, ref)
	if err != nil {
		return errors.New("Komodo provider deployment is not observed running")
	}
	state.Deployment = ref
	// The pending source may retire only after this API observation. The signed
	// witness also covers interruption between the final custody save/checkpoint.
	state.Signature = nil
	state, err = witnessKomodoCustody(workspace, root, intent, state, true)
	if err != nil {
		return err
	}
	if err := saveKomodoCustody(workspace, state); err != nil {
		return err
	}
	return commitKomodoApplied(workspace, root, intent)
}

// Existing node custody signs completion; this is a bounded lifecycle witness,
// not a second owner record. A fresh live down read also precedes provider Delete.
type komodoTeardown struct {
	Version    string                                  `json:"version"`
	Custody    platformdeploy.HostDeployment           `json:"custody"`
	RootDigest string                                  `json:"rootDigest"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func (d komodoTeardown) signingBytes() []byte {
	d.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(d)
	return append([]byte("stackkit.komodo-provider-teardown/v1\n"), raw...)
}
