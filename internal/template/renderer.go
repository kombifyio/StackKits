// Package template handles template rendering for StackKits.
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// Renderer handles template rendering
type Renderer struct {
	templateDir string
	outputDir   string
	funcMap     template.FuncMap
}

// NewRenderer creates a new template renderer
func NewRenderer(templateDir, outputDir string) *Renderer {
	r := &Renderer{
		templateDir: templateDir,
		outputDir:   outputDir,
	}
	r.funcMap = r.defaultFuncMap()
	return r
}

// RenderContext contains data passed to templates
type RenderContext struct {
	Spec      *models.StackSpec
	StackKit  *models.StackKit
	Services  []ServiceContext
	Variables map[string]interface{}
	// Catalog is the CUE-driven service catalog for domain + dashboard generation.
	Catalog []cueval.CatalogEntry
	// Domains includes all services that need a local.domains entry (catalog + dashboard).
	Domains []cueval.CatalogEntry
}

// ServiceContext contains service-specific data for templates
type ServiceContext struct {
	Name        string
	Image       string
	Ports       []PortMapping
	Volumes     []VolumeMapping
	Environment map[string]string
	Labels      map[string]string
	Networks    []string
	DependsOn   []string
	Enabled     bool
}

// PortMapping represents a port mapping
type PortMapping struct {
	Host      int
	Container int
	Protocol  string
}

// VolumeMapping represents a volume mapping
type VolumeMapping struct {
	Source   string
	Target   string
	ReadOnly bool
}

// Render renders all templates to the output directory
func (r *Renderer) Render(ctx *RenderContext) error {
	// Ensure output directory exists
	if err := os.MkdirAll(r.outputDir, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Find all template files
	templates, err := r.findTemplates()
	if err != nil {
		return fmt.Errorf("failed to find templates: %w", err)
	}

	// Render each template
	for _, tmplPath := range templates {
		if err := r.renderTemplate(tmplPath, ctx); err != nil {
			return fmt.Errorf("failed to render %s: %w", tmplPath, err)
		}
	}

	return nil
}

// RenderSingle renders a single template
func (r *Renderer) RenderSingle(templateName string, ctx *RenderContext) (string, error) {
	tmplPath := filepath.Join(r.templateDir, templateName)

	data, err := os.ReadFile(tmplPath)
	if err != nil {
		return "", fmt.Errorf("failed to read template: %w", err)
	}

	tmpl, err := template.New(templateName).Funcs(r.funcMap).Parse(string(data))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// findTemplates finds all template files in the template directory
func (r *Renderer) findTemplates() ([]string, error) {
	var templates []string

	err := filepath.Walk(r.templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Include .tf, .tf.tmpl, .tmpl files
		ext := filepath.Ext(path)
		if ext == ".tf" || ext == ".tmpl" || strings.HasSuffix(path, ".tf.tmpl") {
			templates = append(templates, path)
		}
		return nil
	})

	return templates, err
}

// renderTemplate renders a single template file
func (r *Renderer) renderTemplate(tmplPath string, ctx *RenderContext) error {
	data, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}

	tmpl, err := template.New(filepath.Base(tmplPath)).Funcs(r.funcMap).Parse(string(data))
	if err != nil {
		return err
	}

	// Determine output filename
	relPath, _ := filepath.Rel(r.templateDir, tmplPath)
	outPath := filepath.Join(r.outputDir, relPath)

	// Remove .tmpl extension if present
	outPath = strings.TrimSuffix(outPath, ".tmpl")

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outPath), 0750); err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return err
	}

	return os.WriteFile(outPath, buf.Bytes(), 0600)
}

// defaultFuncMap returns the default template functions
func (r *Renderer) defaultFuncMap() template.FuncMap {
	return template.FuncMap{
		"lower":          strings.ToLower,
		"upper":          strings.ToUpper,
		"title":          cases.Title(language.English).String,
		"trim":           strings.TrimSpace,
		"replace":        strings.ReplaceAll,
		"contains":       strings.Contains,
		"hasPrefix":      strings.HasPrefix,
		"hasSuffix":      strings.HasSuffix,
		"join":           strings.Join,
		"split":          strings.Split,
		"default":        defaultValue,
		"quote":          quote,
		"indent":         indent,
		"toYaml":         toYaml,
		"toJson":         toJSON,
		"toJsonPretty":   toJSONPretty,
		"ifEnabled":      ifEnabled,
		"serviceFor":     serviceFor,
		"envMap":         envMap,
		"labelMap":       labelMap,
		"portList":       portList,
		"catalogSection": catalogSection,
		"hasEnableVar":   hasEnableVar,
	}
}

