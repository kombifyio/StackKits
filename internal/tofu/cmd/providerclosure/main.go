package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/tofu"
)

func main() {
	providers := flag.String("providers-dir", "", "provider mirror root")
	targetOS := flag.String("os", "", "release target operating system")
	targetArch := flag.String("arch", "", "release target architecture")
	archive := flag.String("archive", "", "downloaded provider archive")
	flag.Parse()
	if *providers == "" || *targetOS == "" || *targetArch == "" || *archive == "" {
		fmt.Fprintln(os.Stderr, "providers-dir, os, arch, and archive are required")
		os.Exit(2)
	}
	if err := tofu.PrepareProviderClosure(*providers, *targetOS, *targetArch, *archive); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
