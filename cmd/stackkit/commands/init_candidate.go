package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/spf13/cobra"
)

const maxInitCandidateBytes = 2 << 20

// The candidate is desired intent, never a generated plan or runtime evidence.
// Only StackKits validates and persists it; the normal init custody path follows.
func readArchitectureV2InitCandidate(cmd *cobra.Command, service *architecturev2.Service, profile stackspecmigration.KitProfile, wd string) (architecturev2.StackSpecValidation, error) {
	var reader io.Reader = os.Stdin
	if cmd != nil {
		reader = cmd.InOrStdin()
	}
	path := strings.TrimSpace(initCandidateSpec)
	if path != "-" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(wd, path)
		}
		file, err := os.Open(filepath.Clean(path))
		if err != nil {
			return architecturev2.StackSpecValidation{}, fmt.Errorf("open init candidate: %w", err)
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return architecturev2.StackSpecValidation{}, fmt.Errorf("init candidate must be a regular file")
		}
		reader = file
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maxInitCandidateBytes+1))
	if err != nil {
		return architecturev2.StackSpecValidation{}, fmt.Errorf("read init candidate: %w", err)
	}
	if len(raw) == 0 || len(raw) > maxInitCandidateBytes {
		return architecturev2.StackSpecValidation{}, fmt.Errorf("init candidate must contain a StackSpec within %d bytes", maxInitCandidateBytes)
	}
	validation, err := service.ValidateStackSpec(raw)
	if err != nil {
		return architecturev2.StackSpecValidation{}, fmt.Errorf("validate init candidate: %w", err)
	}
	if validation.KitProfile != profile {
		return architecturev2.StackSpecValidation{}, fmt.Errorf("init candidate does not match selected StackKit %q", profile)
	}
	return validation, nil
}

func validateInitCandidateFlags(cmd *cobra.Command) error {
	if strings.TrimSpace(initCandidateSpec) == "" {
		return nil
	}
	for _, flag := range []string{"api-version", "catalog-defaults", "name", "domain", "platform", "use-case", "use-case-alternative", "use-case-capability", "compute-tier", "module-compute-profile", "module-storage-profile", "module-accelerator-profile", "hardware-profile", "enable"} {
		if commandFlagChanged(cmd, flag) {
			return fmt.Errorf("--candidate-spec cannot be combined with authoring override --%s", flag)
		}
	}
	if initCatalogDefaults || initName != "" || initDomain != "" || initPlatform != "" || initComputeTier != "" || initHardwareProfile != "" ||
		len(initUseCases)+len(initUseCaseAlternatives)+len(initUseCaseCapabilities)+len(initModuleComputeProfiles)+len(initModuleStorageProfiles)+len(initModuleAcceleratorProfiles)+len(initEnableCapabilities) > 0 {
		return fmt.Errorf("--candidate-spec cannot be combined with authoring overrides")
	}
	return nil
}
