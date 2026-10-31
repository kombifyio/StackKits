package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const komodoAppliedFile = "komodo-applied.json"
const komodoUpdateFile = "komodo-update.json"

// Public records contain only signed identities, digests and encrypted-custody
// locators. Recipe/env/config bytes use the existing encrypted node secret
// store, including its existing 8192-byte per-material bound.
type komodoRetainedFile struct {
	Refs   []string `json:"refs"`
	Size   int      `json:"size"`
	Digest string   `json:"digest"`
}
type komodoRecipe struct {
	Intent komodoActionIntent            `json:"intent"`
	Bundle komodoRetainedFile            `json:"bundle"`
	Files  map[string]komodoRetainedFile `json:"files"`
}
type komodoAppliedRecipe struct {
	Version   string                                  `json:"version"`
	Recipe    komodoRecipe                            `json:"recipe"`
	Custody   platformdeploy.HostDeployment           `json:"custody"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// A pending witness is written before custody advances. Observed is set only
// after the API confirms the target running, never merely after saving its ID.
// It shares the authenticated update/source authority, not a second custody.
type komodoCustodyWitness struct {
	Custody  platformdeploy.HostDeployment `json:"custody"`
	Observed bool                          `json:"observed,omitempty"`
}
type komodoPendingUpdate struct {
	Version          string                                  `json:"version"`
	Source           *komodoAppliedRecipe                    `json:"source,omitempty"`
	Target           komodoRecipe                            `json:"target"`
	PriorIntents     []komodoActionIntent                    `json:"priorIntents,omitempty"`
	CustodyWitnesses []komodoCustodyWitness                  `json:"custodyWitnesses,omitempty"`
	Signature        localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func (a komodoAppliedRecipe) signingBytes() []byte {
	a.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(a)
	return append([]byte("stackkit.komodo-applied-recipe/v1\n"), raw...)
}
func (u komodoPendingUpdate) signingBytes() []byte {
	u.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(u)
	return append([]byte("stackkit.komodo-pending-update/v1\n"), raw...)
}
func sameKomodoRecord(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func sameKomodoIntentIdentity(a, b komodoActionIntent) bool {
	a.HeldID, b.HeldID = "", ""
	a.Signature, b.Signature = localevidence.OwnerPolicyStateSignature{}, localevidence.OwnerPolicyStateSignature{}
	return sameKomodoRecord(a, b)
}
func sameKomodoTarget(a, b platformdeploy.HostDeployment) bool {
	return a.Manager == "komodo" && b.Manager == "komodo" && a.WorkloadRef == b.WorkloadRef && a.InstanceRef == b.InstanceRef && a.SiteRef == b.SiteRef && a.NodeRef == b.NodeRef && a.ServerID == b.ServerID && a.Deployment.AppName == b.Deployment.AppName
}
func readKomodoRecord(workspace, root, name string, out any) error {
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
	relative, err := filepath.Rel(workspace, filepath.Join(root, name))
	if err != nil {
		return err
	}
	raw, info, err := tx.ReadStableBounded(filepath.ToSlash(relative), 65536)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 || json.Unmarshal(raw, out) != nil {
		return errors.New("Komodo retained record is not private bounded JSON")
	}
	return nil
}
func verifyKomodoRecipe(workspace string, r komodoRecipe) error {
	i := r.Intent
	if i.Version != "stackkit.komodo-provider-action/v1" || localevidence.VerifyOwnerPolicyState(workspace, i.signingBytes(), i.Signature) != nil || i.Deployment.Manager != "komodo" || len(r.Files) < 2 || len(r.Files) > 16 || r.Bundle.Digest != i.Deployment.ArtifactDigest || r.Files[ComposeFile].Digest != i.ComposeDigest || r.Files[EnvFile].Digest != i.EnvDigest {
		return errors.New("Komodo retained recipe is not bound to its signed artifact and file digests")
	}
	return nil
}
func loadKomodoApplied(workspace, root string) (*komodoAppliedRecipe, error) {
	var a komodoAppliedRecipe
	if err := readKomodoRecord(workspace, root, komodoAppliedFile, &a); err != nil {
		return nil, err
	}
	if a.Version != "stackkit.komodo-applied-recipe/v1" || localevidence.VerifyOwnerPolicyState(workspace, a.signingBytes(), a.Signature) != nil || verifyKomodoRecipe(workspace, a.Recipe) != nil || !sameKomodoTarget(a.Custody, a.Recipe.Intent.Deployment) || a.Custody.Signature == nil || a.Custody.Deployment.ObservedStatus != "running" || a.Custody.ArtifactDigest != a.Recipe.Intent.Deployment.ArtifactDigest || !komodoStackIDPattern.MatchString(a.Custody.Deployment.ExternalID) {
		return nil, errors.New("Komodo applied recipe checkpoint is not authenticated")
	}
	return &a, nil
}
func loadKomodoUpdate(workspace, root string) (*komodoPendingUpdate, error) {
	var u komodoPendingUpdate
	if err := readKomodoRecord(workspace, root, komodoUpdateFile, &u); err != nil {
		return nil, err
	}
	if u.Version != "stackkit.komodo-pending-update/v1" || localevidence.VerifyOwnerPolicyState(workspace, u.signingBytes(), u.Signature) != nil || verifyKomodoRecipe(workspace, u.Target) != nil {
		return nil, errors.New("Komodo pending update is not authenticated")
	}
	if u.Source != nil {
		a := u.Source
		if a.Version != "stackkit.komodo-applied-recipe/v1" || localevidence.VerifyOwnerPolicyState(workspace, a.signingBytes(), a.Signature) != nil || verifyKomodoRecipe(workspace, a.Recipe) != nil || a.Custody.Signature == nil || a.Custody.Deployment.ObservedStatus != "running" || !sameKomodoTarget(a.Custody, u.Target.Intent.Deployment) || a.Custody.ArtifactDigest != a.Recipe.Intent.Deployment.ArtifactDigest || !komodoStackIDPattern.MatchString(a.Custody.Deployment.ExternalID) {
			return nil, errors.New("Komodo pending update lost its authenticated applied source")
		}
	}
	if len(u.PriorIntents) > 16 {
		return nil, errors.New("Komodo pending intent transition bound exceeded")
	}
	for _, old := range u.PriorIntents {
		if old.Version != u.Target.Intent.Version || localevidence.VerifyOwnerPolicyState(workspace, old.signingBytes(), old.Signature) != nil || !sameKomodoIntentIdentity(old, u.Target.Intent) || (old.HeldID != "" && !komodoStackIDPattern.MatchString(old.HeldID)) {
			return nil, errors.New("Komodo prior pending intent is not authenticated to its exact target")
		}
	}
	if len(u.CustodyWitnesses) > 16 {
		return nil, errors.New("Komodo pending custody witness bound exceeded")
	}
	for _, w := range u.CustodyWitnesses {
		canonical, err := canonicalKomodoCustody(workspace, w.Custody)
		if err != nil || !sameKomodoRecord(canonical, w.Custody) || !sameKomodoTarget(w.Custody, u.Target.Intent.Deployment) || w.Custody.ArtifactDigest != u.Target.Intent.Deployment.ArtifactDigest || !komodoStackIDPattern.MatchString(w.Custody.Deployment.ExternalID) || (w.Observed && w.Custody.Deployment.ObservedStatus != "running") {
			return nil, errors.New("Komodo pending custody witness is not authenticated")
		}
	}
	return &u, nil
}
func komodoRetainedRef(project, digest string, part int) string {
	return fmt.Sprintf("secret://komodo-provider/recipes/%s/%s/%d", project, strings.TrimPrefix(digest, "sha256:"), part)
}
func retainKomodoFile(workspace, project string, material []byte, allowEmpty bool) (komodoRetainedFile, error) {
	r := komodoRetainedFile{Digest: digestBytes(material), Size: len(material)}
	if len(material) == 0 && !allowEmpty || len(material) > 1<<20 {
		return r, errors.New("Komodo retained recipe file exceeds its 1 MiB bound")
	}
	// Canonical Files has an empty .env. Its signed size/digest and zero refs
	// represent the exact empty file; nonempty material keeps encrypted custody.
	// CA bundles can exceed the secret store's per-material limit. Keep that
	// custody boundary unchanged and retain independently encrypted chunks.
	for offset := 0; offset < len(material); offset += 8192 {
		ref := komodoRetainedRef(project, r.Digest, len(r.Refs))
		if err := localevidence.StoreEncryptedLocalIssuedSecret(workspace, ref, material[offset:min(offset+8192, len(material))]); err != nil {
			return r, errors.New("Komodo recipe retention requires existing encrypted node custody")
		}
		r.Refs = append(r.Refs, ref)
	}
	return r, nil
}
func retainKomodoRecipe(workspace string, intent komodoActionIntent, bundle []byte, files map[string][]byte) (komodoRecipe, error) {
	r := komodoRecipe{Intent: intent, Files: map[string]komodoRetainedFile{}}
	var err error
	r.Bundle, err = retainKomodoFile(workspace, intent.Deployment.Deployment.AppName, bundle, false)
	if err != nil {
		return r, err
	}
	for name, data := range files {
		r.Files[name], err = retainKomodoFile(workspace, intent.Deployment.Deployment.AppName, data, name == EnvFile && intent.Deployment.WorkloadRef == "files")
		if err != nil {
			return r, err
		}
	}
	return r, verifyKomodoRecipe(workspace, r)
}
func openKomodoRecipe(workspace string, r komodoRecipe) ([]byte, map[string][]byte, error) {
	if err := verifyKomodoRecipe(workspace, r); err != nil {
		return nil, nil, err
	}
	project := r.Intent.Deployment.Deployment.AppName
	open := func(f komodoRetainedFile, allowEmpty bool) ([]byte, error) {
		if f.Size < 0 || f.Size == 0 && !allowEmpty || f.Size > 1<<20 || len(f.Refs) != (f.Size+8191)/8192 {
			return nil, errors.New("Komodo retained recipe exceeds its chunk bounds")
		}
		data := make([]byte, 0, f.Size)
		for part, ref := range f.Refs {
			if ref != komodoRetainedRef(project, f.Digest, part) {
				clear(data)
				return nil, errors.New("Komodo retained locator differs from its signed content")
			}
			chunk, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, ref)
			if err != nil || len(chunk) != min(8192, f.Size-len(data)) {
				clear(chunk)
				clear(data)
				return nil, errors.New("Komodo encrypted retained recipe cannot be authenticated")
			}
			data = append(data, chunk...)
			clear(chunk)
		}
		if digestBytes(data) != f.Digest {
			clear(data)
			return nil, errors.New("Komodo encrypted retained recipe cannot be authenticated")
		}
		return data, nil
	}
	bundle, err := open(r.Bundle, false)
	if err != nil {
		return nil, nil, err
	}
	files := map[string][]byte{}
	for name, f := range r.Files {
		if path.Clean(name) != name || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			clear(bundle)
			clearKomodoFiles(files)
			return nil, nil, errors.New("Komodo retained recipe has a foreign file path")
		}
		files[name], err = open(f, name == EnvFile && r.Intent.Deployment.WorkloadRef == "files")
		if err != nil {
			clear(bundle)
			clearKomodoFiles(files)
			return nil, nil, err
		}
	}
	return bundle, files, nil
}
func clearKomodoFiles(files map[string][]byte) {
	for _, data := range files {
		clear(data)
	}
}
func requireKomodoRecipeFiles(root string, r komodoRecipe) error {
	for name, f := range r.Files {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(root), filepath.FromSlash(name)))
		actual := digestBytes(data)
		clear(data)
		if err != nil || actual != f.Digest {
			return errors.New("Komodo persisted files differ from the authenticated pending recipe")
		}
	}
	return nil
}

// A crash may leave any staged file on either authenticated side of the
// update. Arbitrary disk edits still refuse recovery. Missing new files are
// admitted only when the source never declared them.
func requireKomodoTransitionFiles(root string, u *komodoPendingUpdate) error {
	for name, target := range u.Target.Files {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(root), filepath.FromSlash(name)))
		actual := digestBytes(data)
		clear(data)
		if err == nil && actual == target.Digest {
			continue
		}
		if u.Source != nil {
			source, exists := u.Source.Recipe.Files[name]
			if (err == nil && exists && actual == source.Digest) || (errors.Is(err, os.ErrNotExist) && !exists) {
				continue
			}
		}
		return errors.New("Komodo runtime files match neither authenticated side of the pending update")
	}
	return nil
}

func pendingKomodoRecoveryIntent(workspace, root string, current komodoActionIntent, artifact string) (komodoActionIntent, *komodoPendingUpdate, error) {
	u, err := loadKomodoUpdate(workspace, root)
	if errors.Is(err, os.ErrNotExist) {
		return current, nil, nil
	}
	if err != nil {
		return current, nil, err
	}
	admitted := sameKomodoRecord(current, u.Target.Intent) || (u.Source != nil && sameKomodoRecord(current, u.Source.Recipe.Intent))
	for _, old := range u.PriorIntents {
		admitted = admitted || sameKomodoRecord(current, old)
	}
	if u.Target.Intent.Deployment.ArtifactDigest != artifact || !admitted {
		return current, nil, errors.New("Komodo recovery must resume the authenticated pending target artifact; source rollback requires its own authorized Apply")
	}
	if err := requireKomodoTransitionFiles(root, u); err != nil {
		return current, nil, err
	}
	return u.Target.Intent, u, nil
}
func saveKomodoApplied(workspace, root string, recipe komodoRecipe, custody platformdeploy.HostDeployment) error {
	if custody.Signature == nil || custody.Deployment.ObservedStatus != "running" {
		return errors.New("Komodo applied recipe requires API-observed signed running custody")
	}
	a := komodoAppliedRecipe{Version: "stackkit.komodo-applied-recipe/v1", Recipe: recipe, Custody: custody}
	var err error
	a.Signature, err = localevidence.SignOwnerPolicyState(workspace, a.signingBytes())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if len(raw) > 65536 {
		return errors.New("Komodo applied recipe exceeds its signed record bound")
	}
	return writeKomodoAuthority(workspace, root, komodoAppliedFile, raw)
}

// This runs before native preparation replaces anything. Existing roots created
// before encrypted recipe retention need one unchanged original-artifact Apply
// to establish a source checkpoint; an update cannot invent a missing source.
func komodoUpdateSource(workspace, root string, prior platformdeploy.HostDeployment, deployment nativehost.SelectedPaaSWorkloadDeployment) (*komodoAppliedRecipe, error) {
	if prior.Signature == nil {
		return nil, nil
	}
	a, err := loadKomodoApplied(workspace, root)
	if errors.Is(err, os.ErrNotExist) {
		if prior.Deployment.ObservedStatus != "running" {
			u, pendingErr := loadKomodoUpdate(workspace, root)
			if pendingErr == nil && u.Source == nil && sameKomodoTarget(u.Target.Intent.Deployment, prior) && u.Target.Intent.Deployment.ArtifactDigest == prior.ArtifactDigest && deployment.ArtifactDigest == prior.ArtifactDigest {
				// An uncertain first deployment has no applied source yet. Its
				// unchanged pending target may retry without inventing one.
				return nil, nil
			}
			return nil, errors.New("Komodo initial deployment is not observed running; resume its original authenticated pending target before an update")
		}
		var i komodoActionIntent
		if readKomodoRecord(workspace, root, komodoActionFile, &i) != nil || localevidence.VerifyOwnerPolicyState(workspace, i.signingBytes(), i.Signature) != nil || !sameKomodoTarget(i.Deployment, prior) || i.Deployment.ArtifactDigest != prior.ArtifactDigest || deployment.ArtifactDigest != prior.ArtifactDigest {
			return nil, errors.New("Komodo applied recipe checkpoint is missing; reapply the original unchanged artifact before preparing an update; interrupted older updates require recovery of their signed original recipe")
		}
		files := map[string][]byte{}
		for _, name := range []string{ComposeFile, EnvFile} {
			files[name], err = os.ReadFile(filepath.Join(filepath.Dir(root), name))
			if err != nil {
				clearKomodoFiles(files)
				return nil, err
			}
		}
		defer clearKomodoFiles(files)
		// The legacy checkpoint is deliberately limited to the two primary files.
		// The next staged native recipe captures its complete config-file set.
		r, e := retainKomodoRecipe(workspace, i, deployment.Bundle, files)
		if e != nil {
			return nil, e
		}
		if err = requireKomodoRecipeFiles(root, r); err != nil {
			return nil, err
		}
		if err = saveKomodoApplied(workspace, root, r, prior); err != nil {
			return nil, err
		}
		a, err = loadKomodoApplied(workspace, root)
	}
	if err != nil {
		return nil, err
	}
	if !sameKomodoTarget(a.Custody, prior) {
		return nil, errors.New("Komodo applied source belongs to another node/project")
	}
	u, updateErr := loadKomodoUpdate(workspace, root)
	if updateErr != nil && !errors.Is(updateErr, os.ErrNotExist) {
		return nil, updateErr
	}
	if u != nil {
		if !sameKomodoRecord(u.Target, a.Recipe) && u.Target.Intent.Deployment.ArtifactDigest != deployment.ArtifactDigest {
			return nil, errors.New("Komodo update is pending; resume its authenticated target artifact before preparing another update")
		}
		recipe, err := resolveKomodoPendingSource(workspace, root, u, prior)
		if err != nil {
			return nil, err
		}
		if err := requireKomodoRetainedMaterial(workspace, *recipe); err != nil {
			return nil, err
		}
		if u.Source != nil && sameKomodoRecord(*recipe, u.Source.Recipe) {
			return u.Source, nil
		}
		// The exact observed custody or completed checkpoint, rather than artifact
		// equality, established this target as the next applied source.
		if err := saveKomodoApplied(workspace, root, *recipe, prior); err != nil {
			return nil, err
		}
		return loadKomodoApplied(workspace, root)
	}
	if !sameKomodoRecord(a.Custody, prior) {
		return nil, errors.New("Komodo applied custody differs from its retained source; resume the original pending update or exact-ID recovery")
	}
	if err := requireKomodoRetainedMaterial(workspace, a.Recipe); err != nil {
		return nil, err
	}
	return a, nil
}

func journalKomodoUpdate(workspace, root string, source *komodoAppliedRecipe, prior platformdeploy.HostDeployment, intent komodoActionIntent, bundle []byte, files map[string][]byte) error {
	recipe, err := retainKomodoRecipe(workspace, intent, bundle, files)
	if err != nil {
		return err
	}
	u := komodoPendingUpdate{Version: "stackkit.komodo-pending-update/v1", Source: source, Target: recipe}
	old, oldErr := loadKomodoUpdate(workspace, root)
	if oldErr != nil && !errors.Is(oldErr, os.ErrNotExist) {
		return oldErr
	}
	// A retry can stop again before replacing the installed action intent. The
	// unchanged pending target still needs every authenticated prior intent;
	// successful target journaling alone does not confirm action installation.
	if old != nil && sameKomodoRecord(old.Target, recipe) {
		u.PriorIntents = append(u.PriorIntents, old.PriorIntents...)
	}
	if old != nil && !sameKomodoRecord(old.Target.Intent, intent) && old.Target.Intent.HeldID != intent.HeldID {
		exactRecipe := sameKomodoIntentIdentity(old.Target.Intent, intent) && sameKomodoRecord(old.Target.Bundle, recipe.Bundle) && sameKomodoRecord(old.Target.Files, recipe.Files)
		if exactRecipe {
			if prior.Signature == nil || intent.HeldID != prior.Deployment.ExternalID {
				return errors.New("Komodo intent transition has no exact held custody")
			}
			if _, err := resolveKomodoPendingSource(workspace, root, old, prior); err != nil {
				return err
			}
			u.PriorIntents = append(u.PriorIntents, old.PriorIntents...)
			duplicate := false
			for _, i := range u.PriorIntents {
				duplicate = duplicate || sameKomodoRecord(i, old.Target.Intent)
			}
			if !duplicate {
				u.PriorIntents = append(u.PriorIntents, old.Target.Intent)
			}
			if len(u.PriorIntents) > 16 {
				return errors.New("Komodo pending intent transition bound exceeded; retain original evidence for governed reconciliation")
			}
		} else if old.Target.Intent.Deployment.ArtifactDigest == intent.Deployment.ArtifactDigest && (source == nil || !sameKomodoRecord(source.Recipe, old.Target)) {
			return errors.New("Komodo held-ID rewrite requires its unchanged authenticated pending recipe; finish recovery before changing secret/config material")
		}
	}

	// Keep a still-pending exact custody link across the intent's held-ID update.
	// komodoUpdateSource already resolved and opened its authenticated source.
	if source != nil && !sameKomodoRecord(source.Custody, prior) {
		old, err := loadKomodoUpdate(workspace, root)
		if err != nil {
			return err
		}
		basis, err := resolveKomodoPendingSource(workspace, root, old, prior)
		if err != nil || !sameKomodoRecord(*basis, source.Recipe) || prior.ArtifactDigest != intent.Deployment.ArtifactDigest {
			return errors.New("Komodo pending source changed before recipe journaling")
		}
		u.CustodyWitnesses = []komodoCustodyWitness{{Custody: prior}}
	}
	return saveKomodoUpdate(workspace, root, &u)
}
func saveKomodoUpdate(workspace, root string, u *komodoPendingUpdate) error {
	var err error
	u.Signature, err = localevidence.SignOwnerPolicyState(workspace, u.signingBytes())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if len(raw) > 65536 {
		return errors.New("Komodo pending update exceeds its signed record bound")
	}
	return writeKomodoAuthority(workspace, root, komodoUpdateFile, raw)
}

// Authority must reach the containing directory before custody in the separate
// project directory can advance. An installed rename with a failed directory
// sync remains signed retry evidence, never permission to deploy or save custody.
// The enclosing operation/helper already owns the exact project OS lease.
func writeKomodoAuthority(workspace, root, name string, raw []byte) (returnErr error) {
	fs, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, fs.Close()) }()
	relative, err := filepath.Rel(workspace, root)
	if err != nil {
		return err
	}
	view, err := fs.View(filepath.ToSlash(relative))
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(name, raw)
	if err != nil {
		return fmt.Errorf("Komodo authority publication requires a durable directory: %w", err)
	}
	if !result.Installed || !result.FileSynced || !result.DirectorySynced || !result.PermissionsVerified {
		return errors.New("Komodo authority publication requires a synced private file and containing directory; retry the signed pending operation")
	}
	return nil
}
func commitKomodoApplied(workspace, root string, intent komodoActionIntent) error {
	u, err := loadKomodoUpdate(workspace, root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // Old signed roots must bootstrap retention on unchanged Apply.
	if err != nil {
		return err
	}
	if !sameKomodoRecord(u.Target.Intent, intent) {
		return errors.New("Komodo deployed intent differs from its pending journal")
	}
	if err := requireKomodoRecipeFiles(root, u.Target); err != nil {
		return err
	}
	custody, err := platformdeploy.LoadOwnerHostDeployment(workspace, intent.Deployment.Deployment.AppName)
	if err != nil {
		return err
	}
	if !sameKomodoTarget(custody, intent.Deployment) || custody.ArtifactDigest != intent.Deployment.ArtifactDigest {
		return errors.New("Komodo deployed custody differs from its pending journal")
	}
	return saveKomodoApplied(workspace, root, u.Target, custody)
}

// Canonicalize exactly as SaveOwnerHostDeployment does for explicit managers.
// Only an undeclared version can be filled in; declared versions and signatures
// must already agree. The save/readback below detects persistence drift.
func canonicalKomodoCustody(workspace string, state platformdeploy.HostDeployment) (platformdeploy.HostDeployment, error) {
	const version = "stackkit.platform-host-deployment/v2"
	if state.Manager != "komodo" || (state.APIVersion != "" && state.APIVersion != version) {
		return state, errors.New("Komodo custody has an incompatible declared version")
	}
	supplied := state.Signature
	state.APIVersion = version
	state.Signature = nil
	raw, err := json.Marshal(state)
	if err != nil {
		return state, err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, raw)
	if err != nil {
		return state, err
	}
	state.Signature = &signature
	if supplied != nil && !sameKomodoRecord(*supplied, signature) {
		return state, errors.New("Komodo custody signature differs from its canonical persistence")
	}
	return state, nil
}
func saveKomodoCustody(workspace string, state platformdeploy.HostDeployment) error {
	expected, err := canonicalKomodoCustody(workspace, state)
	if err != nil {
		return err
	}
	if err := platformdeploy.SaveOwnerHostDeployment(workspace, expected.Deployment.AppName, expected); err != nil {
		return err
	}
	actual, err := platformdeploy.LoadOwnerHostDeployment(workspace, expected.Deployment.AppName)
	if err != nil {
		return err
	}
	if !sameKomodoRecord(expected, actual) {
		return errors.New("Komodo custody persistence differs from its signed pending witness")
	}
	return nil
}
func requireKomodoRetainedMaterial(workspace string, r komodoRecipe) error {
	bundle, files, err := openKomodoRecipe(workspace, r)
	clear(bundle)
	clearKomodoFiles(files)
	return err
}

// All pending paths use this resolver. A declared source never falls back to B
// because B's artifact equals held custody; only exact authenticated witnesses
// or an API-observed applied checkpoint can retire A.
func resolveKomodoPendingSource(workspace, root string, u *komodoPendingUpdate, prior platformdeploy.HostDeployment) (*komodoRecipe, error) {
	a, err := loadKomodoApplied(workspace, root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if observedKomodoTarget(u, prior) != nil {
		return &u.Target, nil
	}
	if a != nil && sameKomodoRecord(a.Custody, prior) && sameKomodoTarget(a.Custody, u.Target.Intent.Deployment) {
		return &a.Recipe, nil
	}
	if u.Source != nil && sameKomodoRecord(u.Source.Custody, prior) {
		return &u.Source.Recipe, nil
	}
	for _, w := range u.CustodyWitnesses {
		if sameKomodoRecord(w.Custody, prior) {
			if w.Observed || u.Source == nil {
				return &u.Target, nil
			}
			return &u.Source.Recipe, nil
		}
	}
	// Compatibility with signed recovery receipts written before witnesses were
	// added. Normalize the old undeclared target version exactly as persistence
	// does, retaining and verifying every declared field and the previous custody.
	if u.Source != nil && komodoStackIDPattern.MatchString(prior.Deployment.ExternalID) {
		from := os.Getenv(KomodoProviderRecoverFromEnv)
		if komodoStackIDPattern.MatchString(from) {
			var recovered komodoRecovery
			name := "komodo-recovery-" + from + "-" + prior.Deployment.ExternalID + ".json"
			readErr := readKomodoRecord(workspace, root, name, &recovered)
			if readErr == nil {
				expected, canonicalErr := canonicalKomodoCustody(workspace, recovered.Target)
				if recovered.Version != "stackkit.komodo-provider-recovery/v1" || localevidence.VerifyOwnerPolicyState(workspace, recovered.signingBytes(), recovered.Signature) != nil || recovered.PreviousID != from || recovered.TargetID != prior.Deployment.ExternalID || recovered.Previous == nil || !sameKomodoRecord(*recovered.Previous, u.Source.Custody) || recovered.ArtifactDigest != u.Target.Intent.Deployment.ArtifactDigest || recovered.ComposeDigest != u.Target.Intent.ComposeDigest || recovered.EnvDigest != u.Target.Intent.EnvDigest || canonicalErr != nil || !sameKomodoRecord(expected, prior) {
					return nil, errors.New("Komodo recovery custody has no authenticated applied source")
				}
				return &u.Source.Recipe, nil
			}
			if !errors.Is(readErr, os.ErrNotExist) {
				return nil, readErr
			}
		}
	}
	if u.Source == nil && sameKomodoTarget(prior, u.Target.Intent.Deployment) && prior.ArtifactDigest == u.Target.Intent.Deployment.ArtifactDigest && prior.Deployment.ObservedStatus != "running" {
		return &u.Target, nil
	}
	return nil, errors.New("Komodo pending custody has no authenticated applied source; recover its original signed journal and encrypted recipe")
}

// Ordered witnesses form one persistent phase log. A later authenticated
// running target supersedes its exact earlier pending custody, even if the
// final custody write was interrupted. No unrelated ID or artifact can do so.
func observedKomodoTarget(u *komodoPendingUpdate, prior platformdeploy.HostDeployment) *platformdeploy.HostDeployment {
	matched := -1
	for n, w := range u.CustodyWitnesses {
		if sameKomodoRecord(w.Custody, prior) {
			matched = n
		}
	}
	if matched < 0 {
		return nil
	}
	for n := len(u.CustodyWitnesses) - 1; n >= matched; n-- {
		w := u.CustodyWitnesses[n]
		if w.Observed && w.Custody.Deployment.ExternalID == prior.Deployment.ExternalID && sameKomodoTarget(w.Custody, prior) && w.Custody.ArtifactDigest == u.Target.Intent.Deployment.ArtifactDigest && w.Custody.Deployment.ObservedStatus == "running" {
			value := w.Custody
			return &value
		}
	}
	return nil
}

// Caller first opens the exact target's encrypted material and independently
// verifies its physical graph. Journal observation alone never changes custody.
func commitVerifiedKomodoObservation(workspace, root string, u *komodoPendingUpdate, prior platformdeploy.HostDeployment) (platformdeploy.HostDeployment, error) {
	observed := observedKomodoTarget(u, prior)
	if observed == nil {
		return prior, nil
	}
	// A previous attempt may have installed this signed witness but failed its
	// directory sync. Re-publish durably before advancing separate custody.
	if err := saveKomodoUpdate(workspace, root, u); err != nil {
		return prior, err
	}
	if err := saveKomodoCustody(workspace, *observed); err != nil {
		return prior, err
	}
	if err := saveKomodoApplied(workspace, root, u.Target, *observed); err != nil {
		return prior, err
	}
	applied, err := loadKomodoApplied(workspace, root)
	if err != nil {
		return prior, err
	}
	u.Source = applied
	if err := saveKomodoUpdate(workspace, root, u); err != nil {
		return prior, err
	}
	return *observed, nil
}
func (o *KomodoWorkloadOperations) resumeKomodoObservedCustody(ctx context.Context, workspace, root string, prior platformdeploy.HostDeployment) (platformdeploy.HostDeployment, error) {
	if prior.Signature == nil {
		return prior, nil
	}
	u, err := loadKomodoUpdate(workspace, root)
	if errors.Is(err, os.ErrNotExist) {
		return prior, nil
	}
	if err != nil {
		return prior, err
	}
	if observedKomodoTarget(u, prior) == nil {
		return prior, nil
	}
	// A completed checkpoint with byte-identical durable custody is already past
	// this resume boundary. Ordinary Apply performs its usual live observation.
	applied, appliedErr := loadKomodoApplied(workspace, root)
	if appliedErr != nil && !errors.Is(appliedErr, os.ErrNotExist) {
		return prior, appliedErr
	}
	if applied != nil && sameKomodoRecord(applied.Custody, prior) && sameKomodoRecord(applied.Recipe, u.Target) {
		return prior, nil
	}
	bundle, files, err := openKomodoRecipe(workspace, u.Target)
	if err != nil {
		return prior, err
	}
	defer clear(bundle)
	defer clearKomodoFiles(files)
	deployment, err := komodoSourceDeployment(u.Target, bundle)
	if err != nil {
		return prior, err
	}
	if _, err := nativehost.ObserveKomodoRecoveryRecipe(ctx, o.prepared, deployment, files); err != nil {
		return prior, fmt.Errorf("Komodo observed-target phase data-contract admission: %w", err)
	}
	return commitVerifiedKomodoObservation(workspace, root, u, prior)
}
func komodoRecoverySource(workspace, root string, intent komodoActionIntent, prior platformdeploy.HostDeployment) (*komodoRecipe, error) {
	u, err := loadKomodoUpdate(workspace, root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !sameKomodoRecord(u.Target.Intent, intent) {
		return nil, errors.New("Komodo recovery intent differs from its authenticated pending update")
	}
	if err := requireKomodoTransitionFiles(root, u); err != nil {
		return nil, err
	}
	return resolveKomodoPendingSource(workspace, root, u, prior)
}

// The caller has admitted the exact previous custody. Persist this link BEFORE
// saving target custody, and retain it until running observation is durable.
func witnessKomodoCustody(workspace, root string, intent komodoActionIntent, state platformdeploy.HostDeployment, observed bool) (platformdeploy.HostDeployment, error) {
	canonical, err := canonicalKomodoCustody(workspace, state)
	if err != nil {
		return state, err
	}
	u, err := loadKomodoUpdate(workspace, root)
	if err != nil {
		return state, err
	}
	if !sameKomodoRecord(u.Target.Intent, intent) || !sameKomodoTarget(canonical, intent.Deployment) || canonical.ArtifactDigest != intent.Deployment.ArtifactDigest || (observed && canonical.Deployment.ObservedStatus != "running") {
		return state, errors.New("Komodo custody witness differs from the authenticated target")
	}
	witness := komodoCustodyWitness{Custody: canonical, Observed: observed}
	for _, old := range u.CustodyWitnesses {
		if sameKomodoRecord(old, witness) {
			return canonical, saveKomodoUpdate(workspace, root, u)
		}
	}
	if len(u.CustodyWitnesses) >= 16 {
		return state, errors.New("Komodo pending custody witness bound exceeded; retain original evidence for governed reconciliation")
	}
	u.CustodyWitnesses = append(u.CustodyWitnesses, witness)
	return canonical, saveKomodoUpdate(workspace, root, u)
}
func komodoSourceDeployment(recipe komodoRecipe, bundle []byte) (nativehost.SelectedPaaSWorkloadDeployment, error) {
	d, err := parseKomodoProviderBundle(bundle)
	if err != nil {
		return nativehost.SelectedPaaSWorkloadDeployment{}, err
	}
	i := recipe.Intent.Deployment
	if digestBytes(bundle) != i.ArtifactDigest || d.WorkloadRef != i.WorkloadRef || d.InstanceRef != i.InstanceRef || d.SiteRef != i.SiteRef || d.NodeRef != i.NodeRef {
		return nativehost.SelectedPaaSWorkloadDeployment{}, fmt.Errorf("Komodo retained applied bundle differs from its authenticated target")
	}
	return nativehost.SelectedPaaSWorkloadDeployment{WorkloadRef: d.WorkloadRef, ModuleRef: d.ModuleRef, UnitRef: d.EntryComponent, Release: d.Release, SiteRef: d.SiteRef, NodeRef: d.NodeRef, InstanceRef: d.InstanceRef, ArtifactDigest: i.ArtifactDigest, Bundle: bundle, Route: d.Route, RuntimeAdapter: runtimeexecutor.RuntimeAdapterBinding{ID: "komodo", ModuleRef: "stackkits-komodo-core-runtime"}, GenerationTarget: "opentofu"}, nil
}
