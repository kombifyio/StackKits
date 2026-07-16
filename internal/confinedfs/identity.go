package confinedfs

import (
	"fmt"
	"os"
)

// Identity is a same-filesystem identity for a directory or regular file while
// a surrounding operation still holds and revalidates the object. Filesystems
// may reuse it after handle close, so it is neither a durable journal key nor
// an authorization credential.
type Identity struct {
	Scheme string `json:"scheme"`
	Volume uint64 `json:"volume"`
	File   uint64 `json:"file"`
}

// Valid reports whether the identity was populated by a supported platform.
func (i Identity) Valid() bool { return i.Scheme != "" }

// String returns a deterministic diagnostic representation. It must not be
// used as a durable lock, journal, or authorization identity on its own.
func (i Identity) String() string {
	return fmt.Sprintf("%s:%016x:%016x", i.Scheme, i.Volume, i.File)
}

func identityForOpenFile(file *os.File) (Identity, error) {
	if file == nil {
		return Identity{}, fail(ErrIdentityUnsupported, "identity", "file", "an open file handle is required")
	}
	identity, err := platformFileIdentity(file)
	if err != nil {
		return Identity{}, wrap(ErrIdentityUnsupported, "identity", file.Name(), "read platform file identity", err)
	}
	if !identity.Valid() {
		return Identity{}, fail(ErrIdentityUnsupported, "identity", file.Name(), "platform returned an empty file identity")
	}
	return identity, nil
}
