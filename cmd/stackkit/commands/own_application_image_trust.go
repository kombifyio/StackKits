package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/ownapplication"
	"github.com/spf13/cobra"
)

// Trust installation is an explicit local Owner action, separate from update
// evidence. An update can supply a bundle, never a root or signer allowlist.
func newOwnApplicationImageTrustCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "image-trust", Short: "Install or verify the Owner-custodied image signer policy",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long:        "Install or verify a node-wide Sigstore image signer policy under existing Owner custody. Once installed, stateless one-container image changes require --image-identity-file. Updates to applications with a current or previous image admission also require evidence, even if trust is missing; explicitly supplied evidence always requires installed trust. Applications without retained admissions can still perform non-image updates and stateful or project image updates without image evidence. Guard C currently admits only stateless one-container image changes. The trustedRoot JSON field contains base64-encoded Sigstore trusted-root bytes.",
	}
	var document, expected string
	var approved bool
	install := &cobra.Command{Use: "install", Short: "Install a Sigstore root and exact signer allowlist under local Owner custody", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !approved {
			return errors.New("image identity trust installation requires explicit --owner-approve")
		}
		if !advancedSHA256Pattern.MatchString(expected) {
			return errors.New("image identity trust installation requires --expect-sha256 sha256:<64 lowercase hex>")
		}
		workspace := getWorkDir()
		raw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, document), 4<<20, "image identity trust")
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if "sha256:"+hex.EncodeToString(sum[:]) != expected {
			return errors.New("image identity trust document differs from its independent SHA-256 pin")
		}
		trust, err := ownapplication.ParseImageIdentityTrust(raw)
		if err != nil {
			return err
		}
		return withLifecycleMutation(workspace, "own application image trust install", func() error {
			if _, err := localevidence.LoadHomeAuthorityPublic(workspace); err != nil {
				return err
			}
			if err := ownapplication.SaveImageIdentityTrust(workspace, trust); err != nil {
				return err
			}
			return writeOwnApplicationImageTrust(cmd)
		})
	}}
	install.Flags().StringVar(&document, "document", "", "JSON policy with schemaVersion, trustedRoot, trustedRootSha256 and signers")
	install.Flags().StringVar(&expected, "expect-sha256", "", "Independent sha256:<hex> pin of the exact policy document")
	install.Flags().BoolVar(&approved, "owner-approve", false, "Approve installing this local node image trust policy")
	_ = install.MarkFlagRequired("document")
	_ = install.MarkFlagRequired("expect-sha256")
	verify := &cobra.Command{Use: "verify", Short: "Verify the installed image trust policy and its Owner signature", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return writeOwnApplicationImageTrust(cmd)
	}}
	command.AddCommand(install, verify)
	return command
}

func writeOwnApplicationImageTrust(cmd *cobra.Command) error {
	trust, err := ownapplication.LoadImageIdentityTrust(getWorkDir())
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
		SchemaVersion string                       `json:"schemaVersion"`
		TrustDigest   string                       `json:"trustDigest"`
		Signers       []ownapplication.ImageSigner `json:"signers"`
	}{trust.SchemaVersion, trust.Digest(), trust.Signers})
}
