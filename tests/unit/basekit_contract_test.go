package unit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseKitStackfile_ExposesV5CompatibilitySurface(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "base-kit", "stackfile.cue"))
	require.NoError(t, err)

	content := string(data)

	assert.Contains(t, content, "mode:", "stackfile should expose v5 mode field")
	assert.Contains(t, content, "runtime?:", "stackfile should expose runtime field")
	assert.Contains(t, content, "context?:", "stackfile should expose context field")
	assert.Contains(t, content, "paas?:", "stackfile should expose PAAS field")
	assert.Contains(t, content, "addons?:", "stackfile should expose add-ons field")
	assert.Contains(t, content, "compute:", "stackfile should expose compute object")
	assert.Contains(t, content, "variant:", "stackfile should still expose legacy variant compatibility")
	assert.Contains(t, content, "deploymentMode:", "stackfile should still expose legacy deploymentMode compatibility")
}
