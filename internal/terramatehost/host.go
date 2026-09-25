// Package terramatehost materializes the Terramate host project of one node
// and runs the packaged Terramate binary over it (ADR-0045 section 2 and
// section 5, docs/ARCHITECTURE.md "Advanced change sets through Terramate
// (Stage 1)").
//
// A host project is the executor-managed runtime tree `.stackkit/runtime` of
// the workspace. Generation renders the project root (`terramate.tm.hcl`,
// owned by the Core host bootstrap module) and one `stack.tm.hcl` per stack as
// artifact-only files; this package places them where the stack graph says:
// the root file at `.stackkit/runtime/terramate.tm.hcl` and every stack file
// in its stack's runtime root, next to the OpenTofu `main.tf` that the runtime
// executor installs. Placement is derived from the graph and the rendered
// bytes only, so it is deterministic and the manifest digest can be computed
// before any write (change-set create) and proven again after (apply).
package terramatehost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

const (
	// ManifestAPIVersion identifies the host manifest document.
	ManifestAPIVersion = "stackkit.terramate-host-manifest/v1"
	// ManifestPath is the workspace-relative manifest location. It sits
	// beside, not inside, the runtime tree, so it never becomes part of the
	// Terramate project.
	ManifestPath = ".stackkit/terramate-host-manifest.json"
	// RootConfigFile is the Terramate project root configuration.
	RootConfigFile = "terramate.tm.hcl"
	// StackFile is the Terramate stack definition inside each runtime root.
	StackFile = "stack.tm.hcl"
	// OpenTofuConfigFile is the OpenTofu root module file the runtime
	// executor installs in a stack's runtime root.
	OpenTofuConfigFile = "main.tf"

	fileMode     os.FileMode = 0o640
	manifestMode os.FileMode = 0o600
	dirMode      os.FileMode = 0o750
)

// FileRecord binds one placed file to its bytes.
type FileRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ManifestStack is one stack of the host project.
type ManifestStack struct {
	ID           string     `json:"id"`
	Role         string     `json:"role"`
	RuntimeRoot  string     `json:"runtimeRoot"`
	OpenTofuRoot string     `json:"openTofuRoot"`
	StackFile    FileRecord `json:"stackFile"`
}

// Manifest is the `stackkit.terramate-host-manifest/v1` record of one host
// project. It holds no time or machine facts, so equal inputs produce
// byte-identical manifests and digests.
type Manifest struct {
	APIVersion          string          `json:"apiVersion"`
	StackID             string          `json:"stackId"`
	PlanHash            string          `json:"planHash"`
	SiteRef             string          `json:"siteRef"`
	NodeRef             string          `json:"nodeRef"`
	ExecutionChannelRef string          `json:"executionChannelRef,omitempty"`
	ProjectRoot         string          `json:"projectRoot"`
	RootConfig          FileRecord      `json:"rootConfig"`
	RunOrder            []string        `json:"runOrder"`
	Stacks              []ManifestStack `json:"stacks"`
}

// Layout is the complete, not yet written host project of one node.
type Layout struct {
	Graph          terramatestackgraph.Graph
	Host           terramatestackgraph.Host
	Manifest       Manifest
	ManifestBytes  []byte
	ManifestSHA256 string
	files          []placedFile
}

type placedFile struct {
	path  string
	bytes []byte
	mode  os.FileMode
}

// Stack returns the graph stack with id.
func (layout Layout) Stack(id string) (terramatestackgraph.Stack, bool) {
	for _, stack := range layout.Graph.Stacks {
		if stack.ID == id {
			return stack, true
		}
	}
	return terramatestackgraph.Stack{}, false
}

// GraphFromArtifacts parses the plan-owned stack graph among rendered
// artifacts. A render without a graph is not a `terramate` target render.
func GraphFromArtifacts(artifacts []architecturev2renderer.Artifact) (terramatestackgraph.Graph, error) {
	for _, artifact := range artifacts {
		if artifact.ID == terramatestackgraph.ArtifactID {
			return terramatestackgraph.Parse(artifact.Bytes)
		}
	}
	return terramatestackgraph.Graph{}, errors.New("render has no Terramate stack graph; the generation target must be terramate")
}

// SelectHost returns the graph host of siteRef/nodeRef. Empty refs select the
// only host of a single-host graph.
func SelectHost(graph terramatestackgraph.Graph, siteRef, nodeRef string) (terramatestackgraph.Host, error) {
	if siteRef == "" && nodeRef == "" {
		if len(graph.Hosts) == 1 {
			return graph.Hosts[0], nil
		}
		return terramatestackgraph.Host{}, fmt.Errorf("stack graph has %d hosts; the local site and node are required", len(graph.Hosts))
	}
	for _, host := range graph.Hosts {
		if host.SiteRef == siteRef && host.NodeRef == nodeRef {
			return host, nil
		}
	}
	return terramatestackgraph.Host{}, fmt.Errorf("stack graph has no host for node %s of site %s", nodeRef, siteRef)
}

// PlanFromArtifacts derives the host layout of siteRef/nodeRef from one
// rendered `terramate` target render.
func PlanFromArtifacts(artifacts []architecturev2renderer.Artifact, siteRef, nodeRef string) (Layout, error) {
	graph, err := GraphFromArtifacts(artifacts)
	if err != nil {
		return Layout{}, err
	}
	byID := make(map[string][]byte, len(artifacts))
	for _, artifact := range artifacts {
		byID[artifact.ID] = artifact.Bytes
	}
	return Plan(graph, byID, siteRef, nodeRef)
}

