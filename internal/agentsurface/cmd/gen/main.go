package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/agentsurface"
	"github.com/kombifyio/stackkits/internal/kittemplates"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsurface gen: %v\n", err)
		os.Exit(1)
	}
	root, err := kittemplates.FindRepoRoot(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsurface gen: %v\n", err)
		os.Exit(1)
	}
	surfaces, err := agentsurface.LoadPackageSurfaces(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsurface gen: %v\n", err)
		os.Exit(1)
	}
	data, err := agentsurface.CatalogBytes(surfaces)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsurface gen: %v\n", err)
		os.Exit(1)
	}
	path := filepath.Join(root, "internal", "agentsurface", "surfaces.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "agentsurface gen: %v\n", err)
		os.Exit(1)
	}
}
