package localowner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	domainMigrationStatePath = ".stackkit/custody/identity-domain-migration.json"
	domainMigrationStateV1   = "stackkit.identity-domain-migration/v1"
	domainMigrationStateV2   = "stackkit.identity-domain-migration/v2"
)

type domainEnrollment struct {
	LegacyCredentials []string `json:"legacyCredentials"`
	Started           bool     `json:"started"`
}

type domainMigrationState struct {
	Version     string                                  `json:"version"`
	SpecPath    string                                  `json:"specPath"`
	BeforeSpec  []byte                                  `json:"beforeSpec,omitempty"`
	AfterSpec   []byte                                  `json:"afterSpec,omitempty"`
	Prepared    bool                                    `json:"prepared"`
	Enrollments map[string]domainEnrollment             `json:"enrollments"`
	Runtime     *domainRuntimeHandoff                   `json:"runtimeHandoff,omitempty"`
	Signature   localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// DomainMigration reports local preparation, never runtime deployment or login.
type DomainMigration struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Status string   `json:"status"`
	Next   []string `json:"next"`
}

// MigrateBasementDomain requires the caller's explicit owner approval and idle
// lifecycle mutation lock. All local writes are authenticated and replayable.
// The existing user subjects, client secrets, CA and signing keys stay intact.
type DomainMigrationIntent interface {
	Candidate([]byte) ([]byte, error)
	Persist(workspace, specPath string, before, after []byte) error
}

