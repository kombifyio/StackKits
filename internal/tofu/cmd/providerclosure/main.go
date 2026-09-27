package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/tofu"
)

type providerArchives map[string]string

func (a providerArchives) String() string { return fmt.Sprint(map[string]string(a)) }

func (a providerArchives) Set(value string) error {
	source, path, ok := strings.Cut(value, "=")
	if !ok || source == "" || path == "" || a[source] != "" {
		return fmt.Errorf("archive must be a unique provider-source=path pair")
	}
	a[source] = path
	return nil
}

func main() {
	providers := flag.String("providers-dir", "", "provider mirror root")
	targetOS := flag.String("os", "", "release target operating system")
	targetArch := flag.String("arch", "", "release target architecture")
	archives := make(providerArchives)
	flag.Var(archives, "archive", "provider-source=downloaded archive (repeat for every packaged provider)")
	flag.Parse()
	if *providers == "" || *targetOS == "" || *targetArch == "" || len(archives) == 0 {
		fmt.Fprintln(os.Stderr, "providers-dir, os, arch, and archives are required")
		os.Exit(2)
	}
	if err := tofu.PrepareProviderClosure(*providers, *targetOS, *targetArch, archives); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
