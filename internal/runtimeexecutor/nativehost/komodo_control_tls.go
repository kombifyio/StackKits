package nativehost

import (
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/publiccert"
)

// The Core router owns the TLS policy: the Komodo control listener's minimum
// version (it has no published host port; only the node's private service
// network reaches it) and, on a public identity origin, the public-names
// certificate (publiccert.TLSOptions). The router mounts its directory
// (router-tls) and watches it; the file is replaced atomically.
func prepareCoreControlTLS(workspace string) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err := tx.MkdirAll(".stackkit/runtime/basement-core", 0700); err != nil {
		return err
	}
	return publiccert.WriteRouterPolicy(workspace, true)
}
