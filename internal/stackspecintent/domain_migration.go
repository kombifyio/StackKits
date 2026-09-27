package stackspecintent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"gopkg.in/yaml.v3"
)

// BasementDomainMigrationCandidate admits only the retired Basement domain.
// It changes the domain and explicit route hosts, then validates every field
// against current CUE. It does not generally admit invalid current intent.
func BasementDomainMigrationCandidate(raw []byte) ([]byte, error) {
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	kit, _ := spec["kit"].(map[string]any)
	network, _ := spec["network"].(map[string]any)
	domain, _ := network["domain"].(map[string]any)
	if kit["slug"] != "basement-kit" || domain["base"] != "home" || (domain["subdomainPrefix"] != nil && domain["subdomainPrefix"] != "") {
		return nil, errors.New("domain migration requires native Basement intent with the unprefixed legacy home domain")
	}
	domain["base"] = "lab.home"
	routes, _ := spec["routes"].(map[string]any)
	for _, rawRoute := range routes {
		route, _ := rawRoute.(map[string]any)
		host, _ := route["host"].(string)
		if strings.HasSuffix(host, ".home") {
			route["host"] = strings.TrimSuffix(host, ".home") + ".lab.home"
		}
	}
	candidate, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract("dev"))
	if err != nil {
		return nil, err
	}
	valid, err := service.ValidateStackSpec(candidate)
	if err != nil {
		return nil, fmt.Errorf("validate migrated Basement intent: %w", err)
	}
	return valid.CanonicalStackSpec, nil
}

// PersistBasementDomainMigration is the explicit legacy-domain exception to
// Persist. The frozen original bytes serve as the CAS token because current
// CUE deliberately rejects home. The candidate must be exactly the validated
// narrow transform of those bytes. Interrupted retries accept that same result.
func PersistBasementDomainMigration(workspace, specPath string, before, after []byte) error {
	if err := validateBasementDomainMigration(before, after); err != nil {
		return err
	}
	return lifecyclemutation.WithIdleMutation(workspace, lifecyclemutation.JoinRequest{}, func() error {
		return persistValidatedBasementDomainMigration(workspace, specPath, before, after)
	})
}

func persistBasementDomainMigration(workspace, specPath string, before, after []byte) error {
	if err := validateBasementDomainMigration(before, after); err != nil {
		return err
	}
	return persistValidatedBasementDomainMigration(workspace, specPath, before, after)
}

func validateBasementDomainMigration(before, after []byte) error {
	candidate, err := BasementDomainMigrationCandidate(before)
	if err != nil {
		return err
	}
	if !bytes.Equal(candidate, after) {
		return errors.New("domain migration candidate differs from validated transition")
	}
	return nil
}

func persistValidatedBasementDomainMigration(workspace, specPath string, before, after []byte) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock("intent-authoring/" + specPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, _, err := tx.ReadStableBounded(specPath, 4<<20)
	if err != nil {
		return err
	}
	if bytes.Equal(current, after) {
		return nil
	}
	if !bytes.Equal(current, before) {
		return &Error{Code: ErrCASConflict, Cause: errors.New("StackSpec changed after domain migration was prepared")}
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(specPath, after)
	return err
}

// BasementDomainMigrator supplies the CUE and CAS boundary to local identity
// migration without coupling the runtime identity service to the compiler.
type BasementDomainMigrator struct {
	guard *basementDomainMigrationGuard
}

type basementDomainMigrationGuard struct {
	workspace string
	active    atomic.Bool
}

// WithBasementDomainMigrator holds the lifecycle mutation lock across the
// complete identity migration and supplies a persistence capability valid only
// during callback execution. It is the live command boundary; callers that
// need only StackSpec persistence use PersistBasementDomainMigration instead.
func WithBasementDomainMigrator(
	workspace string,
	join lifecyclemutation.JoinRequest,
	migrate func(BasementDomainMigrator) error,
) error {
	if migrate == nil {
		return errors.New("domain migration callback is required")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	absolute = filepath.Clean(absolute)
	return lifecyclemutation.WithIdleMutation(absolute, join, func() error {
		guard := &basementDomainMigrationGuard{workspace: absolute}
		guard.active.Store(true)
		defer guard.active.Store(false)
		return migrate(BasementDomainMigrator{guard: guard})
	})
}

func (BasementDomainMigrator) Candidate(raw []byte) ([]byte, error) {
	return BasementDomainMigrationCandidate(raw)
}
func (m BasementDomainMigrator) Persist(workspace, specPath string, before, after []byte) error {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if m.guard == nil || !m.guard.active.Load() || filepath.Clean(absolute) != m.guard.workspace {
		return errors.New("domain migration persistence requires the active migration lock")
	}
	return persistBasementDomainMigration(m.guard.workspace, specPath, before, after)
}
