//go:build !unix

package decisionreceipt

import (
	"errors"
	"os"
)

// A platform without POSIX ownership has no root-owned custody chain, so it
// has no receipt path: every custody read and write is refused.

func fileOwner(os.FileInfo) (uint32, uint64, bool) { return 0, 0, false }

func openNoFollow(string) (*os.File, error) {
	return nil, errors.New("decision receipt custody is not supported on this platform")
}

func createExclusive(string) (*os.File, error) {
	return nil, errors.New("decision receipt custody is not supported on this platform")
}

func effectiveUser() (uint32, bool) { return 0, false }
