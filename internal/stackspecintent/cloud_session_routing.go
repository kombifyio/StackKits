package stackspecintent

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync/atomic"

	"github.com/kombifyio/stackkits/internal/addressplan"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

type cloudSessionRouteGuard struct {
	active                   atomic.Bool
	workspace, zone, version string
	authority                *architecturev2.Service
}

type CloudSessionRouteMigrator struct{ guard *cloudSessionRouteGuard }

// WithCloudSessionRouteMigrator shares the normal lifecycle lock with Apply,
// Upgrade and removal. Its writer capability expires when the callback ends.
func WithCloudSessionRouteMigrator(workspace, zone, version string, authority *architecturev2.Service, run func(CloudSessionRouteMigrator) error) error {
	if authority == nil || run == nil {
		return errors.New("Cloud session transition requires the canonical CUE authority")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	return lifecyclemutation.WithIdleMutation(absolute, lifecyclemutation.JoinRequest{Command: "address session-zone"}, func() error {
		guard := &cloudSessionRouteGuard{workspace: filepath.Clean(absolute), zone: zone, version: version, authority: authority}
		guard.active.Store(true)
		defer guard.active.Store(false)
		return run(CloudSessionRouteMigrator{guard: guard})
	})
}

func (m CloudSessionRouteMigrator) Candidate(raw []byte) ([]byte, error) {
	if m.guard == nil || !m.guard.active.Load() {
		return nil, errors.New("Cloud session transition requires its active lifecycle lock")
	}
	before, err := m.guard.authority.ValidateStackSpec(raw)
	if err != nil {
		return nil, err
	}
	candidate, err := addressplan.BindSessionZone(before.CanonicalStackSpec, m.guard.zone)
	if err != nil {
		return nil, err
	}
	after, err := m.guard.authority.ValidateStackSpec(candidate)
	if err != nil {
		return nil, err
	}
	return after.CanonicalStackSpec, nil
}

func (m CloudSessionRouteMigrator) Persist(workspace, specPath string, before, after []byte) error {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if m.guard == nil || !m.guard.active.Load() || filepath.Clean(absolute) != m.guard.workspace {
		return errors.New("Cloud session persistence requires its active lifecycle lock")
	}
	candidate, err := m.Candidate(before)
	if err != nil || !bytes.Equal(candidate, after) {
		return errors.New("Cloud session candidate differs from the validated route transition")
	}
	current, err := localevidence.ReadCloudSessionFile(workspace, specPath)
	if err != nil {
		return err
	}
	if bytes.Equal(current, after) {
		return nil
	}
	if !bytes.Equal(current, before) {
		return errors.New("Cloud session StackSpec changed during transition")
	}
	validated, err := m.guard.authority.ValidateStackSpec(before)
	if err != nil {
		return err
	}
	_, err = persist(Request{WorkspaceRoot: workspace, SpecPath: specPath, Candidate: after, ExpectedSpecHash: validated.SpecHash, BuildVersion: m.guard.version, Authority: m.guard.authority})
	return err
}

func (m CloudSessionRouteMigrator) Restore(workspace, specPath string, before, after []byte) error {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	if m.guard == nil || !m.guard.active.Load() || filepath.Clean(absolute) != m.guard.workspace {
		return errors.New("Cloud session restore requires its active lifecycle lock")
	}
	candidate, err := m.Candidate(before)
	if err != nil || !bytes.Equal(candidate, after) {
		return errors.New("Cloud session restore refuses unrelated route intent")
	}
	current, err := localevidence.ReadCloudSessionFile(workspace, specPath)
	if err != nil {
		return err
	}
	if bytes.Equal(current, before) {
		return nil
	}
	if !bytes.Equal(current, after) {
		return errors.New("Cloud session restore refuses unexpected StackSpec drift")
	}
	validatedBefore, err := m.guard.authority.ValidateStackSpec(before)
	if err != nil {
		return err
	}
	if !bytes.Equal(validatedBefore.CanonicalStackSpec, before) {
		return errors.New("Cloud session restore requires the original canonical source intent")
	}
	validatedAfter, err := m.guard.authority.ValidateStackSpec(after)
	if err != nil {
		return err
	}
	_, err = persist(Request{WorkspaceRoot: workspace, SpecPath: specPath, Candidate: before, ExpectedSpecHash: validatedAfter.SpecHash, BuildVersion: m.guard.version, Authority: m.guard.authority})
	return err
}
