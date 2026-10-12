package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
	"github.com/spf13/cobra"
)

// Sealed host delivery on this node: the recipient key and its generations,
// the pinned delivering authority, detachment, Wallet items and SSH access.
// Every envelope is read from stdin, opened and consumed under the lifecycle
// lock; no value reaches argv, logs, results or a stored command.

func newHostDeliveryCommand() *cobra.Command {
	delivery := &cobra.Command{
		Use:   "delivery",
		Short: "Manage this node's sealed host delivery recipient",
		Long: `Sealed host delivery brings Wallet items, backup targets and SSH access to
this node. A delivering authority seals each value to this node's recipient key
(HPKE, pkg/hostdelivery); only this node opens it into its own custody.
Techstack and Guard carry ciphertext and references only.`,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	delivery.AddCommand(newHostDeliveryRecipientCommand(), newHostDeliveryStatusCommand(), newHostDeliveryDetachCommand())
	return delivery
}

func newHostDeliveryRecipientCommand() *cobra.Command {
	var challenge, authorityKey, authorityKeyID string
	var rotate, approved bool
	cmd := &cobra.Command{
		Use:   "recipient",
		Short: "Prove this node's recipient key, pin the delivering authority, or rotate the key",
		Long: `Print a statement, signed by the local Owner key, that binds this node's
recipient key and its generation to this Site and node and echoes the caller's
challenge. The private key never leaves local custody.

--authority-key pins the delivering authority's Ed25519 public key for the
current generation; Wallet items and SSH access open only when signed by it.
A pinned key is never replaced in place. --rotate re-enrolls the node under the
next generation (and with --authority-key pins a new authority): the previous
key still opens deliveries issued before the rotation for at most their
15-minute lifetime and is then destroyed. Rotation also ends a detach.`,
		Example: `  stackkit host delivery recipient --challenge "$CHALLENGE" --authority-key "$WALLET_KEY" --authority-key-id wallet-1 --owner-approve
  stackkit host delivery recipient --challenge "$CHALLENGE" --rotate --owner-approve`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("host delivery recipient requires --owner-approve")
			}
			var authority *hostcustody.AuthorityKey
			if authorityKey != "" || authorityKeyID != "" {
				parsed, err := hostcustody.ParseAuthorityKey(authorityKeyID, authorityKey)
				if err != nil {
					return err
				}
				authority = &parsed
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "host delivery recipient", func() error {
				store := backupcustody.HostStore(workspace)
				now := time.Now().UTC()
				switch {
				case rotate:
					if _, err := hostcustody.RotateRecipient(store, authority, now); err != nil {
						return err
					}
				case authority != nil:
					if err := hostcustody.PinAuthority(store, *authority, now); err != nil {
						return err
					}
				}
				statement, err := hostcustody.RecipientStatement(store, challenge, now)
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), statement)
			})
		},
	}
	cmd.Flags().StringVar(&challenge, "challenge", "", "Fresh value from the delivering authority that the statement must echo")
	cmd.Flags().StringVar(&authorityKey, "authority-key", "", "Delivering authority Ed25519 public key (base64) to pin for this generation")
	cmd.Flags().StringVar(&authorityKeyID, "authority-key-id", "", "Key ID the delivering authority signs envelopes with")
	cmd.Flags().BoolVar(&rotate, "rotate", false, "Re-enroll under the next recipient generation")
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize this node's recipient change")
	return cmd
}

func newHostDeliveryStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the recipient generation, pinned authority, detach state and delivered references",
		Long: `Report this node's recipient generation and fingerprint, retired generations
still openable, the pinned delivering authority, whether the node is detached,
and every delivered secret by reference and version, with its rotation mark.
No value is read.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := hostcustody.ReadStatus(backupcustody.HostStore(getWorkDir()), time.Now().UTC())
			if errors.Is(err, hostcustody.ErrMissing) {
				return errors.New("this node has no recipient key yet; run 'stackkit host delivery recipient'")
			}
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, cmd.CommandPath(), status)
		},
	}
}

func newHostDeliveryDetachCommand() *cobra.Command {
	var approved bool
	cmd := &cobra.Command{
		Use:   "detach",
		Short: "Stop host delivery to this node and list the delivered secrets to rotate",
		Long: `Detach this node from host delivery: every delivered secret is marked as
