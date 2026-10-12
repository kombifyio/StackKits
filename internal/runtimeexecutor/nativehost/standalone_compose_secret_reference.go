package nativehost

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"os"
	"path/filepath"
	"strings"
)

// Reuse the admitted private runtime artifact, not a second custody store.
// Stable-file validation retains the existing owner, path and symlink checks.
func (o *osStandaloneComposeWorkloadOperations) secretReferenceFileContent(file architecturev2renderer.WorkloadSecretReferenceFile, value, project, variable string) (string, error) {
	if !file.BcryptBase64 {
		return file.Content(value)
	}
	name := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "applications", project, standaloneComposeEnvFile)
	_, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return file.Content(value)
	}
	if err != nil {
		return "", errors.New("read protected workload verifier custody")
	}
	root, err := confinedfs.Open(filepath.Dir(name))
	if err != nil {
		return "", errors.New("open protected workload verifier directory")
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", errors.New("begin protected workload verifier read")
	}
	defer transaction.Close()
	content, info, err := transaction.ReadStable(standaloneComposeEnvFile)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || len(content) > basementCoreProcessOutputLimit {
		return "", errors.New("invalid protected workload verifier artifact")
	}
	defer clear(content)
	previous, matches := "", 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, variable+"=") {
			previous = strings.TrimSuffix(strings.TrimPrefix(line, variable+"="), "\r")
			matches++
		}
	}
	if matches > 1 {
		return "", errors.New("ambiguous private workload verifier")
	}
	return file.ContentWithPrevious(value, previous)
}
