package decisionreceipt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The node custody a receipt is verified and spent against. Every path is
// fixed here: no flag, no environment variable, no tool input and no
// workspace selects or redirects one, so a caller can neither offer its own
// keys nor keep its own spent record.
//
//   - The purpose-key snapshot is the one file the privileged refresher
//     writes (techstack/pkg/ril/nodeproof RefreshPurposeKeys: root only, file
//     0600, a window of at most 45 seconds, generation rollback refused). The
//     techstack-ril-keys.timer of a hosted node runs that refresher 15 seconds
//     after boot and then 20 seconds after each run, so the snapshot a command
//     reads is inside its window while the node reaches the platform. The same
//     file is the RIL proof verifier's (nodeproof.Verify); there is no second
//     path and no fallback.
//   - The node identity is the enrolled identity the privileged installer pins
//     after it admitted the hosted enrollment (nodeproof.PinLocalIdentity).
//   - The spent record is a node-level directory of this product.
//
// A node without the hosted enrollment has none of the first two, so it has
// no receipt path: the ledger's only filer is Techstack.
const (
	nodePurposeKeysPath = "/etc/techstack/ril-purpose-keys.json"
	nodeIdentityPath    = "/var/lib/techstack-guard-executor/ril-local-identity.json"
	nodeJournalPath     = "/var/lib/stackkit/decision-receipts"
)

// maxCustodyFileBytes bounds every custody file a consumer reads.
const maxCustodyFileBytes = 65536

// custody is where one consumer finds its keys, its node identity and its
// spent record, and who must own them. Production uses nodeCustody only; the
// struct exists so the package's own tests can build the same chain under a
// temporary directory they own.
type custody struct {
	// chainRoot is the first directory whose ownership is checked. It is the
	// file system root in production.
	chainRoot    string
	keysPath     string
	identityPath string
	journalDir   string
	// owner is the uid every checked directory and file must belong to: 0.
	owner uint32
}

func nodeCustody() custody {
	return custody{
		chainRoot:    "/",
		keysPath:     nodePurposeKeysPath,
		identityPath: nodeIdentityPath,
		journalDir:   nodeJournalPath,
		owner:        0,
	}
}

// readCustodyFile reads one custody file whose leaf and every parent from the
// chain root down belong to the custody owner and are not writable by group
// or other, whose leaf is a single-link regular file with exactly mode 0600
// (the mode the privileged writer creates it with), and that did not change
// between the check and the read. Symbolic links are refused everywhere.
func (c custody) readCustodyFile(path string) ([]byte, error) {
	if err := c.requireOwnedChain(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > maxCustodyFileBytes {
		return nil, fmt.Errorf("%s is not a private regular file of mode 0600", path)
	}
	owner, links, ok := fileOwner(info)
	if !ok || owner != c.owner || links != 1 {
		return nil, fmt.Errorf("%s is not owned by uid %d with a single link", path, c.owner)
	}
	file, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxCustodyFileBytes+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	return raw, nil
}

// requireOwnedChain checks one directory and each of its parents up to the
// chain root: a directory, not a symbolic link, owned by the custody owner
// and not writable by group or other. A directory an unprivileged account can
// write is a directory in which that account can replace the custody.
func (c custody) requireOwnedChain(dir string) error {
	root := filepath.Clean(c.chainRoot)
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) || !filepath.IsAbs(root) {
		return errors.New("custody paths must be absolute")
	}
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside the custody chain", dir)
	}
	current := root
	chain := []string{root}
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			chain = append(chain, current)
		}
	}
	for _, path := range chain {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		owner, _, ok := fileOwner(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || owner != c.owner || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is not a directory owned by uid %d that only its owner can write", path, c.owner)
		}
	}
	return nil
}
