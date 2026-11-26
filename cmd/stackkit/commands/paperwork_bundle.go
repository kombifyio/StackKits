package commands

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
	"github.com/spf13/cobra"
)

func newVerifyPaperworkBundleCommand() *cobra.Command {
	var pin hostdelivery.PaperworkBundlePin
	cmd := &cobra.Command{
		Use:    "verify-paperwork-bundle",
		Short:  "Read back an exact Paperwork bundle with this node's registry grant",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			grant, err := localevidence.ResolveEncryptedLocalIssuedSecret(getWorkDir(), hostdelivery.PaperworkRegistrySecretRef)
			if err != nil {
				return errors.New("node custody has no Paperwork registry grant")
			}
			defer backupcustody.Clear(grant)
			bundle, err := hostdelivery.FetchPaperworkBundle(cmd.Context(), pin, grant)
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{
				"source_sha": pin.SourceSHA, "image": pin.Image, "bundle_sha256": pin.BundleSHA256, "version": bundle.Version,
			})
		},
	}
	addPaperworkBundlePinFlags(cmd, &pin)
	return cmd
}

func addPaperworkBundlePinFlags(cmd *cobra.Command, pin *hostdelivery.PaperworkBundlePin) {
	cmd.Flags().StringVar(&pin.SourceSHA, "source-sha", "", "Source from the admitted Paperwork Delivery mirror receipt")
	cmd.Flags().StringVar(&pin.Image, "image", "", "Exact customer image reference from the admitted mirror receipt")
	cmd.Flags().StringVar(&pin.BundleSHA256, "bundle-sha256", "", "Bundle SHA-256 from the admitted mirror receipt")
	cmd.Flags().Int64Var(&pin.BundleSize, "bundle-size", 0, "Bundle size from the admitted mirror receipt")
}
