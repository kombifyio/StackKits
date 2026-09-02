package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/spf13/cobra"
)

var backupEmergencyRestoreCmd = &cobra.Command{
	Use:         "emergency-restore",
	Short:       "Decrypt and verify a portable export into a new staging directory",
	Args:        machineAwareNoArgs,
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE:        runBackupEmergencyRestore,
}

func configureEmergencyBackupCommands() {
	backupEmergencyExportCmd.Annotations = map[string]string{noDeployObservabilityAnnotation: "true"}
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportTarget, "target", "", "New output directory; its parent must exist")
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportFormat, "format", "tar.gz.age", "Portable archive format (tar.gz.age)")
	backupEmergencyExportCmd.Flags().StringVar(&backupEmergencyExportLargeMediaMode, "large-media-mode", "manifest-only", "Media sources: manifest-only, include, or exclude")
	backupEmergencyExportCmd.Flags().StringArrayVar(&backupEmergencyExportSourcePaths, "source", nil, "Explicit CLASS=PATH source; repeat for config, secrets, platform-state, database, user-content, documents, photos, large-media, telemetry-timeseries, serverless-config or cache-generated")
	backupEmergencyExportCmd.Flags().StringArrayVar(&backupEmergencyExportRecipients, "recipient", nil, "age X25519 recipient public key; repeat for multiple recovery owners")
	backupEmergencyExportCmd.Flags().Bool("json", false, "Output the archive receipt as JSON")
	backupEmergencyRestoreCmd.Flags().String("archive", "", "Encrypted emergency archive")
	backupEmergencyRestoreCmd.Flags().String("identity-file", "", "Local age identity file; secret keys never belong on the command line")
	backupEmergencyRestoreCmd.Flags().String("target", "", "New isolated restore directory; its parent must exist")
	backupEmergencyRestoreCmd.Flags().Int64("max-bytes", 512<<30, "Maximum decompressed archive bytes, including metadata")
	backupEmergencyRestoreCmd.Flags().Bool("json", false, "Output the verified staging result as JSON")
}

func runBackupEmergencyExport(cmd *cobra.Command, args []string) (returnErr error) {
	defer func() {
		returnErr = machineAwareCommandError(cmd, returnErr, "Use explicit readable CLASS=PATH sources, an age public recipient and a new target outside the sources.")
	}()
	if backupEmergencyExportFormat != "tar.gz.age" {
		return errors.New("emergency export supports tar.gz.age")
	}
	sources := make([]backuplifecycle.EmergencySource, 0, len(backupEmergencyExportSourcePaths))
	for _, value := range backupEmergencyExportSourcePaths {
		class, source, ok := strings.Cut(value, "=")
		if !ok {
			return errors.New("each --source must use CLASS=PATH")
		}
		sources = append(sources, backuplifecycle.EmergencySource{Class: strings.TrimSpace(class), Path: strings.TrimSpace(source)})
	}
	recipients := make([]age.Recipient, 0, len(backupEmergencyExportRecipients))
	for _, value := range backupEmergencyExportRecipients {
		recipient, err := age.ParseX25519Recipient(strings.TrimSpace(value))
		if err != nil {
			return errors.New("invalid age X25519 recipient public key")
		}
		recipients = append(recipients, recipient)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	result, err := backuplifecycle.ExportEmergency(ctx, backuplifecycle.EmergencyExportInput{
		Target: backupEmergencyExportTarget, Sources: sources, Recipients: recipients, LargeMediaMode: backupEmergencyExportLargeMediaMode,
	})
	if err != nil {
		return err
	}
	if machine, _ := cmd.Flags().GetBool("json"); machine {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Encrypted emergency export: %s\nSHA-256: %s\nConsistency: %s; application recovery remains unverified.\n", result.Archive, result.ArchiveSHA256, result.Manifest.Consistency)
	return err
}

func runBackupEmergencyRestore(cmd *cobra.Command, args []string) (returnErr error) {
	defer func() {
		returnErr = machineAwareCommandError(cmd, returnErr, "Check the archive and its separately retained age identity; choose a new isolated target directory.")
	}()
	archive, _ := cmd.Flags().GetString("archive")
	identityFile, _ := cmd.Flags().GetString("identity-file")
	target, _ := cmd.Flags().GetString("target")
	maxBytes, _ := cmd.Flags().GetInt64("max-bytes")
	if archive == "" || identityFile == "" || target == "" {
		return errors.New("--archive, --identity-file and --target are required")
	}
	identities, err := backuplifecycle.ReadEmergencyIdentities(identityFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	result, err := backuplifecycle.RestoreEmergency(ctx, backuplifecycle.EmergencyRestoreInput{
		Archive: archive, Identities: identities, Target: target, MaxBytes: maxBytes,
	})
	if err != nil {
		return err
	}
	if machine, _ := cmd.Flags().GetBool("json"); machine {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Emergency data authenticated and verified: %s\nApplications are not activated or verified. Follow RESTORE.md for application recovery.\n", result.Target)
	return err
}
