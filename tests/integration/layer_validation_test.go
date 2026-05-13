package integration

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kombifyio/stackkits/internal/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getProjectRootForLayerValidation returns the project root directory
func getProjectRootForLayerValidation(t *testing.T) string {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "Failed to get caller info")
	// Go up 3 levels: tests/integration -> tests -> root
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// TestLayerValidation_ExistingStackKits validates existing StackKits
func TestLayerValidation_ExistingStackKits(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectRoot := getProjectRootForLayerValidation(t)

	testCases := []struct {
		name      string
		stackkit  string
		wantValid bool
	}{
		{
			// base-kit uses #BaseKitStack (simplified schema) which
			// does not include the generic layer fields (system, packages, etc.)
			// that the full #BaseStackKit schema expects. Layer validation will
			// report missing fields. This is expected — base-kit delegates
			// L1 provisioning to the CLI at apply time, not in the CUE schema.
			name:      "base-kit",
			stackkit:  "base-kit",
			wantValid: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stackkitDir := filepath.Join(projectRoot, tc.stackkit)

			validator := validation.NewLayerValidator(projectRoot)
			result, err := validator.ValidateStackKit(stackkitDir)

			require.NoError(t, err)

			if tc.wantValid {
				assert.True(t, result.Valid,
					"StackKit %s should pass layer validation. Errors: %v",
					tc.stackkit, result.AllErrors)
			} else {
				assert.False(t, result.Valid,
					"StackKit %s should fail layer validation", tc.stackkit)
			}
		})
	}
}

// TestLayerValidation_BaseKitLayers validates base-kit layer by layer.
// base-kit uses a simplified schema (#BaseKitStack) that does not include
// the generic layer fields (system, packages, platform, etc.). These are handled
// by the CLI at apply time. This test verifies the validator runs without error
// and correctly identifies what's missing from the simplified schema.
func TestLayerValidation_BaseKitLayers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectRoot := getProjectRootForLayerValidation(t)
	stackkitDir := filepath.Join(projectRoot, "base-kit")

	validator := validation.NewLayerValidator(projectRoot)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)

	// base-kit's simplified schema won't pass the full layer validation.
	// Verify the validator runs and produces meaningful errors.
	t.Run("Layer1_Foundation", func(t *testing.T) {
		assert.NotEmpty(t, result.Layer1.Errors, "Layer 1 should report missing fields for simplified schema")
	})

	t.Run("Layer2_Platform", func(t *testing.T) {
		assert.NotEmpty(t, result.Layer2.Errors, "Layer 2 should report missing fields for simplified schema")
	})

	t.Run("StackKitName", func(t *testing.T) {
		assert.Equal(t, "base-kit", result.StackKit)
	})
}

// TestLayerValidation_ErrorMessages validates that error messages are clear and actionable
func TestLayerValidation_ErrorMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectRoot := getProjectRootForLayerValidation(t)
	stackkitDir := filepath.Join(projectRoot, "base-kit")

	validator := validation.NewLayerValidator(projectRoot)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)

	// If there are any errors, verify they have clear messages
	for _, layerErr := range result.AllErrors {
		t.Run("Error_"+layerErr.Code, func(t *testing.T) {
			assert.NotEmpty(t, layerErr.Message, "Error message should not be empty")
			assert.NotEmpty(t, layerErr.Code, "Error code should not be empty")
			assert.True(t, layerErr.Layer >= 1 && layerErr.Layer <= 3, "Layer should be 1, 2, or 3")
		})
	}
}

// TestLayerValidation_StackKitNames validates that all StackKits have proper names
func TestLayerValidation_StackKitNames(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectRoot := getProjectRootForLayerValidation(t)

	stackkits := []string{"base-kit", "ha-kit", "modern-homelab"}

	for _, stackkit := range stackkits {
		t.Run(stackkit, func(t *testing.T) {
			stackkitDir := filepath.Join(projectRoot, stackkit)
			validator := validation.NewLayerValidator(projectRoot)
			result, err := validator.ValidateStackKit(stackkitDir)

			require.NoError(t, err)
			assert.Equal(t, stackkit, result.StackKit, "StackKit name should match directory name")
		})
	}
}
