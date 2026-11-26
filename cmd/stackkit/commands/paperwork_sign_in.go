package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
	"github.com/spf13/cobra"
)

const paperworkSignInRef = "secret://stackkit/paperwork/linked-sign-in"

var (
	paperworkOrigin       = regexp.MustCompile(`^https://[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?-paperwork-[0-9a-f]{4}\.kombify\.me$`)
	paperworkClientID     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	paperworkClientSecret = regexp.MustCompile(`^[\x21-\x7e]{16,1000}$`)
	paperworkHostID       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

type paperworkSignInCredential struct {
	Origin          string `json:"origin"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret,omitempty"`
	GuardGeneration uint64 `json:"guard_generation"`
}

type paperworkSignInRecord struct {
	Tenant       string                    `json:"tenant"`
	Server       string                    `json:"server"`
	Revision     uint64                    `json:"revision"`
	DeliveryHash [sha256.Size]byte         `json:"delivery_hash"`
	Revoked      bool                      `json:"revoked"`
	Credential   paperworkSignInCredential `json:"credential"`
}

func parsePaperworkSignIn(raw []byte, importing bool) (paperworkSignInCredential, error) {
	var credential paperworkSignInCredential
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF ||
		!validPaperworkSignIn(credential, importing) {
		return paperworkSignInCredential{}, errors.New("linked Paperwork credential is invalid")
	}
	return credential, nil
}

func validPaperworkSignIn(credential paperworkSignInCredential, importing bool) bool {
	return paperworkOrigin.MatchString(credential.Origin) && paperworkClientID.MatchString(credential.ClientID) &&
		credential.GuardGeneration > 0 && ((importing && paperworkClientSecret.MatchString(credential.ClientSecret)) ||
		(!importing && credential.ClientSecret == ""))
}

func readPaperworkSignInRecord(workspace string) (paperworkSignInRecord, error) {
	material, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, paperworkSignInRef)
	if err != nil {
		return paperworkSignInRecord{}, err
	}
	defer clear(material)
	var record paperworkSignInRecord
	if json.Unmarshal(material, &record) != nil || !paperworkHostID.MatchString(record.Tenant) ||
		!paperworkHostID.MatchString(record.Server) || record.Revision == 0 || !validPaperworkSignIn(record.Credential, !record.Revoked) {
		return paperworkSignInRecord{}, errors.New("linked Paperwork custody record is invalid")
	}
	return record, nil
}

func newApplyPaperworkSignInCommand() *cobra.Command {
	var tenant, server string
	var generation uint64
	var approved bool
	cmd := &cobra.Command{
		Use:    "apply-paperwork-sign-in",
		Short:  "Apply a linked Paperwork sign-in delivery from stdin to local custody",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved || !paperworkHostID.MatchString(tenant) || !paperworkHostID.MatchString(server) || generation == 0 {
				return errors.New("linked Paperwork custody requires an approved tenant, server and Guard generation")
			}
			raw, err := readSealedEnvelope(cmd)
			if err != nil {
				return err
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "host delivery apply-paperwork-sign-in", func() error {
				state, err := applyPaperworkSignIn(workspace, tenant, server, generation, raw)
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{"state": state})
			})
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant-ref", "", "Enrolled tenant from the Guard")
	cmd.Flags().StringVar(&server, "server-id", "", "Enrolled server from the Guard")
	cmd.Flags().Uint64Var(&generation, "guard-generation", 0, "Current enrolled Guard generation")
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize the linked sign-in custody change")
	return cmd
}

func applyPaperworkSignIn(workspace, tenant, server string, generation uint64, raw []byte) (string, error) {
	current, err := readPaperworkSignInRecord(workspace)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	hash := sha256.Sum256(raw)
	if err == nil && current.DeliveryHash == hash && current.Tenant == tenant && current.Server == server &&
		current.Credential.GuardGeneration == generation {
		if current.Revoked {
			return "removed", nil
		}
		return "stored", nil
	}
	store := backupcustody.HostStore(workspace)
	delivery, err := hostcustody.OpenPaperworkSignInDelivery(store, raw, tenant, time.Now().UTC())
	if err != nil {
		return "", err
	}
	defer clear(delivery.Plaintext)
	importing := delivery.Context.Operation == hostdelivery.OperationPaperworkSignIn
	credential, err := parsePaperworkSignIn(delivery.Plaintext, importing)
	if err != nil || credential.GuardGeneration != generation || delivery.Context.DeliveryRef != "paperwork-sign-in:"+server {
		return "", errors.New("linked Paperwork delivery does not match this Guard binding")
	}
	if importing && !linkedPaperworkInstallEnabled() {
		return "", errors.New("linked Paperwork sign-in is disabled on this node")
	}
	next := paperworkSignInRecord{Tenant: tenant, Server: server, Revision: delivery.Context.ItemVersion, DeliveryHash: hash, Revoked: !importing, Credential: credential}
	if err := checkPaperworkSignInTransition(current, next); err != nil {
		return "", err
	}
	record, err := json.Marshal(next)
	if err != nil {
		return "", errors.New("linked Paperwork custody record could not be encoded")
	}
	defer clear(record)
	if err := localevidence.StoreEncryptedLocalIssuedSecret(workspace, paperworkSignInRef, record); err != nil {
		return "", err
	}
	confirmed, err := readPaperworkSignInRecord(workspace)
	if err != nil || confirmed != next {
		return "", errors.New("linked Paperwork custody write did not read back")
	}
	if importing {
		return "stored", nil
	}
	return "removed", nil
}

func checkPaperworkSignInTransition(current, next paperworkSignInRecord) error {
	if next.Revision == 0 {
		return errors.New("linked Paperwork delivery requires a positive revision")
	}
	if current.Revision == 0 {
		return nil
	}
	if current.Tenant != next.Tenant || current.Server != next.Server {
		return errors.New("linked Paperwork custody belongs to another Guard binding")
	}
	before, after := current.Credential, next.Credential
	if after.GuardGeneration < before.GuardGeneration || (after.GuardGeneration == before.GuardGeneration && next.Revision < current.Revision) {
		return errors.New("linked Paperwork delivery is older than node custody")
	}
	if after.GuardGeneration == before.GuardGeneration && next.Revision == current.Revision {
		if after.ClientID != before.ClientID || after.Origin != before.Origin ||
			(!next.Revoked && (current.Revoked || after.ClientSecret != before.ClientSecret)) {
			return errors.New("linked Paperwork delivery conflicts with the current credential or its revocation")
		}
	}
	return nil
}