func (s *Service) MigrateBasementDomain(ctx context.Context, specPath string, intent DomainMigrationIntent, quiesceRuntime bool) (DomainMigration, error) {
	result := DomainMigration{From: "home", To: "lab.home", Status: "runtime-quiesced", Next: []string{
		"The authenticated prior-release Core containers were gracefully stopped and removed without deleting volumes, networks or images.",
		"Use the preserved prior-release CLI and its packaged tools for generate, apply, then verify; do not use this target CLI for that rollout.",
		"After the prior release verifies on lab.home, use the target CLI: stackkit upgrade --to <exact-target-release>.",
		"The release-upgrade checkpoint covers the migrated prior release on lab.home, not the original home installation. The v0.47.4 bridge is awaiting real-host qualification; see basement-kit/README.md.",
		"Enroll client DNS for lab.home using the existing Owner CA.",
		"Run stackkit user owner activate --owner-approve; household members use stackkit user activate <username> --owner-approve.",
	}}
	specPath = filepath.ToSlash(filepath.Clean(specPath))
	if !quiesceRuntime {
		return result, errors.New("localowner: domain migration requires explicit source runtime quiescence")
	}
	if intent == nil {
		return result, errors.New("localowner: domain migration requires the StackSpec persistence boundary")
	}
	state, err := s.loadDomainMigration()
	if errors.Is(err, os.ErrNotExist) {
		if _, err := s.verifyOwnerRuntime(ctx, false); err != nil {
			return result, err
		}
		root, err := confinedfs.Open(s.workspaceRoot)
		if err != nil {
			return result, err
		}
		tx, err := root.BeginTransaction()
		if err != nil {
			root.Close()
			return result, err
		}
		raw, _, err := tx.ReadStableBounded(specPath, 4<<20)
		tx.Close()
		root.Close()
		if err != nil {
			return result, err
		}
		candidate, err := intent.Candidate(raw)
		if err != nil {
			return result, err
		}
		custody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
		if err != nil {
			return result, err
		}
		if custody.Domain != localevidence.LegacyBasementDomain {
			return result, errors.New("localowner: domain migration requires established home custody")
		}
		_, client, err := s.ready(ctx)
		if err != nil {
			return result, err
		}
		users, err := client.ListUsers(ctx)
		if err != nil {
			return result, err
		}
		if s.domainRuntime == nil {
			return result, errors.New("localowner: domain runtime handoff is unavailable")
		}
		runtimeHandoff, err := s.domainRuntime.Capture(ctx, s.workspaceRoot)
		if err != nil {
			return result, err
		}
		state = domainMigrationState{Version: domainMigrationStateV2, SpecPath: specPath, BeforeSpec: raw, AfterSpec: candidate, Enrollments: map[string]domainEnrollment{}, Runtime: &runtimeHandoff}
		for _, user := range users {
			credentials, err := client.ListUserWebAuthnCredentials(ctx, user.ID)
			if err != nil {
				return result, err
			}
			state.Enrollments[user.ID] = domainEnrollment{LegacyCredentials: credentialIDs(credentials)}
		}
		if err := s.saveDomainMigration(state); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	if state.SpecPath != specPath {
		return result, errors.New("localowner: resume domain migration with the original StackSpec path")
	}
	if state.Version != domainMigrationStateV2 || state.Runtime == nil {
		return result, errors.New("localowner: existing domain migration has no authenticated source runtime capture and cannot be quiesced retroactively")
	}
	if state.Prepared {
		custody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
		if err != nil {
			return result, err
		}
		if custody.Domain != localevidence.MigratedBasementDomain {
			return result, errors.New("localowner: prepared domain migration no longer matches runtime custody")
		}
		if state.Runtime.Phase != "quiesced" {
			return result, errors.New("localowner: prepared domain migration has no completed runtime handoff")
		}
		return result, nil
	}
	if s.domainRuntime == nil {
		return result, errors.New("localowner: domain runtime handoff is unavailable")
	}
	if state.Runtime.Phase == "quiescing" {
		custody, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
		if err != nil {
			return result, err
		}
		if custody.Domain != localevidence.MigratedBasementDomain {
			return result, errors.New("localowner: runtime quiescence requires migrated runtime custody")
		}
		if err := s.domainRuntime.Quiesce(ctx, s.workspaceRoot, *state.Runtime); err != nil {
			return result, err
		}
		state.Runtime.Phase = "quiesced"
		state.Prepared = true
		state.BeforeSpec, state.AfterSpec = nil, nil
		if err := s.saveDomainMigration(state); err != nil {
			return result, err
		}
		return result, nil
	}
	if state.Runtime.Phase != "captured" {
		return result, errors.New("localowner: domain migration runtime handoff phase is invalid")
	}
	if err := s.domainRuntime.VerifySource(ctx, s.workspaceRoot, *state.Runtime); err != nil {
		return result, err
	}
	if err := localevidence.MigrateBasementRuntimeDomain(s.workspaceRoot); err != nil {
		return result, err
	}
	if err := intent.Persist(s.workspaceRoot, state.SpecPath, state.BeforeSpec, state.AfterSpec); err != nil {
		return result, err
	}
	if err := s.convergeMigratedTinyAuth(ctx); err != nil {
		return result, err
	}
	state.Runtime.Phase = "quiescing"
	if err := s.saveDomainMigration(state); err != nil {
		return result, err
	}
	if err := s.domainRuntime.Quiesce(ctx, s.workspaceRoot, *state.Runtime); err != nil {
		return result, err
	}
	state.Runtime.Phase = "quiesced"
	state.Prepared = true
	// Retire exact-byte constraints only after the captured Core identities have
	// been removed. A signed quiescing retry still needs the original evidence.
	state.BeforeSpec, state.AfterSpec = nil, nil
	if err := s.saveDomainMigration(state); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) convergeMigratedTinyAuth(ctx context.Context) error {
	owner, client, err := s.ready(ctx)
	if err != nil {
		return err
	}
	binding, err := localevidence.LoadBasementTinyAuthPocketIDBinding(s.workspaceRoot)
	if err != nil {
		return err
	}
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return err
	}
	current, err := client.GetOIDCClient(ctx, binding.ClientID)
	if err != nil {
		return err
	}
	if current == nil || current.ID != binding.ClientID || current.Name != tinyAuthClientName || current.IsPublic || !current.IsGroupRestricted || !samePocketIDGroupIDs(current.AllowedUserGroups, groups) {
		return errors.New("localowner: migration refuses a changed TinyAuth client identity or policy")
	}
	if slices.Equal(current.CallbackURLs, []string{"https://auth.home/api/oauth/callback/pocketid"}) ||
		(slices.Equal(current.CallbackURLs, []string{binding.CallbackURL}) && !current.PkceEnabled) {
		updater, ok := client.(oidcClientUpdater)
		if !ok {
			return errors.New("localowner: PocketID client cannot migrate its callback")
		}
		if _, err := updater.UpdateOIDCClient(ctx, binding.ClientID, pocketid.RegisterClientRequest{
			ID: current.ID, Name: current.Name, CallbackURLs: []string{binding.CallbackURL},
			IsPublic: false, IsGroupRestricted: true, PkceEnabled: true,
			RequiresReauthentication: current.RequiresReauthentication,
		}); err != nil {
			return err
		}
	}
	// Secret creation/rotation is deliberately absent: the signed local binding
	// still holds the original confidential-client secret.
	return s.verifyTinyAuthPocketIDBinding(ctx, client, owner, groups)
}