needing rotation, retired recipient keys are destroyed, and no delivery opens
until the node is re-enrolled with 'host delivery recipient --rotate'. The
result lists the references and versions to rotate where they came from; it
never contains a value. Values already in local custody stay usable here.`,
		Example: `  stackkit host delivery detach --owner-approve`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("host delivery detach requires --owner-approve")
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "host delivery detach", func() error {
				report, err := hostcustody.Detach(backupcustody.HostStore(workspace), time.Now().UTC())
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), report)
			})
		},
	}
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize detaching this node")
	return cmd
}

// readSealedEnvelope reads one bounded envelope from stdin.
func readSealedEnvelope(cmd *cobra.Command) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), hostdelivery.MaxEnvelopeBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > hostdelivery.MaxEnvelopeBytes {
		return nil, errors.New("stdin must carry one sealed host delivery envelope within its size bound")
	}
	return raw, nil
}

func decodeDeliveryPayload(plaintext []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("the delivered payload does not match its operation")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("the delivered payload contains trailing content")
	}
	return nil
}

// --- Wallet items ---------------------------------------------------------------

func newWalletItemCommand() *cobra.Command {
	item := &cobra.Command{
		Use:   "wallet-item",
		Short: "Wallet items delivered to this node's encrypted local custody",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	item.AddCommand(newWalletItemImportCommand(), newWalletItemStatusCommand())
	return item
}

func newWalletItemImportCommand() *cobra.Command {
	var tenantRef string
	var approved bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Open a sealed Wallet item delivery from stdin into encrypted local custody",
		Long: `Read one sealed wallet-item-import or wallet-item-rotate envelope on stdin,
signed by the pinned delivering authority. The node opens it, records it as
consumed, and stores the value encrypted under its local custody key, keyed by
item reference and version. A rotation removes every older version. The result
names the reference and version only.`,
		Example: `  stackkit wallet-item import --tenant-ref "$TENANT" --owner-approve < envelope.json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved || tenantRef == "" {
				return errors.New("wallet-item import requires --tenant-ref and --owner-approve")
			}
			raw, err := readSealedEnvelope(cmd)
			if err != nil {
				return err
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "wallet-item import", func() error {
				store := backupcustody.HostStore(workspace)
				now := time.Now().UTC()
				delivery, err := hostcustody.OpenDelivery(store, raw, hostcustody.Expectation{TenantRef: tenantRef, Operations: []string{hostdelivery.OperationWalletItemImport, hostdelivery.OperationWalletItemRotate}}, now)
				if err != nil {
					return err
				}
				defer backupcustody.Clear(delivery.Plaintext)
				receipt, err := hostcustody.StoreWalletItem(store, delivery, now)
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), receipt)
			})
		},
	}
	cmd.Flags().StringVar(&tenantRef, "tenant-ref", "", "Tenant the sealed delivery must be addressed to")
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize storing this Wallet item")
	return cmd
}

func newWalletItemStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List Wallet items in local custody by reference and version",
		Long:  `List every Wallet item in local custody by tenant, item reference and versions, and whether it needs rotation after a detach. No value is read.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			items, err := hostcustody.ListWalletItems(backupcustody.HostStore(getWorkDir()))
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"items": items})
		},
	}
}

// --- SSH access -----------------------------------------------------------------

func newHostSSHCommand() *cobra.Command {
	ssh := &cobra.Command{
		Use:         "ssh",
		Short:       "SSH certificate access delivered to this node",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	ssh.AddCommand(newHostSSHApplyCommand())
	return ssh
}

// sshApplyResult is the secret-free result of one SSH access delivery.
type sshApplyResult struct {
	Operation           string   `json:"operation"`
	State               string   `json:"state"`
	TrustedCAKeys       int      `json:"trustedCaKeys"`
	Users               []string `json:"users,omitempty"`
	HostCertificatePath string   `json:"hostCertificatePath,omitempty"`
	CleanupPending      []string `json:"cleanupPending,omitempty"`
	Changes             []string `json:"changes,omitempty"`
}

func newHostSSHApplyCommand() *cobra.Command {
	var tenantRef string
	var hostNames []string
	var approved bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a sealed SSH CA trust, revocation or host certificate delivery from stdin",
		Long: `Read one sealed ssh-ca-trust, ssh-ca-revoke or ssh-host-certificate envelope on
