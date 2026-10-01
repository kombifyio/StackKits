package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/imagepreparation"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var initPreinstalledManifest string

func newImageCommand() *cobra.Command {
	command := &cobra.Command{Use: "image", Short: "Prepare and admit an owner-neutral golden image cache", Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, Long: `Prepare immutable core container caches from the embedded CUE catalog.
This never initializes an Owner, starts services, creates volumes or prepares a
provider VM. Ubuntu 26.04 is the default target; Ubuntu 24.04 remains compatible.
The manifest is unsigned cache metadata, never host conformance or Apply authority.
The existing packaged OpenTofu provider closure is verified and reused. Network
access remains necessary for first-boot service integrations and optional
workloads. Techstack owns machine sysprep, VM images and enrollment.`}
	var profile, root, target string
	plan := &cobra.Command{Use: "plan", Short: "Print the authoritative neutral core cache profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		value, err := imagepreparation.Plan(profile)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
	}}
	plan.Flags().StringVar(&profile, "profile", "cloud-core-compose", "CUE neutral profile (cloud-core-compose, basement-core-compose, basement-core-lite-compose)")
	command.AddCommand(plan)
	for _, operation := range []string{"prepare", "verify"} {
		operation := operation
		child := &cobra.Command{Use: operation, Short: map[string]string{"prepare": "Cache the exact running attested release and pull core images only", "verify": "Re-observe release bytes, empty target, host and exact core image cache"}[operation], Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			options, cleanup, err := neutralImageOptions(root, target)
			if err != nil {
				return err
			}
			defer cleanup()
			var manifest imagepreparation.Manifest
			if operation == "prepare" {
				manifest, err = imagepreparation.Prepare(cmd.Context(), options, profile)
			} else {
				manifest, err = imagepreparation.Verify(cmd.Context(), options, filepath.Join(root, imagepreparation.ManifestName))
			}
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(manifest)
		}}
		child.Flags().StringVar(&root, "cache-root", "", "Separate neutral release/cache metadata directory")
		child.Flags().StringVar(&target, "target", "", "Empty future deployment directory; never an initialized stack")
		_ = child.MarkFlagRequired("cache-root")
		_ = child.MarkFlagRequired("target")
		if operation == "prepare" {
			child.Flags().StringVar(&profile, "profile", "cloud-core-compose", "CUE neutral core cache profile")
		}
		command.AddCommand(child)
	}
	return command
}

func neutralImageOptions(root, target string) (imagepreparation.Options, func(), error) {
	cleanup := func() {}
	for _, key := range []string{"STACKKIT_TOFU_BINARY", "STACKKIT_TERRAMATE_BINARY", "STACKKIT_TOFU_PROVIDERS_DIR"} {
		if os.Getenv(key) != "" {
			return imagepreparation.Options{}, cleanup, fmt.Errorf("neutral admission forbids packaged tool override %s", key)
		}
	}
	tag, err := releaseindex.ExactTagForBuildVersion(version)
	if err != nil {
		return imagepreparation.Options{}, cleanup, err
	}
	executable, err := os.Executable()
	if err != nil {
		return imagepreparation.Options{}, cleanup, err
	}
	config, err := os.MkdirTemp("", "stackkit-neutral-docker-")
	if err != nil {
		return imagepreparation.Options{}, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(config) }
	return imagepreparation.Options{CacheRoot: root, Target: target, Executable: executable, Version: tag, Installer: releaseindex.Installer{Source: newPublicReleaseSource(), Attestations: newPublicAttestationVerifier()}, Docker: imagepreparation.LocalDocker{Config: config}, ObserveHost: imagepreparation.ObserveHost}, cleanup, nil
}

// admitPreinstalledInit runs before any spec, Owner, runtime custody or secret
// is written. A nonempty target must resume the same canonical intent under CAS.
func admitPreinstalledInit(cmd *cobra.Command, wd, kit string, canonical []byte, service *architecturev2.Service, request *stackspecintent.Request) error {
	if strings.TrimSpace(initPreinstalledManifest) == "" {
		return nil
	}
	options, cleanup, err := neutralImageOptions(filepath.Dir(initPreinstalledManifest), wd)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := imagepreparation.CleanTarget(wd); err != nil {
		if !errors.Is(err, imagepreparation.ErrNotNeutral) {
			return err
		}
		request.RequireAlreadyApplied = true
	}
	manifest, err := imagepreparation.VerifyCache(cmd.Context(), options, initPreinstalledManifest)
	if err != nil {
		return fmt.Errorf("preinstalled image admission: %w", err)
	}
	if manifest.Profile.Kit != kit {
		return fmt.Errorf("preinstalled profile %s cannot initialize %s", manifest.Profile.ID, kit)
	}
	var spec struct {
		Generation struct {
			Target string `yaml:"target"`
		} `yaml:"generation"`
	}
	if err := yaml.Unmarshal(canonical, &spec); err != nil {
		return err
	}
	if err := service.RequireSelectedWorkloadModule(canonical, manifest.Profile.Module); err != nil {
		return fmt.Errorf("preinstalled profile %s: %w", manifest.Profile.ID, err)
	}
	if techStackHandoffFromEnv().requested() && spec.Generation.Target != manifest.Profile.ManagedTarget {
		return fmt.Errorf("Techstack managed first boot requires generation.target %s", manifest.Profile.ManagedTarget)
	}
	// Re-observe after release/cache verification as well. A target populated
	// while admission ran must never become a fresh-create or replacement path.
	if err := imagepreparation.CleanTarget(wd); err != nil {
		if !errors.Is(err, imagepreparation.ErrNotNeutral) {
			return err
		}
		request.RequireAlreadyApplied = true
	}
	return nil
}
