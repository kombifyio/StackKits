package identityprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	approvalSchema = "stackkit.local-identity-projection-approval/v1"
	receiptSchema  = "stackkit.local-identity-projection-receipt/v1"
	identityRoot   = ".stackkit/identity/projections"
	evidenceRoot   = ".stackkit/evidence/identity-projections"
	pocketIDAPI    = "http://127.0.0.1:1411"
	pocketIDWait   = 30 * time.Second
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Approval struct {
	ApprovedAt        time.Time                                      `json:"approvedAt"`
	Kind              string                                         `json:"kind"`
	OwnerRef          string                                         `json:"ownerRef"`
	Projection        Projection                                     `json:"projection"`
	ProjectionSHA256  string                                         `json:"projectionSHA256"`
	SchemaVersion     string                                         `json:"schemaVersion"`
	TrustBundleSHA256 string                                         `json:"trustBundleSHA256"`
	OwnerSignature    localevidence.OwnerIdentityProjectionSignature `json:"ownerSignature"`
}

type Receipt struct {
	CloudRequired     bool                                           `json:"cloudRequired"`
	CompletedAt       time.Time                                      `json:"completedAt"`
	DeletionPerformed bool                                           `json:"deletionPerformed"`
	Groups            []string                                       `json:"groups"`
	Kind              string                                         `json:"kind"`
	Operation         string                                         `json:"operation"`
	OwnerRef          string                                         `json:"ownerRef"`
	PocketIDSubject   string                                         `json:"pocketIdSubject,omitempty"`
	ProjectionID      string                                         `json:"projectionId"`
	ProjectionSHA256  string                                         `json:"projectionSHA256"`
	SchemaVersion     string                                         `json:"schemaVersion"`
	Status            string                                         `json:"status"`
	OwnerSignature    localevidence.OwnerIdentityProjectionSignature `json:"ownerSignature"`
}

type Mutator interface {
	Apply(context.Context, Projection) (string, []string, error)
}

type Service struct {
	workspace string
	mutator   Mutator
}

func NewService(workspace string) (*Service, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("identityprojection: workspace is required")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("identityprojection: workspace must be an existing plain directory")
	}
	return &Service{
		workspace: filepath.Clean(absolute),
	}, nil
}

func NewServiceWithMutator(workspace string, mutator Mutator) (*Service, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" || mutator == nil {
		return nil, errors.New("identityprojection: workspace and mutator are required")
	}
	return &Service{workspace: filepath.Clean(absolute), mutator: mutator}, nil
}

func (s *Service) Inspect(raw []byte, now time.Time) (Inspection, error) {
	verified, _, err := s.verifyExternal(raw, now, false)
	if err != nil {
		return Inspection{}, err
	}
	return verified.Inspection(), nil
}

// Approve verifies external issuer trust and expiry, then records a separate
// local Owner signature. It performs no PocketID request or other network I/O.
func (s *Service) Approve(raw []byte, now time.Time) (Approval, error) {
	verified, trustSHA256, err := s.verifyExternal(raw, now, false)
	if err != nil {
		return Approval{}, err
	}
	approval := Approval{
		ApprovedAt:        now.UTC().Truncate(time.Second),
		Kind:              "LocalIdentityProjectionApproval",
		OwnerRef:          verified.Projection.OwnerRef,
		Projection:        verified.Projection,
		ProjectionSHA256:  verified.SHA256,
		SchemaVersion:     approvalSchema,
		TrustBundleSHA256: trustSHA256,
	}
	unsigned, err := approval.canonical(false)
	if err != nil {
		return Approval{}, err
	}
	approval.OwnerSignature, err =
		localevidence.SignOwnerIdentityProjectionApproval(s.workspace, unsigned)
	if err != nil {
		return Approval{}, err
	}
	canonical, err := approval.canonical(true)
	if err != nil {
		return Approval{}, err
	}
	if err := writePrivateAtomic(s.workspace, approvalPath(verified.SHA256), canonical); err != nil {
		return Approval{}, err
	}
	return s.loadApproval(verified.SHA256, now, false, true)
}

