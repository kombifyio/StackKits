package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/spf13/cobra"
)

const commandResultSchemaVersion = "stackkit.command-result/v1"

var (
	newPublicReleaseSource = func() releaseindex.Source {
		if endpoint := strings.TrimSpace(os.Getenv("STACKKIT_RELEASE_FIXTURE_URL")); endpoint != "" {
			source, err := releaseindex.NewGitHubFixtureSource(nil, endpoint)
			if err != nil {
				return rejectedReleaseSource{err: err}
			}
			return source
		}
		return releaseindex.NewGitHubSource(nil)
	}
	newPublicAttestationVerifier = func() releaseindex.AttestationVerifier {
		return releaseindex.SigstoreVerifier{}
	}
	currentReleasePlatform = func() releaseindex.Platform {
		return releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
)

type rejectedReleaseSource struct {
	err error
}

func (source rejectedReleaseSource) ListReleases(context.Context) ([]releaseindex.Release, error) {
	return nil, source.err
}

func (source rejectedReleaseSource) Fetch(context.Context, string, int64) ([]byte, error) {
	return nil, source.err
}

type commandResult struct {
	SchemaVersion string `json:"schemaVersion"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	Data          any    `json:"data"`
}

func runOfflineReleaseVerification(cmd *cobra.Command, workspace string, asJSON bool) error {
	receipts, err := verifyWorkspaceReleaseReceipts(cmd, workspace)
	if err != nil {
		return err
	}
	if asJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), receipts)
	}
	for _, receipt := range receipts {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Verified %s %s (%s, %s/%s)\n",
			receipt.Kit, receipt.Version, receipt.Channel, receipt.Platform.OS, receipt.Platform.Arch); err != nil {
			return err
		}
	}
	return nil
}

func verifyWorkspaceReleaseReceipts(cmd *cobra.Command, workspace string) ([]releaseindex.Receipt, error) {
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return nil, fmt.Errorf("load StackKit identity before offline verification: %w", err)
	}
	receipts, err := (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).VerifyWorkspace(cmd.Context(), workspace, kit, releaseindex.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH})
	if err != nil {
		return nil, fmt.Errorf("offline release verification failed: %w", err)
	}
	return receipts, nil
}

func loadWorkspaceKit(workspace string) (string, error) {
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(specFile)
	if err != nil {
		return "", err
	}
	if loaded.Document.V2 != nil {
		return string(loaded.Document.V2.KitProfile), nil
	}
	if loaded.Document.Legacy != nil && strings.TrimSpace(loaded.Document.Legacy.StackKit) != "" {
		return strings.TrimSpace(loaded.Document.Legacy.StackKit), nil
	}
	return "", fmt.Errorf("%s has no StackKit identity", loaded.DisplayPath)
}

func writeCommandResult(cmd *cobra.Command, commandName string, data any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(commandResult{
		SchemaVersion: commandResultSchemaVersion,
		Command:       commandName,
		Status:        "success",
		Data:          data,
	})
}

func shortDigest(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func validateExpectedCurrentReleaseReceipt(
	receipt releaseindex.Receipt,
	kit string,
	exactTag string,
	platform releaseindex.Platform,
) error {
	if receipt.Kit != kit || receipt.Version != exactTag || receipt.Platform != platform {
		return fmt.Errorf(
			"verified receipt %s/%s/%s/%s does not match expected current release %s/%s/%s/%s",
			receipt.Kit,
			receipt.Version,
			receipt.Platform.OS,
			receipt.Platform.Arch,
			kit,
			exactTag,
			platform.OS,
			platform.Arch,
		)
	}
	return nil
}
