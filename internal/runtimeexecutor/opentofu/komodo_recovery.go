package opentofu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	KomodoProviderRecoverIDEnv   = "STACKKIT_KOMODO_PROVIDER_RECOVER_ID"
	KomodoProviderRecoverFromEnv = "STACKKIT_KOMODO_PROVIDER_RECOVER_FROM_ID"
)

// This local journal admits one exact custody change inside a verified Apply.
// It retains the original signed record, and can resume either side of the
// custody write without issuing an upstream mutation or editing Tofu state.
type komodoRecovery struct {
	Version        string                                  `json:"version"`
	PreviousID     string                                  `json:"previousId"`
	TargetID       string                                  `json:"targetId"`
	ArtifactDigest string                                  `json:"artifactDigest"`
	ComposeDigest  string                                  `json:"composeDigest"`
	EnvDigest      string                                  `json:"envDigest"`
	DataDigest     string                                  `json:"dataDigest"`
	Previous       *platformdeploy.HostDeployment          `json:"previous,omitempty"`
	Target         platformdeploy.HostDeployment           `json:"target"`
	Signature      localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func (r komodoRecovery) signingBytes() []byte {
	r.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(r)
	return append([]byte("stackkit.komodo-provider-recovery/v1\n"), raw...)
}

func (o *KomodoWorkloadOperations) recoverKomodoOwnership(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment, root string, cfg platformdeploy.HTTPConfig) error {
	targetID, previousID := os.Getenv(KomodoProviderRecoverIDEnv), os.Getenv(KomodoProviderRecoverFromEnv)
	if !komodoStackIDPattern.MatchString(targetID) || (previousID != "none" && !komodoStackIDPattern.MatchString(previousID)) || targetID == previousID || os.Getenv(KomodoProviderImportEnv) != "" {
		return fmt.Errorf("Komodo recovery requires %s=<exact new ID> and %s=<exact previous ID or none>, without an import override", KomodoProviderRecoverIDEnv, KomodoProviderRecoverFromEnv)
	}
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return err
	}
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	if deployment.SiteRef != owner.Binding.SiteRef || deployment.NodeRef != owner.Binding.NodeRef {
		return errors.New("Komodo recovery refuses another workload/node authority")
	}
	marker, err := ReadRootMarker(root)
	if err != nil || marker.Execution != komodoProviderExecution || marker.ModuleRef != deployment.ModuleRef || marker.InstanceRef != deployment.InstanceRef || marker.ComposeProject != project {
		return errors.New("Komodo recovery requires the original identified provider root; legacy/native installs require a separately governed adoption with signed original recipe/data evidence")
	}
	fs, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	relative, err := filepath.Rel(workspace, root)
	if err != nil {
		return err
	}
	// The enclosing Apply holds the exact application project lock across this
	// entire operation. A second lock here would split the concurrency contract
	// or deadlock when the same project key is used.
	raw, _, err := tx.ReadStableBounded(filepath.ToSlash(filepath.Join(relative, komodoActionFile)), 65536)
	if err != nil {
		return errors.New("Komodo recovery requires the original signed Apply intent; no same-name or unsigned legacy adoption is admitted")
	}
	var intent komodoActionIntent
	if json.Unmarshal(raw, &intent) != nil || intent.Version != "stackkit.komodo-provider-action/v1" || localevidence.VerifyOwnerPolicyState(workspace, intent.signingBytes(), intent.Signature) != nil {
		return errors.New("Komodo recovery original Apply intent is not authenticated")
	}
	intent, pending, err := pendingKomodoRecoveryIntent(workspace, root, intent, deployment.ArtifactDigest)
	if err != nil {
		return err
	}
	i := intent.Deployment
	if !komodoProviderRootMatches(marker, i) || i.Deployment.AppName != project || i.InstanceRef != deployment.InstanceRef || i.SiteRef != deployment.SiteRef || i.NodeRef != deployment.NodeRef || i.WorkloadRef != deployment.WorkloadRef || i.ServerID != cfg.ServerID || i.ArtifactDigest != deployment.ArtifactDigest || (intent.HeldID != "" && intent.HeldID != previousID && intent.HeldID != targetID) {
		return errors.New("Komodo recovery must use the unchanged original workload artifact, node, server and exact previous ID")
	}
	if pending == nil {
		for file, expected := range map[string]string{ComposeFile: intent.ComposeDigest, EnvFile: intent.EnvDigest} {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(root), file))
			if err != nil {
				return errors.New("Komodo recovery requires its original persisted recipe")
			}
			actual := digestBytes(data)
			clear(data)
			if actual != expected {
				return errors.New("Komodo recovery recipe changed; reconcile the original signed Apply before adoption")
			}
		}
	}
	adapter := platformdeploy.NewKomodoAdapter(cfg)
	ref := platformdeploy.DeploymentRef{Platform: "komodo", AppName: project, ExternalID: targetID}
	if err := adapter.RequireProviderHostStack(ctx, ref, filepath.Dir(root)); err != nil {
		return err
	}
	if previousID != "none" {
		if err := adapter.RequireProviderStackIDAbsent(ctx, previousID); err != nil {
			return err
		}
	}
	present, err := platformdeploy.HasOwnerHostDeployment(workspace, project)
	if err != nil {
		return err
	}
	var prior *platformdeploy.HostDeployment
	if present {
		value, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if value.Manager != "komodo" || value.Signature == nil || platformdeploy.RequireHostDeploymentManager(value, "komodo") != nil || !sameKomodoTarget(value, i) {
			return errors.New("Komodo recovery existing signed ownership differs from the original intent")
		}
		prior = &value
	} else if previousID != "none" {
		return errors.New("Komodo recovery previous signed custody is missing; its exact record must be recovered before replacing an ID")
	}
	if deployment.ModuleRef == komodoFilesModule && prior == nil {
		return errors.New("Komodo Files recovery requires its original signed deployment custody; unsigned adoption is not admitted")
	}
	var source *komodoRecipe
	if prior != nil {
		source, err = komodoRecoverySource(workspace, root, intent, *prior)
		if err != nil {
			return err
		}
		if source == nil && prior.ArtifactDigest != i.ArtifactDigest {
			return errors.New("Komodo recovery existing signed ownership differs from the original intent; an authenticated applied/pending update journal is required")
		}
	}
	var dataDigest string
	if source != nil {
		bundle, files, err := openKomodoRecipe(workspace, *source)
		if err != nil {
			return err
		}
		defer clear(bundle)
		defer clearKomodoFiles(files)
		original, err := komodoSourceDeployment(*source, bundle)
		if err != nil {
			return err
		}
		dataDigest, err = nativehost.ObserveKomodoRecoveryRecipe(ctx, o.prepared, original, files)
		if err != nil {
			return fmt.Errorf("Komodo recovery applied-source data-contract admission: %w", err)
		}
	} else {
		dataDigest, err = nativehost.ObserveKomodoRecoveryBindings(ctx, o.prepared, deployment)
		if err != nil {
			return fmt.Errorf("Komodo recovery data-contract admission: %w", err)
		}
	}
	// A running target witness is durable before final custody. Only after
	// its independently verified B graph may that phase replace applied A.
	if prior != nil && pending != nil && observedKomodoTarget(pending, *prior) != nil {
		resumed, err := commitVerifiedKomodoObservation(workspace, root, pending, *prior)
		if err != nil {
			return err
		}
		prior = &resumed
	}
	var journal komodoRecovery
	restorePending := func() error {
		if pending == nil {
			return nil
		}
		bundle, files, err := openKomodoRecipe(workspace, pending.Target)
		if err != nil {
			return err
		}
		defer clear(bundle)
		defer clearKomodoFiles(files)
		if err := nativehost.RestoreKomodoProviderRecipe(ctx, o.prepared, deployment, files); err != nil {
			return err
		}
		raw, err := json.Marshal(intent)
		if err != nil {
			return err
		}
		return writeKomodoAuthority(workspace, root, komodoActionFile, raw)
	}
	journalFile := "komodo-recovery-" + previousID + "-" + targetID + ".json"
	journalPath := filepath.Join(root, journalFile)
	if raw, err := os.ReadFile(journalPath); err == nil {
		if len(raw) > 65536 || json.Unmarshal(raw, &journal) != nil || journal.Version != "stackkit.komodo-provider-recovery/v1" || localevidence.VerifyOwnerPolicyState(workspace, journal.signingBytes(), journal.Signature) != nil || journal.PreviousID != previousID || journal.TargetID != targetID || journal.ArtifactDigest != i.ArtifactDigest || journal.ComposeDigest != intent.ComposeDigest || journal.EnvDigest != intent.EnvDigest || (journal.DataDigest != dataDigest && (prior == nil || prior.Deployment.ExternalID != targetID)) || journal.Target.ServerID != cfg.ServerID || journal.Target.Deployment.AppName != project {
			return errors.New("Komodo recovery must resume its original signed exact-ID/data admission")
		}
		// Readback proves authenticity, not that a previous rename reached disk.
		if err := writeKomodoAuthority(workspace, root, journalFile, raw); err != nil {
			return err
		}
		if prior != nil && prior.Deployment.ExternalID == targetID {
			return restorePending()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if (prior == nil && previousID != "none") || (prior != nil && prior.Deployment.ExternalID != previousID) {
			return errors.New("Komodo recovery previous ID does not match independently held ownership")
		}
		target := i
		target.Manager = "komodo"
		target.Deployment = ref
		target.Handover = nil
		target, err = canonicalKomodoCustody(workspace, target)
		if err != nil {
			return err
		}
		journal = komodoRecovery{Version: "stackkit.komodo-provider-recovery/v1", PreviousID: previousID, TargetID: targetID, ArtifactDigest: i.ArtifactDigest, ComposeDigest: intent.ComposeDigest, EnvDigest: intent.EnvDigest, DataDigest: dataDigest, Previous: prior, Target: target}
		journal.Signature, err = localevidence.SignOwnerPolicyState(workspace, journal.signingBytes())
		if err != nil {
			return err
		}
		raw, err := json.Marshal(journal)
		if err != nil {
			return err
		}
		if err := writeKomodoAuthority(workspace, root, journalFile, raw); err != nil {
			return err
		}
	}
	if (prior == nil && journal.Previous != nil) || (prior != nil && prior.Deployment.ExternalID != previousID) {
		return errors.New("Komodo recovery custody changed while admission was pending")
	}
	// Only after exact-ID/data admission and its signed journal may an interrupted
	// pending recipe be restored. No provider state, marker, upstream object,
	// container or application data is changed.
	if err := restorePending(); err != nil {
		return err
	}
	target, err := canonicalKomodoCustody(workspace, journal.Target)
	if err != nil {
		return err
	}
	if pending != nil {
		target, err = witnessKomodoCustody(workspace, root, intent, target, false)
		if err != nil {
			return err
		}
	}
	return saveKomodoCustody(workspace, target)
}