stdin, signed by the pinned delivering authority, and apply it to this server's
sshd:

  - ssh-ca-trust installs the owner's user CA keys as TrustedUserCAKeys
    (/etc/ssh/stackkit/trusted_user_ca_keys) and one AuthorizedPrincipalsFile
    per local user (/etc/ssh/stackkit/principals/<user>). Root is never a
    target. An empty key list revokes like ssh-ca-revoke.
  - ssh-ca-revoke removes that trust. Revocations take effect at once and are
    never rolled back; one sshd cannot reload for is recorded as cleanup
    pending and finished by 'host security repair'.
  - ssh-host-certificate installs a host certificate beside the host key it
    certifies, only if it names every --host-name (default: the system
    hostname) and is currently valid.

sshd is reloaded only after 'sshd -t' accepts the new configuration; a change
that fails is rolled back. The applied state is recorded in owner custody, so
'host security verify' reports drift and 'host security repair' reinstalls it.`,
		Example: `  sudo stackkit host ssh apply --tenant-ref "$TENANT" --owner-approve < envelope.json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved || tenantRef == "" {
				return errors.New("host ssh apply requires --tenant-ref and --owner-approve")
			}
			raw, err := readSealedEnvelope(cmd)
			if err != nil {
				return err
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "host ssh apply", func() error {
				result, err := applySSHDelivery(cmd, workspace, raw, tenantRef, hostNames, hostsecurity.Engine{Host: hostsecurity.LocalHost{}})
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), result)
			})
		},
	}
	cmd.Flags().StringVar(&tenantRef, "tenant-ref", "", "Tenant the sealed delivery must be addressed to")
	cmd.Flags().StringArrayVar(&hostNames, "host-name", nil, "Name a host certificate must carry as a principal (repeatable; default: the system hostname)")
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize changing this server's SSH access")
	return cmd
}

//nolint:gocyclo // One dispatch over the three SSH operations, each with its custody ordering.
func applySSHDelivery(cmd *cobra.Command, workspace string, raw []byte, tenantRef string, hostNames []string, engine hostsecurity.Engine) (sshApplyResult, error) {
	store := backupcustody.HostStore(workspace)
	now := time.Now().UTC()
	delivery, err := hostcustody.OpenDelivery(store, raw, hostcustody.Expectation{TenantRef: tenantRef, Operations: []string{
		hostdelivery.OperationSSHCATrust, hostdelivery.OperationSSHCARevoke, hostdelivery.OperationSSHHostCertificate,
	}}, now)
	if err != nil {
		return sshApplyResult{}, err
	}
	defer backupcustody.Clear(delivery.Plaintext)
	record, err := hostcustody.LoadSSHAccess(store)
	if err != nil && !errors.Is(err, hostcustody.ErrMissing) {
		return sshApplyResult{}, err
	}
	context := delivery.Context
	result := sshApplyResult{Operation: context.Operation}
	next := record
	next.UpdatedAt = now.Format(time.RFC3339Nano)

	if context.Operation == hostdelivery.OperationSSHHostCertificate {
		var payload hostdelivery.SSHHostCertificate
		if err := decodeDeliveryPayload(delivery.Plaintext, &payload); err != nil {
			return sshApplyResult{}, err
		}
		if record.HostCertificateRef == context.DeliveryRef && context.ItemVersion < record.HostCertificateVersion {
			return sshApplyResult{}, errors.New("the delivered host certificate is older than the one in custody")
		}
		applied, err := engine.ApplySSHHostCertificate(cmd.Context(), payload.Certificate, hostNames)
		if err != nil {
			return sshApplyResult{}, err
		}
		next.HostCertificate, next.HostCertificateRef, next.HostCertificateVersion = payload.Certificate, context.DeliveryRef, context.ItemVersion
		if err := hostcustody.SaveSSHAccess(store, next); err != nil {
			return sshApplyResult{}, err
		}
		result.State, result.HostCertificatePath, result.Changes, result.CleanupPending = "installed", applied.HostCertificatePath, applied.Changes, applied.CleanupPending
		result.TrustedCAKeys = len(next.CAKeys)
		return result, nil
	}

	var keys []string
	var principals map[string][]string
	if context.Operation == hostdelivery.OperationSSHCATrust {
		var payload hostdelivery.SSHCATrust
		if err := decodeDeliveryPayload(delivery.Plaintext, &payload); err != nil {
			return sshApplyResult{}, err
		}
		keys, principals = payload.CAKeys, payload.Principals
	} else if err := decodeDeliveryPayload(delivery.Plaintext, &hostdelivery.SSHCARevoke{}); err != nil {
		return sshApplyResult{}, err
	}
	keys, principals, err = hostsecurity.NormalizeSSHUserCATrust(keys, principals)
	if err != nil {
		return sshApplyResult{}, err
	}
	if len(keys) > 0 && record.TrustRef == context.DeliveryRef && context.ItemVersion < record.TrustVersion {
		return sshApplyResult{}, errors.New("the delivered SSH CA trust is older than the one in custody")
	}
	next.CAKeys, next.Principals, next.CleanupPending = keys, principals, nil
	if len(keys) > 0 {
		next.TrustRef, next.TrustVersion = context.DeliveryRef, context.ItemVersion
	}
	// A change that withdraws any key or principal is recorded first, so a
	// repair can never reinstall what the owner revoked, even if applying it
	// fails part-way.
	revoking := sshTrustWithdraws(record, keys, principals)
	if revoking {
		if err := hostcustody.SaveSSHAccess(store, next); err != nil {
			return sshApplyResult{}, err
		}
	}
	applied, applyErr := engine.ApplySSHUserCATrust(cmd.Context(), keys, principals)
	if len(applied.CleanupPending) > 0 {
		next.CleanupPending = applied.CleanupPending
		if err := hostcustody.SaveSSHAccess(store, next); err != nil {
			return sshApplyResult{}, errors.Join(applyErr, err)
		}
	}
	if applyErr != nil {
		return sshApplyResult{}, applyErr
	}
	if !revoking {
		if err := hostcustody.SaveSSHAccess(store, next); err != nil {
			return sshApplyResult{}, err
		}
	}
	result.State, result.TrustedCAKeys, result.Changes, result.CleanupPending = "trusted", len(keys), applied.Changes, applied.CleanupPending
	if len(keys) == 0 {
		result.State = "revoked"
	}
	for user := range principals {
		result.Users = append(result.Users, user)
	}
	sort.Strings(result.Users)
	return result, nil
}

