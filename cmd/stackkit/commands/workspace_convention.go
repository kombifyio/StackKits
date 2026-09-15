package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
)

const (
	conventionalBasementWorkspace = "my-homelab"
	conventionalCloudWorkspace    = "my-cloud-homelab"
)

func conventionalInstallerWorkspaceCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, conventionalBasementWorkspace),
		filepath.Join(home, conventionalCloudWorkspace),
	}
}

func stackSpecPresent(wd string) bool {
	if strings.TrimSpace(wd) == "" {
		return false
	}
	loader := config.NewLoader(wd)
	resolvedPath, _, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolvedPath)
	return err == nil && !info.IsDir()
}

func stackSpecModTime(wd string) (time.Time, bool) {
	loader := config.NewLoader(wd)
	resolvedPath, _, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || info.IsDir() {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func adoptConventionalInstallerWorkspace(wd string) string {
	if stackSpecPresent(wd) {
		return wd
	}
	var chosen string
	var chosenMod time.Time
	for _, candidate := range conventionalInstallerWorkspaceCandidates() {
		abs, err := filepath.Abs(candidate)
		if err != nil || abs == wd || !stackSpecPresent(abs) {
			continue
		}
		mod, ok := stackSpecModTime(abs)
		if !ok {
			continue
		}
		if chosen == "" || mod.After(chosenMod) {
			chosen = abs
			chosenMod = mod
		}
	}
	if chosen == "" {
		return wd
	}
	printInfo("No %s in %s; using installer workspace %s", specFile, wd, chosen)
	workDir = chosen
	return chosen
}

func missingNativeStackSpecError(wd, displayPath string, mode architectureV2ExecutionMode) error {
	if mode != architectureV2Remove {
		return fmt.Errorf(
			"%s: canonical StackSpec v2 is required on the v0.7 line; %s is missing and implicit legacy defaults are disabled (run stackkit init, then retry)",
			mode,
			displayPath,
		)
	}
	hint := strings.Join(conventionalInstallerWorkspaceCandidates(), " or ")
	if hint == "" {
		hint = filepath.Join("$HOME", conventionalBasementWorkspace)
	}
	return fmt.Errorf(
		"%s: %s is missing in %s; cd into the project directory (often %s) or pass --chdir, then retry",
		mode,
		displayPath,
		wd,
		hint,
	)
}
