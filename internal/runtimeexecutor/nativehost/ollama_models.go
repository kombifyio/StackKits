package nativehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ollamaDataDirectory is the Ollama models volume mount of the Private AI
// bundle; the disk budget is measured on it.
const ollamaDataDirectory = "/root/.ollama"

var ollamaModelTagRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}:[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// StandaloneComposeOllama runs the owner's model management through the fixed
// Compose exec contract inside the admitted Private AI deployment's `ollama`
// component. It offers exactly: ollama list, ollama pull <tag>, ollama rm
// <tag> and the free space of the models volume.
type StandaloneComposeOllama struct {
	operations *osStandaloneComposeWorkloadOperations
	project    standaloneComposeProject
}

// NewStandaloneComposeOllama admits the deployment: only the Private AI
// workload with its persisted Compose custody intact.
func NewStandaloneComposeOllama(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*StandaloneComposeOllama, error) {
	if deployment.ModuleRef != privateAIModuleRef {
		return nil, errors.New("model management belongs only to the Private AI workload")
	}
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	o, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return nil, errors.New("standalone Compose operations have an unexpected implementation")
	}
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return nil, err
	}
	if err := o.verifyPersisted(project); err != nil {
		return nil, err
	}
	if _, found := standaloneComposeComponent(project.bundle.Components, "ollama"); !found || project.bundle.ModuleRef != privateAIModuleRef {
		return nil, errors.New("the Private AI bundle has no Ollama component")
	}
	return &StandaloneComposeOllama{operations: o, project: project}, nil
}

// Ollama runs `ollama <args...>` in the Ollama component. Progress is written
// to out as it happens.
func (h *StandaloneComposeOllama) Ollama(ctx context.Context, out io.Writer, args ...string) ([]byte, error) {
	switch {
	case len(args) == 1 && args[0] == "list":
	case len(args) == 2 && (args[0] == "pull" || args[0] == "rm") && ollamaModelTagRE.MatchString(args[1]):
	default:
		return nil, errors.New("model management admits only list, pull <tag> and rm <tag>")
	}
	return h.exec(ctx, out, append([]string{"ollama"}, args...)...)
}

// FreeBytes returns the free space of the Ollama models volume.
func (h *StandaloneComposeOllama) FreeBytes(ctx context.Context) (int64, error) {
	raw, err := h.exec(ctx, nil, "df", "-Pk", ollamaDataDirectory)
	if err != nil {
		return 0, fmt.Errorf("read the free space of the Ollama models volume: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return 0, errors.New("the Ollama models volume free space is unreadable")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, errors.New("the Ollama models volume free space is unreadable")
	}
	kib, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || kib < 0 {
		return 0, errors.New("the Ollama models volume free space is unreadable")
	}
	return kib * 1024, nil
}

func (h *StandaloneComposeOllama) exec(ctx context.Context, out io.Writer, command ...string) ([]byte, error) {
	args := []string{
		"compose", "--project-name", h.project.name, "--env-file", filepath.Join(h.project.directory, ".env"),
		"-f", filepath.Join(h.project.directory, "compose.yaml"),
		"exec", "-T", "ollama",
	}
	args = append(args, command...)
	if streaming, ok := h.operations.runner.(interface {
		RunStreaming(context.Context, []string, string, io.Writer) ([]byte, error)
	}); ok && out != nil {
		return streaming.RunStreaming(ctx, args, h.project.directory, out)
	}
	raw, err := h.operations.runner.Run(ctx, args, h.project.directory)
	if err == nil && out != nil {
		_, _ = out.Write(raw)
	}
	return raw, err
}
