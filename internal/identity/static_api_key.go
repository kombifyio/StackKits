package identity

import (
	"fmt"
	"os"
	"path/filepath"
)

// StaticAPIKeyFilename is the basename of the on-disk file that holds the
// PocketID STATIC_API_KEY for a homelab. It lives under <homelab>/.stackkit/.
const StaticAPIKeyFilename = "pocketid-static-api-key"

// minStaticAPIKeyLen is the smallest length we accept on read for the static
// API key. RandomPassword emits ~43 chars for a 32-byte input, so anything
// below 16 means the file is truncated, manually edited, or otherwise corrupt
// and we should refuse to use it rather than send a bad token to PocketID and
// chase a 401.
const minStaticAPIKeyLen = 16

// ReadStaticAPIKey returns the existing STATIC_API_KEY for a homelab without
// generating one when the file is missing. This supports reading immutable
// v0.6 artifacts during migration; current native generation uses owner custody.
func ReadStaticAPIKey(baseDir string) (string, error) {
	return readSecretFile(baseDir, StaticAPIKeyFilename, minStaticAPIKeyLen, "static API key")
}

// readSecretFile validates one immutable legacy secret without creating state.
func readSecretFile(baseDir, filename string, minLen int, label string) (string, error) {
	path := filepath.Join(baseDir, ".stackkit", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("legacy PocketID %s not found at %s; migrate this v0.6 workspace to native owner custody", label, path)
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key := string(data)
	if len(key) < minLen {
		return "", fmt.Errorf("legacy %s file %s is too short (%d bytes); restore the immutable v0.6 artifact or migrate the workspace", label, path, len(key))
	}
	return key, nil
}
