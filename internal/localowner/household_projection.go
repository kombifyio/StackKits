package localowner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
	"github.com/kombifyio/stackkits/internal/pocketidruntime"
)

// HouseholdProjection binds the Cloud owner/person generation to the node's
// actual identity mutation. Key is SHA-256(owner subject + newline + member
// subject), produced by Techstack after verifying Cloud's delegated owner.
type HouseholdProjection struct {
	Key      string
	Revision uint64
	Action   string
	User     HouseholdUserSpec
}

type householdProjectionState struct {
	Schema             string                                  `json:"schema"`
	Key                string                                  `json:"key"`
	Revision           uint64                                  `json:"revision"`
	Digest             string                                  `json:"digest"`
	Action             string                                  `json:"action"`
	Username           string                                  `json:"username"`
	PocketIDSubject    string                                  `json:"pocketIDSubject"`
	SubjectReserved    bool                                    `json:"subjectReserved"`
	CreationPending    bool                                    `json:"creationPending"`
	CreationAttempted  bool                                    `json:"creationAttempted"`
	RemovalAttempted   bool                                    `json:"removalAttempted"`
	Applied            bool                                    `json:"applied"`
	MembershipContract string                                  `json:"membershipContract,omitempty"`
	Signature          localevidence.OwnerPolicyStateSignature `json:"signature"`
}