func (s *Service) loadDomainMigration() (domainMigrationState, error) {
	var state domainMigrationState
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return state, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return state, err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStableBounded(domainMigrationStatePath, 8<<20)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	signature := state.Signature
	state.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, err := json.Marshal(state)
	state.Signature = signature
	if err != nil {
		return state, err
	}
	if err := localevidence.VerifyOwnerPolicyState(s.workspaceRoot, payload, signature); err != nil {
		return state, err
	}
	if (state.Version != domainMigrationStateV1 && state.Version != domainMigrationStateV2) || state.SpecPath == "" || state.Enrollments == nil {
		return state, errors.New("localowner: unrecognized domain migration state")
	}
	if state.Version == domainMigrationStateV2 {
		if state.Runtime == nil || validateDomainRuntimeHandoff(*state.Runtime) != nil ||
			(state.Prepared && state.Runtime.Phase != "quiesced") || (!state.Prepared && state.Runtime.Phase == "quiesced") {
			return state, errors.New("localowner: invalid domain migration runtime handoff")
		}
	}
	return state, nil
}

func (s *Service) saveDomainMigration(state domainMigrationState) error {
	state.Signature = localevidence.OwnerPolicyStateSignature{}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	state.Signature, err = localevidence.SignOwnerPolicyState(s.workspaceRoot, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600(domainMigrationStatePath, raw)
	return err
}

func credentialIDs(credentials []pocketid.WebAuthnCredential) []string {
	ids := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		if credential.ID != "" {
			ids = append(ids, credential.ID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (s *Service) passkeyActive(subject string, credentials []pocketid.WebAuthnCredential) (bool, bool, error) {
	state, err := s.loadDomainMigration()
	if errors.Is(err, os.ErrNotExist) {
		return len(credentials) > 0, false, nil
	}
	if err != nil {
		return false, false, err
	}
	enrollment := state.Enrollments[subject]
	// Unknown subjects fail closed too: the server's initial user page can be
	// bounded, and a subject may have been created during an interrupted rollout.
	if !state.Prepared || !enrollment.Started {
		return false, true, nil
	}
	for _, credential := range credentials {
		if credential.ID != "" && !slices.Contains(enrollment.LegacyCredentials, credential.ID) {
			return true, true, nil
		}
	}
	return false, true, nil
}

type oidcIssuerReader interface {
	OIDCIssuer(context.Context) (string, error)
}

// Start reenrollment only after the running PocketID advertises the new issuer.
// Snapshot again at this point so credentials enrolled during rollout at home
// can never make the migrated owner/household status active.
func (s *Service) beginDomainReenrollment(ctx context.Context, client pocketIDOwnerClient, subject string) error {
	state, err := s.loadDomainMigration()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.Prepared {
		return errors.New("localowner: finish domain migration before passkey reenrollment")
	}
	reader, ok := client.(oidcIssuerReader)
	if !ok {
		return errors.New("localowner: cannot verify the running PocketID issuer")
	}
	issuer, err := reader.OIDCIssuer(ctx)
	if err != nil || strings.TrimSuffix(issuer, "/") != "https://id.lab.home" {
		return errors.New("localowner: apply the migrated runtime before enrolling a lab.home passkey")
	}
	enrollment := state.Enrollments[subject]
	if enrollment.Started {
		return nil
	}
	credentials, err := client.ListUserWebAuthnCredentials(ctx, subject)
	if err != nil {
		return err
	}
	enrollment.LegacyCredentials, enrollment.Started = credentialIDs(credentials), true
	state.Enrollments[subject] = enrollment
	return s.saveDomainMigration(state)
}
