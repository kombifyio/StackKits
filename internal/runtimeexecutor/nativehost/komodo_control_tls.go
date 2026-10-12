package nativehost

import (
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// The Core router owns the TLS policy. The control listener has no published
// host port; only the node's private service network can reach it.
const komodoControlTLSOptions = "tls:\n  options:\n    stackkit-komodo:\n      minVersion: VersionTLS13\n      sniStrict: true\n"

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
	view, err := root.View(".stackkit/runtime/basement-core")
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600("tls-options.yaml", []byte(komodoControlTLSOptions))
	return err
}
