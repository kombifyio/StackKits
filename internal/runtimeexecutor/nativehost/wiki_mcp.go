package nativehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// WikiMCPInstalled advertises the optional tool only for one admitted local
// installation. Runtime readiness is checked again for each call.
func WikiMCPInstalled(workspace string) bool {
	var err error
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return false
	}
	projects, err := filepath.Glob(filepath.Join(workspace, ".stackkit", "runtime", "applications", "stackkit-wiki-*"))
	if err != nil || len(projects) != 1 {
		return false
	}
	image, exists, err := readPersistedEntryImage(projects[0], filepath.Base(projects[0]), "wiki")
	if err != nil || !exists {
		return false
	}
	for _, release := range architecturev2renderer.AdmittedWorkloadReleases("stackkits-wiki-runtime") {
		if image == release.Ref+"@"+release.Digest {
			return true
		}
	}
	return false
}

// WikiMCPConnection resolves only this server's installed, pinned Wiki. No
// caller-supplied endpoint or credential can redirect the MCP proxy.
func WikiMCPConnection(ctx context.Context, workspace string) (endpoint, bearer string, err error) {
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return "", "", err
	}
	projects, err := filepath.Glob(filepath.Join(workspace, ".stackkit", "runtime", "applications", "stackkit-wiki-*"))
	if err != nil || len(projects) != 1 {
		return "", "", errors.New("Wiki is not installed on this server, or its runtime identity is ambiguous")
	}
	directory, name := projects[0], filepath.Base(projects[0])
	image, exists, err := readPersistedEntryImage(directory, name, "wiki")
	if err != nil || !exists {
		return "", "", errors.New("Wiki runtime custody is unavailable")
	}
	admitted := false
	for _, release := range architecturev2renderer.AdmittedWorkloadReleases("stackkits-wiki-runtime") {
		admitted = admitted || image == release.Ref+"@"+release.Digest
	}
	if !admitted {
		return "", "", errors.New("Wiki runtime image differs from the admitted release")
	}
	project := standaloneComposeProject{name: name, directory: directory,
		bundle: architecturev2renderer.ApplicationDeliveryBundleDescriptor{EntryComponent: "wiki"},
		entry:  architecturev2renderer.ApplicationDeliveryComponentDescriptor{HealthPort: 8484},
	}
	runner := osStandaloneComposeProcessRunner{}
	raw, err := runner.Run(ctx, standaloneComposeArgs(project, "ps"), directory)
	if err != nil {
		return "", "", err
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil || len(statuses) != 1 || statuses["wiki"].State != "running" || statuses["wiki"].Image != image {
		return "", "", errors.New("Wiki is not running at its admitted image")
	}
	raw, err = runner.Run(ctx, standaloneComposeArgs(project, "port"), directory)
	if err != nil {
		return "", "", err
	}
	address, err := standaloneComposeLoopbackAddress(raw)
	if err != nil {
		return "", "", err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", "", err
	}
	defer tx.Close()
	env, info, err := tx.ReadStableBounded(".env", standaloneComposeOutputMax)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != os.FileMode(0o600) {
		return "", "", errors.New("Wiki reader custody is unavailable")
	}
	defer clear(env)
	for _, line := range strings.Split(string(env), "\n") {
		if value, ok := strings.CutPrefix(line, standaloneComposeSecretVariable("wiki", "WIKI_READ_BEARER")+"="); ok {
			if bearer != "" || len(value) < 32 || strings.ContainsAny(value, "\r\x00") {
				return "", "", errors.New("Wiki reader custody is invalid or ambiguous")
			}
			bearer = value
		}
	}
	if bearer == "" {
		return "", "", errors.New("Wiki reader custody is missing")
	}
	return "http://" + address + "/mcp", bearer, nil
}