// sshTrustWithdraws reports whether the new trust drops a key, a user or a
// principal the recorded trust allowed.
func sshTrustWithdraws(record hostcustody.SSHAccess, keys []string, principals map[string][]string) bool {
	for _, key := range record.CAKeys {
		if !slices.Contains(keys, key) {
			return true
		}
	}
	for user, names := range record.Principals {
		for _, name := range names {
			if !slices.Contains(principals[user], name) {
				return true
			}
		}
	}
	return false
}

// sshAccessState loads the SSH access recorded in node custody for host
// security verify and repair.
func sshAccessState(workspace string, hostNames []string) *hostsecurity.SSHAccessState {
	state := &hostsecurity.SSHAccessState{HostNames: hostNames}
	record, err := hostcustody.LoadSSHAccess(backupcustody.HostStore(workspace))
	switch {
	case errors.Is(err, hostcustody.ErrMissing):
	case err != nil:
		state.Failure = err.Error()
	default:
		state.Recorded = true
		state.Desired = hostsecurity.SSHAccess{CAKeys: record.CAKeys, Principals: record.Principals, HostCertificate: record.HostCertificate}
		state.CleanupPending = record.CleanupPending
	}
	return state
}

// settleSSHAccessCleanup clears a recorded pending revocation cleanup once a
// repair completed it.
func settleSSHAccessCleanup(workspace string, report hostsecurity.RepairReport) error {
	applied := slices.ContainsFunc(report.Steps, func(step hostsecurity.RepairStep) bool {
		return step.Control == hostsecurity.ControlSSHUserCA && step.Status == hostsecurity.StepApplied
	})
	if !applied {
		return nil
	}
	return withLifecycleMutation(workspace, "host security repair", func() error {
		store := backupcustody.HostStore(workspace)
		record, err := hostcustody.LoadSSHAccess(store)
		if err != nil || len(record.CleanupPending) == 0 {
			if errors.Is(err, hostcustody.ErrMissing) {
				return nil
			}
			return err
		}
		record.CleanupPending = nil
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return hostcustody.SaveSSHAccess(store, record)
	})
}