// Apply returns an existing verified receipt before consulting projection
// expiry or PocketID. Therefore expiry, trust rotation, or Cloud loss can
// never undo an already locally applied identity.
func (s *Service) Apply(ctx context.Context, projectionSHA256 string, now time.Time) (Receipt, error) {
	if receipt, err := s.loadReceipt(projectionSHA256, "apply"); err == nil {
		return receipt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, err
	}
	approval, err := s.loadApproval(projectionSHA256, now, false, true)
	if err != nil {
		return Receipt{}, err
	}
	mutator, err := s.applyMutator()
	if err != nil {
		return Receipt{}, err
	}
	subject, groups, err := mutator.Apply(ctx, approval.Projection)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		CloudRequired:     false,
		CompletedAt:       now.UTC().Truncate(time.Second),
		DeletionPerformed: false,
		Groups:            slices.Clone(groups),
		Kind:              "LocalIdentityProjectionReceipt",
		Operation:         "apply",
		OwnerRef:          approval.OwnerRef,
		PocketIDSubject:   subject,
		ProjectionID:      approval.Projection.ProjectionID,
		ProjectionSHA256:  approval.ProjectionSHA256,
		SchemaVersion:     receiptSchema,
		Status:            "applied",
	}
	return s.persistReceipt(receipt)
}

// applyMutator loads the local PocketID admin credential only at the precise
// mutation boundary. Inspect, approve, unlink, and existing-receipt reads do
// not touch that credential and remain network-free.
func (s *Service) applyMutator() (Mutator, error) {
	if s.mutator != nil {
		return s.mutator, nil
	}
	adminKey, err := localevidence.ReadBasementRuntimePocketIDAdminKey(s.workspace)
	if err != nil {
		return nil, fmt.Errorf("identityprojection: load local PocketID runtime custody: %w", err)
	}
	return &pocketIDMutator{
		workspace: s.workspace,
		client:    pocketid.NewClient(pocketIDAPI, adminKey),
	}, nil
}

// Unlink records only that the optional external projection is detached.
// It deliberately has no Mutator call and no delete operation. Local users,
// groups, Owner custody, keys, TinyAuth, and lifecycle state remain untouched.
func (s *Service) Unlink(projectionSHA256 string, now time.Time) (Receipt, error) {
	if receipt, err := s.loadReceipt(projectionSHA256, "unlink"); err == nil {
		return receipt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, err
	}
	approval, err := s.loadApproval(projectionSHA256, now, true, false)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		CloudRequired:     false,
		CompletedAt:       now.UTC().Truncate(time.Second),
		DeletionPerformed: false,
		Groups:            []string{},
		Kind:              "LocalIdentityProjectionReceipt",
		Operation:         "unlink",
		OwnerRef:          approval.OwnerRef,
		ProjectionID:      approval.Projection.ProjectionID,
		ProjectionSHA256:  approval.ProjectionSHA256,
		SchemaVersion:     receiptSchema,
		Status:            "detached-no-delete",
	}
	return s.persistReceipt(receipt)
}

func (s *Service) verifyExternal(
	raw []byte,
	now time.Time,
	allowExpired bool,
) (Verified, string, error) {
	owner, err := localevidence.LoadOwnerCustody(s.workspace)
	if err != nil {
		return Verified{}, "", err
	}
	trust, err := advancedtrust.Load(s.workspace)
	if err != nil {
		return Verified{}, "", fmt.Errorf("identityprojection: load Owner-approved issuer trust: %w", err)
	}
	verified, err := Verify(raw, trust.TrustBundle(), owner.OwnerRef, now, allowExpired)
	if err != nil {
		return Verified{}, "", err
	}
	return verified, trust.BundleSHA256, nil
}

