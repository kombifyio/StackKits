package nativehost

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// The Marketplace Coolify moves its Coolify release as one application
// (`stackkit use-cases upgrade marketplace`, D-2026-11-12-06), under the same
// owner-signed release record as a curated application: the checkpoint holds
// the installation, retains its database, Redis and storage volumes and its
// private runtime files (APP_KEY, credentials, API token custody), and selects
// the target release; Apply then starts it and re-runs the admin, Pocket ID
// and token bootstrap with its readback. Coolify migrates its database forward
// on start, so an earlier release only ever starts on restored data.
const marketplaceReleaseWorkload = "marketplace"

func marketplaceCoolifyPin(d architecturev2renderer.MarketplaceCoolifyDocument) (string, string) {
	for _, c := range d.Components {
		if c.ID == "coolify" {
			return c.ImageRef + "@" + c.ImageDigest, componentImageTag(c.ImageRef)
		}
	}
	return "", ""
}

// requireMarketplaceReleaseTransition admits an Apply of the Marketplace
// while a release operation governs it only for the release that operation
// expects: the target of a completed checkpoint (or a later StackKits default
// that supersedes it), the prior release after its data and custody were
// restored, and nothing while a checkpoint or rollback is incomplete.
func requireMarketplaceReleaseTransition(workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) error {
	record, open, err := applicationrelease.OpenFor(workspace, marketplaceReleaseWorkload)
	if err != nil {
		return &ApplicationReleaseSafetyError{reason: applicationReleaseUnknown, workload: marketplaceReleaseWorkload}
	}
	if !open {
		return nil
	}
	pin, to := marketplaceCoolifyPin(d)
	switch record.State {
	case applicationrelease.StateCheckpointed:
		if pin == record.TargetImage.Ref+"@"+record.TargetImage.Digest {
			return nil
		}
		if later, comparable := releaseIsLater(to, record.TargetRelease); comparable && later {
			return nil
		}
	case applicationrelease.StateRolledBack:
		if pin == record.PriorImage.Ref+"@"+record.PriorImage.Digest {
			return nil
		}
	}
	return &ApplicationReleaseSafetyError{reason: applicationReleaseOperationOpen, workload: marketplaceReleaseWorkload, from: record.OperationID, to: to}
}

// admitMarketplaceReleaseImage reports whether an installed recipe may move to
// the document's Coolify image under the open release operation: exactly its
// checkpointed target (or, after a rollback, its prior release), with the live
// private runtime files still those of its checkpoint. found is false without
// such an operation; the image change then needs the StackKits upgrade
// authority.
func admitMarketplaceReleaseImage(workspace string, d architecturev2renderer.MarketplaceCoolifyDocument) (found bool, err error) {
	record, open, err := applicationrelease.OpenFor(workspace, marketplaceReleaseWorkload)
	if err != nil || !open {
		return false, err
	}
	pin, _ := marketplaceCoolifyPin(d)
	switch {
	case record.State == applicationrelease.StateCheckpointed && pin == record.TargetImage.Ref+"@"+record.TargetImage.Digest,
		record.State == applicationrelease.StateRolledBack && pin == record.PriorImage.Ref+"@"+record.PriorImage.Digest:
	default:
		return false, nil
	}
	if len(record.RuntimeFiles) == 0 {
		return true, errors.New("Marketplace release operation " + record.OperationID + " retained no private runtime files")
	}
	if err := applicationrelease.VerifyRuntimeFilesAtCheckpoint(workspace, record); err != nil {
		return true, err
	}
	return true, nil
}

// persistedMarketplaceEntryImage reads the entry image of the installed
// Marketplace recipe; found is false before its first Apply.
func persistedMarketplaceEntryImage(workspaceRoot, entry string) (string, bool, error) {
	directory := filepath.Join(workspaceRoot, filepath.FromSlash(marketplaceDirectory))
	if info, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	} else if err != nil || !info.IsDir() {
		return "", false, errors.New("Marketplace runtime custody is unavailable")
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Close() }()
	exists, _, err := tx.Exists("compose.yaml")
	if err != nil || !exists {
		return "", false, err
	}
	raw, info, err := tx.ReadStableBounded("compose.yaml", 131072)
	defer clear(raw)
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return "", false, errors.New("Marketplace installation recipe is unavailable")
	}
	var recipe struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(raw, &recipe) != nil || recipe.Name != marketplaceProject || recipe.Services[entry].Image == "" {
		return "", false, errors.New("Marketplace installation recipe differs from its project identity")
	}
	return recipe.Services[entry].Image, true, nil
}