var householdProjectionKey = regexp.MustCompile(`^[a-f0-9]{64}$`)
var householdProjectionUsername = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ProjectHouseholdUser must execute under the node lifecycle mutation lock.
// The provider UUID is signed before POST /users. Only that UUID can recover
// an uncertain creation; a username, email or mutable profile is not custody.
func (s *Service) ProjectHouseholdUser(ctx context.Context, request HouseholdProjection) (HouseholdUser, error) {
	request.User.Username = strings.TrimSpace(request.User.Username)
	if !householdProjectionKey.MatchString(request.Key) || request.Revision == 0 ||
		!householdProjectionUsername.MatchString(request.User.Username) ||
		(request.Action != "add" && request.Action != "remove") {
		return HouseholdUser{}, errors.New("localowner: invalid household projection identity")
	}
	access, err := normalizeHouseholdAccess(request.User.Access)
	if err != nil {
		return HouseholdUser{}, err
	}
	slices.Sort(access.Apps)
	request.User.Access = access
	// Mutable profile metadata never blocks an existing subject's revoke.
	effect := struct {
		Key, Action, Username string
		Revision              uint64
		Access                HouseholdAppAccess
	}{request.Key, request.Action, request.User.Username, request.Revision, access}
	raw, err := json.Marshal(effect)
	if err != nil {
		return HouseholdUser{}, err
	}
	digest := sha256.Sum256(raw)
	state := householdProjectionState{Schema: "stackkit.household-projection/v2", Key: request.Key,
		Revision: request.Revision, Digest: hex.EncodeToString(digest[:]), Action: request.Action, Username: request.User.Username}
	newIncarnation := func() error {
		id, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		state.PocketIDSubject = id.String()
		state.SubjectReserved, state.CreationPending = false, true
		state.CreationAttempted, state.RemovalAttempted, state.Applied = false, false, false
		state.MembershipContract = ""
		return nil
	}
	statePath := ".stackkit/custody/household-projection-" + request.Key + ".json"
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return HouseholdUser{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return HouseholdUser{}, err
	}
	stored, _, readErr := tx.ReadStableBounded(statePath, 4096)
	_ = tx.Close()
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return HouseholdUser{}, readErr
	}
	if readErr == nil {
		var previous householdProjectionState
		if err := json.Unmarshal(stored, &previous); err != nil {
			return HouseholdUser{}, err
		}
		signature := previous.Signature
		previous.Signature = localevidence.OwnerPolicyStateSignature{}
		canonical, err := json.Marshal(previous)
		if err != nil {
			return HouseholdUser{}, err
		}
		if err := localevidence.VerifyOwnerPolicyState(s.workspaceRoot, canonical, signature); err != nil {
			return HouseholdUser{}, err
		}
		if previous.Schema != state.Schema || previous.Key != request.Key || previous.Username != request.User.Username ||
			(previous.Action != "add" && previous.Action != "remove") ||
			(previous.PocketIDSubject == "" && previous.Action != "remove") ||
			request.Revision < previous.Revision || (request.Revision == previous.Revision && state.Digest != previous.Digest) {
			return HouseholdUser{}, errors.New("localowner: stale, unbound or conflicting household projection")
		}
		if previous.PocketIDSubject != "" {
			if id, err := uuid.Parse(previous.PocketIDSubject); err != nil || id == uuid.Nil || id.String() != previous.PocketIDSubject {
				return HouseholdUser{}, errors.New("localowner: invalid household subject custody")
			}
		}
		state.PocketIDSubject, state.CreationPending = previous.PocketIDSubject, previous.CreationPending
		state.SubjectReserved = previous.SubjectReserved
		state.CreationAttempted = previous.CreationAttempted
		state.RemovalAttempted = previous.RemovalAttempted
		if request.Revision == previous.Revision {
			state.MembershipContract = previous.MembershipContract
			state.Applied = previous.Applied && (request.Action == "remove" || previous.MembershipContract == pocketid.MembershipContract)
		} else if previous.Action == "remove" && previous.Applied && request.Action == "add" {
			// A higher add after confirmed removal starts a new incarnation.
			// Delayed provider effects must still address the retired UUID.
			if err := newIncarnation(); err != nil {
				return HouseholdUser{}, err
			}
		}
	}
	if state.PocketIDSubject == "" && request.Action == "add" {
		if err := newIncarnation(); err != nil {
			return HouseholdUser{}, err
		}
	}
	view, err := root.View(".")
	if err != nil {
		return HouseholdUser{}, err
	}
	persist := func() error {
		state.Signature = localevidence.OwnerPolicyStateSignature{}
		canonical, err := json.Marshal(state)
		if err != nil {
			return err
		}
		state.Signature, err = localevidence.SignOwnerPolicyState(s.workspaceRoot, canonical)
		if err != nil {
			return err
		}
		body, err := json.Marshal(state)
		if err != nil {
			return err
		}
		_, err = view.WriteAtomic0600(statePath, body)
		return err
	}
	if err := persist(); err != nil {
		return HouseholdUser{}, err
	}
	// Commit the generation before provider reads, including failed readiness.
	if err := pocketidruntime.Require(ctx, s.workspaceRoot); err != nil {
		return HouseholdUser{}, err
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return HouseholdUser{}, err
	}
	fenced, ok := client.(pocketIDMembershipClient)
	if !ok || fenced.RequireMembershipCAS(ctx) != nil {
		return HouseholdUser{}, errors.New("localowner: provider conditional membership contract unavailable")
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	binding, err := s.Verify(ctx)
	if err != nil {
		return HouseholdUser{}, err
	}
	if binding.OwnerRef != owner.OwnerRef || binding.PocketIDSubject == "" {
		return HouseholdUser{}, errors.New("localowner: household owner custody mismatch")
	}
	reserveSubject := func() error {
		if state.SubjectReserved {
			return nil
		}
		// Persist proof of absence before creation. A crash before this
		// reservation cannot turn an existing UUID collision into custody.
		if _, err := client.GetUser(ctx, state.PocketIDSubject); !errors.Is(err, pocketid.ErrNotFound) {
			return errors.New("localowner: household creation UUID is not proven absent")
		}
		state.SubjectReserved = true
		return persist()
	}
	if request.Action == "add" {
		if err := reserveSubject(); err != nil {
			return HouseholdUser{}, err
		}
	}
	var member HouseholdUser
	if request.Action == "remove" {
		if !state.SubjectReserved {
			// There is no proof that an existing legacy/local name belongs to
			// this projection. Never convert it into deletion authority.
			matches, err := client.FindUsersByUsername(ctx, state.Username)
			if err != nil || len(matches) != 0 {
				return HouseholdUser{}, errors.New("localowner: household removal lacks subject custody")
			}
		} else {
			user, err := client.GetUser(ctx, state.PocketIDSubject)
			if err != nil && !errors.Is(err, pocketid.ErrNotFound) {
				return HouseholdUser{}, errors.New("localowner: household removal subject readback failed")
			}
			if errors.Is(err, pocketid.ErrNotFound) && state.CreationPending && state.CreationAttempted && !state.RemovalAttempted {
				// An uncertain POST may still commit. Absence is not a delete
				// receipt until its reserved subject has actually been observed.
				return HouseholdUser{}, errors.New("localowner: household creation outcome is unknown; removal remains pending")
			}
			if err == nil {
				if err := validateProjectedSubject(user, state.PocketIDSubject, binding.PocketIDSubject); err != nil {
					return HouseholdUser{}, err
				}
				if state.RemovalAttempted {
					return HouseholdUser{}, errors.New("localowner: household deletion outcome is unknown; retry UUID readback only")
				}
				// This exact UUID readback proves the earlier create committed.
				// Save that proof with the delete attempt so a lost DELETE
				// response can finish from absence without the old create fence.
				snapshot, e := fenced.GetMembership(ctx, state.PocketIDSubject)
				if e != nil {
					return HouseholdUser{}, e
				}
				state.CreationPending = false
				state.RemovalAttempted = true
				if err := persist(); err != nil {
					return HouseholdUser{}, err
				}
				if err := fenced.DeleteMembership(ctx, *snapshot); err != nil && !errors.Is(err, pocketid.ErrNotFound) {
					if errors.Is(err, pocketid.ErrMembershipConflict) {
						state.RemovalAttempted = false
						if e := persist(); e != nil {
							return HouseholdUser{}, e
						}
					}
					return HouseholdUser{}, errors.New("localowner: household bound-subject deletion failed")
				}
				if _, err := client.GetUser(ctx, state.PocketIDSubject); !errors.Is(err, pocketid.ErrNotFound) {
					return HouseholdUser{}, errors.New("localowner: household deletion is not confirmed")
				}
			}
		}
		state.CreationPending = false
		state.RemovalAttempted = false
		member = HouseholdUser{Username: state.Username, Status: "removed"}
	} else {
		user, err := client.GetUser(ctx, state.PocketIDSubject)
		if state.RemovalAttempted {
			// Do not regrant/recreate while an earlier DELETE may still
			// commit. A proven absent UUID completes that single attempt.
			if !errors.Is(err, pocketid.ErrNotFound) {
				return HouseholdUser{}, errors.New("localowner: prior household deletion is not confirmed")
			}
			// The higher add retained the old delete marker until this absence
			// proved removal. Sign a fresh incarnation before checking its UUID.
			if err := newIncarnation(); err != nil {
				return HouseholdUser{}, err
			}
			if err := persist(); err != nil {
				return HouseholdUser{}, err
			}
			if err := reserveSubject(); err != nil {
				return HouseholdUser{}, err
			}
		}
		if errors.Is(err, pocketid.ErrNotFound) && state.CreationPending {
			if state.CreationAttempted {
				return HouseholdUser{}, errors.New("localowner: household creation outcome is unknown; retry UUID readback only")
			}
			user, err = s.createProjectedSubject(ctx, client, request.User, state.PocketIDSubject, owner.PocketID.Username, owner.PocketID.Email, func() error {
				state.CreationAttempted = true
				return persist()
			})
		}
		if err != nil {
			return HouseholdUser{}, errors.New("localowner: bound household subject unavailable; retry by reserved UUID only")
		}
		if err := validateProjectedSubject(user, state.PocketIDSubject, binding.PocketIDSubject); err != nil {
			return HouseholdUser{}, err
		}
		if user.Username != state.Username {
			return HouseholdUser{}, errors.New("localowner: household bound subject was renamed")
		}
		if state.CreationPending {
			state.CreationPending = false
			if err := persist(); err != nil {
				return HouseholdUser{}, err
			}
		}
		if !state.Applied {
			if _, err := ensureHouseholdOpenGroup(ctx, client); err != nil {
				return HouseholdUser{}, err
			}
			if err := s.applyHouseholdAccessWithFence(ctx, client, *user, access, true); err != nil {
				return HouseholdUser{}, err
			}
			state.MembershipContract = pocketid.MembershipContract
			user, err = client.GetUser(ctx, state.PocketIDSubject)
			if err != nil || validateProjectedSubject(user, state.PocketIDSubject, binding.PocketIDSubject) != nil || !sameAccess(householdAccessOf(*user), access) {
				return HouseholdUser{}, errors.New("localowner: household bound-subject grant readback failed")
			}
		}
		// Every enrollment operation addresses this UUID too. Both a new
		// pending account and a migrated account need a fresh transient link.
		member, err = s.projectedSubjectEnrollment(ctx, client, *user)
		if err != nil {
			return HouseholdUser{}, err
		}
	}
	state.Applied = true
	if err := persist(); err != nil {
		return HouseholdUser{}, err
	}
	return member, nil
}