// defaultValue returns the default if value is empty
func defaultValue(def, val interface{}) interface{} {
	if val == nil || val == "" {
		return def
	}
	return val
}

// quote wraps a string in quotes
func quote(s string) string {
	return fmt.Sprintf(`"%s"`, s)
}

// indent adds indentation to each line
func indent(spaces int, s string) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// toYaml converts to YAML string (TD-013: proper implementation)
func toYaml(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("# Error: %v", err)
	}
	return strings.TrimSuffix(string(data), "\n")
}

// toJSON converts to JSON string (TD-013: proper implementation).
// Exposed in templates as "toJson" for backward compatibility with existing
// template files; the Go identifier follows the canonical ID initialism.
func toJSON(v interface{}) string {
	if v == nil {
		return "null"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": "%v"}`, err)
	}
	return string(data)
}

// toJSONPretty converts to pretty-printed JSON string.
// Exposed in templates as "toJsonPretty" for backward compatibility.
func toJSONPretty(v interface{}) string {
	if v == nil {
		return "null"
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "%v"}`, err)
	}
	return string(data)
}

// ifEnabled returns value if condition is true
func ifEnabled(cond bool, val interface{}) interface{} {
	if cond {
		return val
	}
	return nil
}

// serviceFor finds a service by name
func serviceFor(name string, services []ServiceContext) *ServiceContext {
	for _, s := range services {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

// envMap creates environment variable HCL
func envMap(env map[string]string) string {
	if len(env) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf(`    %s = %q`, k, env[k]))
	}
	return "{\n" + strings.Join(lines, "\n") + "\n  }"
}

// labelMap creates labels HCL
func labelMap(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf(`    "%s" = %q`, k, labels[k]))
	}
	return "{\n" + strings.Join(lines, "\n") + "\n  }"
}

// portList creates ports HCL
func portList(ports []PortMapping) string {
	if len(ports) == 0 {
		return ""
	}
	var blocks []string
	for _, p := range ports {
		protocol := p.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		block := fmt.Sprintf(`  ports {
    internal = %d
    external = %d
    protocol = %q
  }`, p.Container, p.Host, protocol)
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n")
}

// catalogSection filters catalog entries by section name
func catalogSection(section string, catalog []cueval.CatalogEntry) []cueval.CatalogEntry {
	var result []cueval.CatalogEntry
	for _, e := range catalog {
		if e.Section == section {
			result = append(result, e)
		}
	}
	return result
}

// hasEnableVar returns true if the entry has a Terraform enable variable
func hasEnableVar(e cueval.CatalogEntry) bool {
	return e.EnableVar != ""
}

// validateHCLValue checks that a string is safe to embed in HCL.
// It rejects values containing characters that could break out of a quoted HCL string.
func validateHCLValue(name, value string) error {
	for _, c := range value {
		switch {
		case c == '"', c == '\\', c == '\n', c == '\r', c == '$', c == '{', c == '}':
			return fmt.Errorf("%s contains unsafe character %q for HCL embedding", name, c)
		}
	}
	return nil
}

// GenerateMainTf generates the main.tf file content.
// All user-controlled values are validated before embedding into HCL.
func GenerateMainTf(ctx *RenderContext) (string, error) {
	// Validate all user-controlled values before embedding
	stackKitName := ctx.StackKit.Metadata.Name
	mode := ctx.Spec.Mode
	subnet := ctx.Spec.Network.Subnet

	if err := validateHCLValue("stackkit name", stackKitName); err != nil {
		return "", err
	}
	if err := validateHCLValue("mode", mode); err != nil {
		return "", err
	}
	if err := validateHCLValue("network subnet", subnet); err != nil {
		return "", err
	}

	const tmplText = `# Generated by stackkit - DO NOT EDIT
# StackKit: {{.Name}}
# Mode: {{.Mode}}

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
  }
}

provider "docker" {
}

resource "docker_network" "stackkit" {
  name = "stackkit-network"

  ipam_config {
    subnet = "{{.Subnet}}"
  }
}
`
	tmpl, err := template.New("main.tf").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse main.tf template: %w", err)
	}

	data := struct {
		Name   string
		Mode   string
		Subnet string
	}{
		Name:   stackKitName,
		Mode:   mode,
		Subnet: subnet,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute main.tf template: %w", err)
	}

	return buf.String(), nil
}