// Plan derives the host layout from the graph and the rendered bytes keyed by
// artifact ID. It performs no I/O.
func Plan(graph terramatestackgraph.Graph, artifacts map[string][]byte, siteRef, nodeRef string) (Layout, error) {
	host, err := SelectHost(graph, siteRef, nodeRef)
	if err != nil {
		return Layout{}, err
	}
	rootBytes, exists := artifacts[host.RootConfigArtifact]
	if !exists || len(rootBytes) == 0 {
		return Layout{}, fmt.Errorf("host %s/%s: project root artifact %s was not rendered", host.SiteRef, host.NodeRef, host.RootConfigArtifact)
	}
	rootPath := path.Join(host.ProjectRoot, RootConfigFile)
	layout := Layout{
		Graph: graph, Host: host,
		files: []placedFile{{path: rootPath, bytes: bytes.Clone(rootBytes), mode: fileMode}},
		Manifest: Manifest{
			APIVersion: ManifestAPIVersion, StackID: graph.StackID, PlanHash: graph.PlanHash,
			SiteRef: host.SiteRef, NodeRef: host.NodeRef, ExecutionChannelRef: host.ExecutionChannelRef,
			ProjectRoot: host.ProjectRoot, RootConfig: FileRecord{Path: rootPath, SHA256: digest(rootBytes)},
			RunOrder: append([]string{}, host.RunOrder...), Stacks: make([]ManifestStack, 0, len(host.RunOrder)),
		},
	}
	for _, id := range host.RunOrder {
		stack, found := layout.Stack(id)
		if !found {
			return Layout{}, fmt.Errorf("host run order names unknown stack %s", id)
		}
		stackBytes, exists := artifacts[stack.Artifacts.Stack]
		if !exists || len(stackBytes) == 0 {
			return Layout{}, fmt.Errorf("stack %s: stack artifact %s was not rendered", stack.ID, stack.Artifacts.Stack)
		}
		if !bytes.Contains(stackBytes, []byte(`id          = "`+stack.ID+`"`)) {
			return Layout{}, fmt.Errorf("stack %s: rendered %s does not carry the graph stack id", stack.ID, StackFile)
		}
		stackPath := path.Join(stack.RuntimeRoot, StackFile)
		layout.files = append(layout.files, placedFile{path: stackPath, bytes: bytes.Clone(stackBytes), mode: fileMode})
		layout.Manifest.Stacks = append(layout.Manifest.Stacks, ManifestStack{
			ID: stack.ID, Role: string(stack.Role), RuntimeRoot: stack.RuntimeRoot,
			OpenTofuRoot: stack.OpenTofuRoot, StackFile: FileRecord{Path: stackPath, SHA256: digest(stackBytes)},
		})
	}
	raw, err := marshal(layout.Manifest)
	if err != nil {
		return Layout{}, err
	}
	layout.ManifestBytes = raw
	layout.ManifestSHA256 = digest(raw)
	return layout, nil
}

// MaterializeResult reports which workspace-relative files were written. A
// second materialization of the same layout writes nothing.
type MaterializeResult struct {
	Written        []string `json:"written"`
	ManifestSHA256 string   `json:"manifestSha256"`
}

// Materialize places the layout under workspaceRoot. It creates missing
// runtime roots (the executor may not have installed `main.tf` yet), rewrites
// a file only when its bytes differ, refuses symlinks and non-regular files on
// the way, and writes the manifest last.
func Materialize(workspaceRoot string, layout Layout) (MaterializeResult, error) {
	if len(layout.files) == 0 || len(layout.ManifestBytes) == 0 {
		return MaterializeResult{}, errors.New("host layout is empty; derive it with Plan")
	}
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return MaterializeResult{}, fmt.Errorf("resolve workspace: %w", err)
	}
	if info, statErr := os.Lstat(workspace); statErr != nil || !info.IsDir() {
		return MaterializeResult{}, fmt.Errorf("workspace %s must be an existing plain directory", workspace)
	}
	result := MaterializeResult{Written: make([]string, 0), ManifestSHA256: layout.ManifestSHA256}
	files := append(append([]placedFile{}, layout.files...), placedFile{path: ManifestPath, bytes: layout.ManifestBytes, mode: manifestMode})
	for _, file := range files {
		written, err := placeFile(workspace, file)
		if err != nil {
			return result, err
		}
		if written {
			result.Written = append(result.Written, file.path)
		}
	}
	return result, nil
}

func placeFile(workspace string, file placedFile) (bool, error) {
	clean := path.Clean(file.path)
	if clean != file.path || path.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, "../") || !strings.HasPrefix(clean, ".stackkit/") {
		return false, fmt.Errorf("host file %q is outside the workspace .stackkit tree", file.path)
	}
	directory := workspace
	for _, segment := range strings.Split(path.Dir(clean), "/") {
		directory = filepath.Join(directory, segment)
		info, err := os.Lstat(directory)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(directory, dirMode); err != nil {
				return false, fmt.Errorf("create %s: %w", directory, err)
			}
		case err != nil:
			return false, fmt.Errorf("inspect %s: %w", directory, err)
		case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
			return false, fmt.Errorf("%s must be a plain directory", directory)
		}
	}
	target := filepath.Join(workspace, filepath.FromSlash(clean))
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s must be a regular file", target)
		}
		existing, readErr := os.ReadFile(target)
		if readErr != nil {
			return false, fmt.Errorf("read %s: %w", target, readErr)
		}
		if bytes.Equal(existing, file.bytes) {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect %s: %w", target, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("stage %s: %w", target, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(file.bytes); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("write %s: %w", target, err)
	}
	if err := temporary.Chmod(file.mode); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("protect %s: %w", target, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("sync %s: %w", target, err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", target, err)
	}
	if err := os.Rename(name, target); err != nil {
		return false, fmt.Errorf("install %s: %w", target, err)
	}
	return true, nil
}

func marshal(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
