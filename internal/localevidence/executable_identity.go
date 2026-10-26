package localevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
)

// CurrentExecutableSHA256 is the shared process identity observation used by
// lifecycle joins and the signed first-Apply record. Version and executor
// contract digests are not executable identities.
func CurrentExecutableSHA256() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current lifecycle executable: %w", err)
	}
	// The executing inode survives an atomic install-path replacement on
	// Linux. Hash that kernel reference instead of the newly named binary.
	if runtime.GOOS == "linux" {
		path = "/proc/self/exe"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read current lifecycle executable: %w", err)
	}
	if len(raw) == 0 || len(raw) > 512<<20 {
		return "", errors.New("current lifecycle executable bytes are invalid")
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