func (s *Service) loadApproval(
	projectionSHA256 string,
	now time.Time,
	allowExpired bool,
	reverifyExternal bool,
) (Approval, error) {
	raw, err := readPrivate(s.workspace, approvalPath(projectionSHA256))
	if err != nil {
		return Approval{}, err
	}
	var approval Approval
	if err := decodeCanonical(raw, &approval); err != nil {
		return Approval{}, err
	}
	if approval.SchemaVersion != approvalSchema ||
		approval.Kind != "LocalIdentityProjectionApproval" ||
		approval.ProjectionSHA256 != projectionSHA256 ||
		!digestPattern.MatchString(approval.TrustBundleSHA256) {
		return Approval{}, errors.New("identityprojection: local approval is malformed")
	}
	projectionRaw, err := approval.Projection.MarshalCanonical()
	if err != nil {
		return Approval{}, err
	}
	sum := sha256.Sum256(projectionRaw)
	if projectionSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		return Approval{}, errors.New("identityprojection: local approval projection digest differs")
	}
	unsigned, err := approval.canonical(false)
	if err != nil {
		return Approval{}, err
	}
	if err := localevidence.VerifyOwnerIdentityProjectionApproval(
		s.workspace, unsigned, approval.OwnerSignature,
	); err != nil {
		return Approval{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(s.workspace)
	if err != nil || approval.OwnerRef != owner.OwnerRef ||
		approval.Projection.OwnerRef != owner.OwnerRef {
		return Approval{}, errors.New("identityprojection: local approval differs from current Owner custody")
	}
	if reverifyExternal {
		verified, trustSHA, err := s.verifyExternal(projectionRaw, now, allowExpired)
		if err != nil || verified.SHA256 != projectionSHA256 ||
			trustSHA != approval.TrustBundleSHA256 {
			return Approval{}, errors.New("identityprojection: approved external projection or issuer trust changed")
		}
	}
	return approval, nil
}

func (s *Service) persistReceipt(receipt Receipt) (Receipt, error) {
	unsigned, err := receipt.canonical(false)
	if err != nil {
		return Receipt{}, err
	}
	receipt.OwnerSignature, err =
		localevidence.SignOwnerIdentityProjectionReceipt(s.workspace, unsigned)
	if err != nil {
		return Receipt{}, err
	}
	raw, err := receipt.canonical(true)
	if err != nil {
		return Receipt{}, err
	}
	if err := writePrivateAtomic(
		s.workspace,
		receiptPath(receipt.ProjectionSHA256, receipt.Operation),
		raw,
	); err != nil {
		return Receipt{}, err
	}
	return s.loadReceipt(receipt.ProjectionSHA256, receipt.Operation)
}

func (s *Service) loadReceipt(projectionSHA256, operation string) (Receipt, error) {
	raw, err := readPrivate(s.workspace, receiptPath(projectionSHA256, operation))
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := decodeCanonical(raw, &receipt); err != nil {
		return Receipt{}, err
	}
	if receipt.SchemaVersion != receiptSchema ||
		receipt.Kind != "LocalIdentityProjectionReceipt" ||
		receipt.ProjectionSHA256 != projectionSHA256 ||
		receipt.Operation != operation ||
		receipt.CloudRequired || receipt.DeletionPerformed {
		return Receipt{}, errors.New("identityprojection: receipt is malformed or claims external/delete authority")
	}
	if operation == "apply" &&
		(receipt.Status != "applied" || receipt.PocketIDSubject == "") {
		return Receipt{}, errors.New("identityprojection: Apply receipt is incomplete")
	}
	if operation == "unlink" &&
		(receipt.Status != "detached-no-delete" || receipt.PocketIDSubject != "") {
		return Receipt{}, errors.New("identityprojection: unlink receipt must be no-delete")
	}
	unsigned, err := receipt.canonical(false)
	if err != nil {
		return Receipt{}, err
	}
	if err := localevidence.VerifyOwnerIdentityProjectionReceipt(
		s.workspace, unsigned, receipt.OwnerSignature,
	); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (approval Approval) canonical(includeSignature bool) ([]byte, error) {
	copy := approval
	if !includeSignature {
		copy.OwnerSignature = localevidence.OwnerIdentityProjectionSignature{}
	}
	return resolvedplan.CanonicalJSON(copy)
}

func (receipt Receipt) canonical(includeSignature bool) ([]byte, error) {
	copy := receipt
	if !includeSignature {
		copy.OwnerSignature = localevidence.OwnerIdentityProjectionSignature{}
	}
	return resolvedplan.CanonicalJSON(copy)
}

func decodeCanonical(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > maxDocumentBytes {
		return errors.New("identityprojection: local record is unbounded")
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("identityprojection: decode local record: %w", err)
	}
	canonical, err := resolvedplan.CanonicalJSON(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("identityprojection: local record is not canonical")
	}
	return nil
}

func approvalPath(digest string) string {
	return filepath.ToSlash(filepath.Join(identityRoot, digestHex(digest)+".approval.json"))
}

func receiptPath(digest, operation string) string {
	return filepath.ToSlash(filepath.Join(
		evidenceRoot, digestHex(digest)+"."+operation+".receipt.json",
	))
}

func digestHex(digest string) string {
	if !digestPattern.MatchString(digest) {
		return "invalid"
	}
	return strings.TrimPrefix(digest, "sha256:")
}

func writePrivateAtomic(workspace, relative string, data []byte) error {
	target := filepath.Join(workspace, filepath.FromSlash(relative))
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := backupcustody.ProtectPrivatePath(directory, true); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".identity-projection-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		existing, readErr := os.ReadFile(target)
		if readErr == nil && slices.Equal(existing, data) {
			return nil
		}
		return errors.New("identityprojection: immutable local record already exists with different bytes")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tempName, target); err != nil {
		return err
	}
	return backupcustody.ProtectPrivatePath(target, false)
}

func readPrivate(workspace, relative string) ([]byte, error) {
	target := filepath.Join(workspace, filepath.FromSlash(relative))
	if _, err := os.Lstat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	if err := backupcustody.RequirePrivatePath(filepath.Dir(target), true); err != nil {
		return nil, err
	}
	if err := backupcustody.RequirePrivatePath(target, false); err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

type pocketIDClient interface {
	WaitHealthy(context.Context, time.Duration) error
	FindUsersByUsername(context.Context, string) ([]pocketid.User, error)
	CreateUser(context.Context, pocketid.CreateUserRequest) (*pocketid.User, error)
	GetUser(context.Context, string) (*pocketid.User, error)
	UpdateUserGroups(context.Context, string, []string) (*pocketid.User, error)
	GetGroupIDByName(context.Context, string) (string, error)
	CreateUserGroup(context.Context, pocketid.CreateUserGroupRequest) (*pocketid.UserGroup, error)
}

type pocketIDMutator struct {
	workspace string
	client    pocketIDClient
}

func (m *pocketIDMutator) Apply(
	ctx context.Context,
	projection Projection,
) (string, []string, error) {
	if ctx == nil || m == nil || m.client == nil {
		return "", nil, errors.New("identityprojection: PocketID mutator is not initialized")
	}
	owner, err := localevidence.LoadOwnerCustody(m.workspace)
	if err != nil {
		return "", nil, err
	}
	if projection.Profile.Username == owner.PocketID.Username ||
		projection.Profile.Email == owner.PocketID.Email {
		return "", nil, errors.New("identityprojection: convenience projection cannot replace the local Owner")
	}
	if err := m.client.WaitHealthy(ctx, pocketIDWait); err != nil {
		return "", nil, errors.New("identityprojection: local PocketID is unavailable")
	}
	groupIDs := make([]string, 0, len(projection.Groups))
	for _, name := range projection.Groups {
		id, err := m.client.GetGroupIDByName(ctx, name)
		if err != nil {
			return "", nil, errors.New("identityprojection: PocketID group lookup failed")
		}
		if id == "" {
			created, createErr := m.client.CreateUserGroup(
				ctx,
				pocketid.CreateUserGroupRequest{
					Name: name, FriendlyName: name,
				},
			)
			if errors.Is(createErr, pocketid.ErrAlreadyExists) {
				id, createErr = m.client.GetGroupIDByName(ctx, name)
			} else if createErr == nil && created != nil {
				id = created.ID
			}
			if createErr != nil || id == "" {
				return "", nil, errors.New("identityprojection: PocketID group creation failed")
			}
		}
		groupIDs = append(groupIDs, id)
	}
	users, err := m.client.FindUsersByUsername(ctx, projection.Profile.Username)
	if err != nil || len(users) > 1 {
		return "", nil, errors.New("identityprojection: PocketID username lookup is ambiguous")
	}
	var user *pocketid.User
	if len(users) == 0 {
		user, err = m.client.CreateUser(ctx, pocketid.CreateUserRequest{
			Username:     projection.Profile.Username,
			Email:        projection.Profile.Email,
			FirstName:    projection.Profile.DisplayName,
			DisplayName:  projection.Profile.DisplayName,
			IsAdmin:      false,
			Disabled:     false,
			UserGroupIDs: slices.Clone(groupIDs),
		})
	} else {
		copy := users[0]
		user = &copy
	}
	if err != nil || user == nil || user.ID == "" ||
		user.Username != projection.Profile.Username ||
		user.Email != projection.Profile.Email ||
		effectiveDisplayName(*user) != projection.Profile.DisplayName ||
		user.IsAdmin || user.Disabled {
		return "", nil, errors.New("identityprojection: PocketID user conflicts with approved projection")
	}
	for _, existing := range user.UserGroups {
		if strings.TrimSpace(existing.ID) != "" {
			groupIDs = append(groupIDs, existing.ID)
		}
	}
	slices.Sort(groupIDs)
	groupIDs = slices.Compact(groupIDs)
	user, err = m.client.UpdateUserGroups(ctx, user.ID, groupIDs)
	if err != nil || user == nil || user.IsAdmin || user.Disabled {
		return "", nil, errors.New("identityprojection: PocketID group mutation failed")
	}
	readback, err := m.client.GetUser(ctx, user.ID)
	if err != nil || readback == nil ||
		readback.Username != projection.Profile.Username ||
		readback.Email != projection.Profile.Email ||
		effectiveDisplayName(*readback) != projection.Profile.DisplayName ||
		readback.IsAdmin || readback.Disabled {
		return "", nil, errors.New("identityprojection: PocketID readback differs from approved projection")
	}
	return readback.ID, slices.Clone(projection.Groups), nil
}

func effectiveDisplayName(user pocketid.User) string {
	if strings.TrimSpace(user.DisplayName) != "" {
		return strings.TrimSpace(user.DisplayName)
	}
	return strings.TrimSpace(user.FirstName)
}

var _ Mutator = (*pocketIDMutator)(nil)
var _ pocketIDClient = (*pocketid.Client)(nil)
