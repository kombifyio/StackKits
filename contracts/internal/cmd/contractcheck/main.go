// Command contractcheck enforces the runtime contract boundary below
// contracts/: package imports, retired namespaces, package-root
// classification, URL literals and the Runtime Inventory generation digests.
// Run it from the StackKits repository root.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	mode := flag.String("mode", "boundary", "check mode: boundary or runtimeinventory")
	root := flag.String("root", "contracts", "contract tree below the StackKits repository root")
	flag.Parse()
	var err error
	switch *mode {
	case "boundary":
		err = checkContractBoundary(*root)
	case "runtimeinventory":
		err = verifyRuntimeInventoryManifest(*root)
	default:
		fmt.Fprintf(os.Stderr, "unsupported contractcheck mode %q\n", *mode)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("contracts-%s=PASS\n", *mode)
}
