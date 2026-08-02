package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/contractgen/stackactiongen"
)

func main() {
	root := flag.String("repo-root", ".", "StackKits repository root")
	external := flag.String("external-go-output", "", "optional generated staging Go file outside this repository")
	externalBundle := flag.String("external-bundle-output", "", "optional generated StackAction contract bundle directory outside this repository")
	check := flag.Bool("check", false, "verify generated outputs without writing")
	flag.Parse()
	if err := stackactiongen.Run(stackactiongen.Options{RepoRoot: *root, ExternalGoOutput: *external, ExternalBundleOutput: *externalBundle, Check: *check}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
