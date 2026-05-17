package main

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/api/openapi"
	stackkitdocs "github.com/kombifyio/stackkits/docs"
)

func loadDocs() map[string]string {
	out := map[string]string{}
	_ = fs.WalkDir(stackkitdocs.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, readErr := stackkitdocs.FS.ReadFile(path)
		if readErr == nil {
			out["docs/"+path] = string(raw)
		}
		return nil
	})
	out["api/openapi.v1.yaml"] = openAPISpec()
	return out
}

func openAPISpec() string {
	return string(openapi.Spec)
}

func endpointSnippet(spec, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	idx := strings.Index(spec, "\n  "+path+":")
	if idx < 0 {
		idx = strings.Index(spec, "\n  \""+path+"\":")
	}
	if idx < 0 {
		return ""
	}
	end := strings.Index(spec[idx+1:], "\n  /")
	if end < 0 {
		end = min(len(spec)-idx, 3000)
	} else {
		end++
	}
	return strings.TrimSpace(spec[idx : idx+end])
}

func openAPIEndpoints(spec string) []string {
	re := regexp.MustCompile(`(?m)^ {2}(/[^:]+):`)
	matches := re.FindAllStringSubmatch(spec, -1)
	endpoints := make([]string, 0, len(matches))
	for _, m := range matches {
		endpoints = append(endpoints, strings.TrimSpace(m[1]))
	}
	sort.Strings(endpoints)
	return endpoints
}

func resourceTitle(uri string) string {
	base := strings.TrimSuffix(uri, ".md")
	base = strings.TrimSuffix(base, ".yaml")
	base = strings.ReplaceAll(base, "/", " ")
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	return strings.TrimSpace(base)
}

func mimeTypeForResource(uri string) string {
	switch {
	case strings.HasSuffix(uri, ".yaml"), strings.HasSuffix(uri, ".yml"):
		return "application/yaml"
	case strings.HasSuffix(uri, ".json"):
		return "application/json"
	case strings.HasSuffix(uri, ".txt"):
		return "text/plain"
	default:
		return "text/markdown"
	}
}

func resourcePriority(uri string) float64 {
	switch uri {
	case "api/openapi.v1.yaml", "docs/API.md", "docs/CLI.md", "docs/agent/agents.md", "docs/agent/stackkit-mcp.md":
		return 1.0
	case "docs/stack-spec-reference.md", "docs/agent/monitoring.md":
		return 0.9
	default:
		return 0.6
	}
}

func promptText(name string) string {
	switch name {
	case "stackkit_basekit_autonomous_rollout":
		return embeddedPrompt("docs/agent/basekit-autonomous-rollout.md")
	case "stackkit_inspect_existing_rollout":
		return embeddedPrompt("docs/agent/inspect-existing-rollout.md")
	case "stackkit_diagnose_failed_rollout":
		return embeddedPrompt("docs/agent/diagnose-failed-rollout.md")
	case "stackkit_enable_monitoring_addon":
		return embeddedPrompt("docs/agent/enable-monitoring-addon.md")
	case "stackkit_ssh_rollout":
		return embeddedPrompt("docs/agent/ssh-rollout.md")
	default:
		return "Use https://stackkit.cc/llms.txt and the local stackkit-mcp tools to complete this StackKits task. Prefer BaseKit for release-ready autonomous rollout."
	}
}

func embeddedPrompt(path string) string {
	docs := loadDocs()
	if body := docs[path]; body != "" {
		return body
	}
	return "Prompt not embedded: " + path
}
