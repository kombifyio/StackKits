package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/imagepreparation"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"github.com/spf13/cobra"
)

func newBundleCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "bundle", Short: "Prepare and import a verified Full Local artifact bundle",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long:        "Prepare owner-neutral release tools, provider packages, container images and a pinned local model. Install imports only the verified cache. Continue through the existing native init, generate and apply commands to create this host's own Owner and runtime custody.",
	}
	for _, operation := range []string{"create", "verify", "install"} {
		operation := operation
		var root, target, profile, modelCache string
		var offline bool
		var source neutralSourceFlags
		child := &cobra.Command{
			Use: operation, Args: cobra.NoArgs,
			Short: map[string]string{"create": "Prepare the complete neutral closure while online", "verify": "Authenticate every bundle input without importing it", "install": "Import the complete verified closure without network access"}[operation],
			RunE: func(cmd *cobra.Command, args []string) error {
				if operation != "create" && !offline {
					return errors.New("bundle verify and install require --offline; registry fallback is forbidden")
				}
				absolute, err := filepath.Abs(root)
				if err != nil {
					return err
				}
				if target == "" {
					target, err = os.Getwd()
					if err != nil {
						return err
					}
				}
				options, cleanup, err := neutralImageOptions(filepath.Join(absolute, "cache"), target)
				if err != nil {
					return err
				}
				defer cleanup()
				if err := source.apply(&options); err != nil {
					return err
				}
				if operation != "create" && source.directory != "" {
					return errors.New("bundle readback uses retained bytes; --source-candidate-dir is preparation only")
				}
				var manifest imagepreparation.BundleManifest
				switch operation {
				case "create":
					manifest, err = imagepreparation.PrepareOfflineBundle(cmd.Context(), options, absolute, profile, modelCache)
				case "verify":
					manifest, err = imagepreparation.VerifyOfflineBundle(cmd.Context(), options, absolute)
				case "install":
					manifest, err = imagepreparation.ImportOfflineBundle(cmd.Context(), options, absolute)
				}
				if err != nil {
					return err
				}
				digest, err := imagepreparation.FileDigest(filepath.Join(absolute, imagepreparation.BundleManifestName))
				if err != nil {
					return err
				}
				result := map[string]any{
					"schemaVersion": "stackkit.offline-bundle-result/v1", "operation": operation,
					"profile": manifest.Neutral.Profile.ID, "bundle": absolute,
					"manifestSha256":       digest,
					"preinstalledManifest": filepath.Join(absolute, "cache", imagepreparation.ManifestName),
					"runtimeInstalled":     false,
				}
				if manifest.Neutral.Candidate != nil {
					result["sourceKind"], result["candidate"] = manifest.Neutral.SourceKind, manifest.Neutral.Candidate
				} else {
					result["sourceKind"], result["release"] = imagepreparation.PublicReleaseSource, manifest.Neutral.Release
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			},
		}
		if operation == "create" {
			child.Flags().StringVar(&root, "to", "", "New owner-neutral bundle directory")
			_ = child.MarkFlagRequired("to")
			child.Flags().StringVar(&profile, "profile", "basement-full-local-compose", "Catalog Full Local profile")
			child.Flags().StringVar(&modelCache, "model-cache", "", "Optional existing Ollama models directory; only manifest-pinned blobs are copied")
		} else {
			child.Flags().StringVar(&root, "from", "", "Complete verified bundle directory")
			_ = child.MarkFlagRequired("from")
			child.Flags().BoolVar(&offline, "offline", false, "Require the complete local closure; never resolve a registry")
		}
		child.Flags().StringVar(&target, "target", "", "Future native workspace, separate from the bundle (default current directory)")
		source.bind(child)
		command.AddCommand(child)
	}
	return command
}

// admittedOfflineBundleForInit runs before candidate authoring or custody.
// Only the complete authenticated bundle can supply a persisted source ref.
func admittedOfflineBundleForInit(ctx context.Context, profile runtimeprofile.DataProfile, workspace string) (*runtimeprofile.OfflineBundleRef, error) {
	if !profile.FullLocal() {
		return nil, nil
	}
	if initPreinstalledManifest == "" {
		return nil, errors.New("Full Local init requires --preinstalled-manifest from an installed verified bundle")
	}
	manifest, err := filepath.Abs(initPreinstalledManifest)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(filepath.Dir(manifest))
	if filepath.Base(manifest) != imagepreparation.ManifestName || filepath.Base(filepath.Dir(manifest)) != "cache" {
		return nil, errors.New("Full Local admission requires the bundle's cache/neutral-image.json")
	}
	options, cleanup, err := neutralImageOptions(filepath.Join(root, "cache"), workspace)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	ref, err := imagepreparation.OfflineBundleReference(root)
	if err != nil {
		return nil, err
	}
	if _, err := imagepreparation.VerifyOfflineBundleRef(ctx, options, ref); err != nil {
		return nil, fmt.Errorf("Full Local bundle admission: %w", err)
	}
	return &ref, nil
}
