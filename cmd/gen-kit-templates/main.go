// Command gen-kit-templates materializes the per-kit OpenTofu/Terramate template
// trees (basement-kit/templates/, cloud-kit/templates/) from the single
// canonical source in base/templates/.
//
// It is wired via the `//go:generate` directive in internal/kittemplates and
// therefore runs as part of `go generate ./...` (including the goreleaser
// `before` hooks). The repository root is located by walking up to the nearest
// go.mod, so the tool works regardless of the working directory.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/kombifyio/stackkits/internal/kittemplates"
)

func main() {
	repoRoot := flag.String("repo-root", "", "repository root (default: nearest go.mod ancestor of the working directory)")
	flag.Parse()

	root := *repoRoot
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("gen-kit-templates: %v", err)
		}
		root, err = kittemplates.FindRepoRoot(wd)
		if err != nil {
			log.Fatalf("gen-kit-templates: %v", err)
		}
	}

	if err := kittemplates.Generate(root); err != nil {
		log.Fatalf("gen-kit-templates: %v", err)
	}
	for _, kit := range kittemplates.Kits {
		fmt.Printf("gen-kit-templates: wrote %s/templates\n", kit.Slug)
	}
}
