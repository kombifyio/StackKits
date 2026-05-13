package cue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Generator produces per-module OpenTofu fragments from resolved module contracts.
type Generator struct {
	domain             string
	variables          map[string]any
	dockerMemoryLimits bool
}

// NewGenerator creates a new OpenTofu generator.
func NewGenerator(domain string) *Generator {
	return &Generator{domain: domain, dockerMemoryLimits: true}
}

// NewGeneratorWithVariables creates a generator that also knows the tfvars
// keys available to generated fragments.
func NewGeneratorWithVariables(domain string, variables map[string]any) *Generator {
	copied := make(map[string]any, len(variables))
	for k, v := range variables {
		copied[k] = v
	}
	return &Generator{domain: domain, variables: copied, dockerMemoryLimits: true}
}

// WithDockerMemoryLimits controls whether generated docker_container resources
// include memory and memory_swap fields from module contracts.
func (g *Generator) WithDockerMemoryLimits(enabled bool) *Generator {
	g.dockerMemoryLimits = enabled
	return g
}

// GenerateAll produces OpenTofu files for all modules in the resolved graph.
// Output structure:
//
//	outputDir/
//	  providers.tf      — shared provider config
//	  networks.tf       — shared Docker networks
//	  variables.tf      — all variable declarations
//	  terraform.tfvars.json — variable values
//	  traefik.tf        — per-module resource definitions
//	  tinyauth.tf
//	  ...
func (g *Generator) GenerateAll(graph *ModuleGraph, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Shared infrastructure files
	if err := g.writeProviders(outputDir); err != nil {
		return err
	}
	if err := g.writeNetworks(graph, outputDir); err != nil {
		return err
	}
	if err := g.writeVariables(graph, outputDir); err != nil {
		return err
	}
	if err := g.writeTFVarsJSON(graph, outputDir); err != nil {
		return err
	}

	// Per-module .tf files
	for _, name := range graph.Ordered {
		mc := graph.Modules[name]
		if err := g.writeModuleTF(mc, graph, outputDir); err != nil {
			return fmt.Errorf("failed to generate %s.tf: %w", name, err)
		}
	}

	return nil
}

// writeProviders generates the shared providers.tf file.
func (g *Generator) writeProviders(outputDir string) error {
	content := `terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
  }
}

provider "docker" {
  host = var.docker_host
}
`
	return writeFile(filepath.Join(outputDir, "providers.tf"), content)
}

// writeNetworks generates networks.tf with all required Docker networks.
func (g *Generator) writeNetworks(graph *ModuleGraph, outputDir string) error {
	networks := g.collectNetworks(graph)

	var sb strings.Builder
	sb.WriteString("# Auto-generated Docker networks from module contracts\n\n")

	for _, net := range networks {
		fmt.Fprintf(&sb, "resource \"docker_network\" %q {\n", net)
		fmt.Fprintf(&sb, "  name   = %q\n", net)
		sb.WriteString("  driver = \"bridge\"\n")
		sb.WriteString("}\n\n")
	}

	return writeFile(filepath.Join(outputDir, "networks.tf"), sb.String())
}

// collectNetworks gathers all unique network names from module services.
func (g *Generator) collectNetworks(graph *ModuleGraph) []string {
	seen := make(map[string]bool)
	// Always include the default frontend network
	seen["frontend"] = true

	for _, mc := range graph.Modules {
		for _, svc := range mc.Services {
			for _, port := range svc.Ports {
				_ = port // ports don't define networks, but services do via traefik
			}
		}
		if mc.Requires != nil && mc.Requires.Infrastructure.Network != "" {
			seen[mc.Requires.Infrastructure.Network] = true
		}
	}

	var nets []string
	for net := range seen {
		nets = append(nets, net)
	}
	sortStrings(nets)
	return nets
}

// writeVariables generates variables.tf with declarations for all dynamic values.
func (g *Generator) writeVariables(graph *ModuleGraph, outputDir string) error {
	var sb strings.Builder
	sb.WriteString("# Auto-generated variables from module contracts\n\n")

	// Global variables
	sb.WriteString(`variable "domain" {
  type    = string
  default = "stack.local"
}

variable "docker_host" {
  type    = string
  default = "unix:///var/run/docker.sock"
}

variable "network_name" {
  type    = string
  default = "frontend"
}

`)

	// Per-module enable flags
	declaredVars := map[string]bool{
		"domain":       true,
		"docker_host":  true,
		"network_name": true,
	}
	for _, name := range graph.Ordered {
		varName := moduleVarName(name)
		enableName := "enable_" + varName
		declaredVars[enableName] = true
		fmt.Fprintf(&sb, "variable %q {\n", enableName)
		sb.WriteString("  type    = bool\n")
		sb.WriteString("  default = true\n")
		sb.WriteString("}\n\n")
	}

	extraVars := g.extraVariableNames(graph)
	for _, name := range extraVars {
		if declaredVars[name] {
			continue
		}
		fmt.Fprintf(&sb, "variable %q {\n", name)
		sb.WriteString("  type = any\n")
		sb.WriteString("}\n\n")
	}

	return writeFile(filepath.Join(outputDir, "variables.tf"), sb.String())
}

// writeTFVarsJSON generates terraform.tfvars.json with concrete values.
func (g *Generator) writeTFVarsJSON(graph *ModuleGraph, outputDir string) error {
	vars := make(map[string]any)

	vars["domain"] = g.domain
	if g.domain == "" {
		vars["domain"] = "stack.local"
	}

	if dockerHost := defaultDockerHost(); dockerHost != "" {
		vars["docker_host"] = dockerHost
	}

	// Per-module enable flags
	for _, name := range graph.Ordered {
		mc := graph.Modules[name]
		varName := moduleVarName(name)
		vars["enable_"+varName] = mc.Enabled
	}
	for k, v := range g.variables {
		vars[k] = v
	}

	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tfvars: %w", err)
	}

	return writeFile(filepath.Join(outputDir, "terraform.tfvars.json"), string(data))
}

func defaultDockerHost() string {
	if dockerHost := os.Getenv("DOCKER_HOST"); dockerHost != "" {
		return dockerHost
	}
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return ""
}

// writeModuleTF generates a per-module .tf file with docker_image + docker_container resources.
func (g *Generator) writeModuleTF(mc ModuleContract, graph *ModuleGraph, modulesDir string) error {
	name := mc.Metadata.Name
	if name == "" {
		return fmt.Errorf("module has empty metadata.name")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Auto-generated from module contract: %s\n", name)
	fmt.Fprintf(&sb, "# Layer: %s | Version: %s\n\n", mc.Metadata.Layer, mc.Metadata.Version)

	varName := moduleVarName(name)

	for svcKey, svc := range mc.Services {
		tfName := tfResourceName(svcKey)
		imageRef := svc.Image
		if svc.Tag != "" {
			imageRef = svc.Image + ":" + svc.Tag
		}

		// docker_image resource
		fmt.Fprintf(&sb, "resource \"docker_image\" %q {\n", tfName)
		fmt.Fprintf(&sb, "  count = var.enable_%s ? 1 : 0\n", varName)
		fmt.Fprintf(&sb, "  name  = %q\n", imageRef)
		sb.WriteString("}\n\n")

		// docker_container resource
		fmt.Fprintf(&sb, "resource \"docker_container\" %q {\n", tfName)
		fmt.Fprintf(&sb, "  count = var.enable_%s ? 1 : 0\n", varName)
		fmt.Fprintf(&sb, "  name  = %q\n", svcKey)
		fmt.Fprintf(&sb, "  image = docker_image.%s[0].image_id\n", tfName)

		if len(svc.Command) > 0 {
			sb.WriteString("  command = [")
			for i, part := range svc.Command {
				if i > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "%q", hclString(renderTemplateVariables(part)))
			}
			sb.WriteString("]\n")
		}

		// Restart policy
		restart := svc.RestartPolicy
		if restart == "" {
			restart = "unless-stopped"
		}
		fmt.Fprintf(&sb, "  restart = %q\n", restart)

		// Must run
		sb.WriteString("  must_run = true\n")

		// Ports
		for _, port := range svc.Ports {
			if port.Host > 0 {
				sb.WriteString("\n  ports {\n")
				fmt.Fprintf(&sb, "    internal = %d\n", port.Container)
				fmt.Fprintf(&sb, "    external = %d\n", port.Host)
				sb.WriteString("  }\n")
			}
		}

		// Volumes
		for _, vol := range svc.Volumes {
			sb.WriteString("\n  volumes {\n")
			fmt.Fprintf(&sb, "    container_path = %q\n", vol.Target)
			if vol.Type == "bind" {
				fmt.Fprintf(&sb, "    host_path      = %q\n", vol.Source)
			} else {
				fmt.Fprintf(&sb, "    volume_name    = %q\n", vol.Source)
			}
			if vol.ReadOnly {
				sb.WriteString("    read_only      = true\n")
			}
			sb.WriteString("  }\n")
		}

		// Environment
		if len(svc.Environment) > 0 {
			sb.WriteString("\n  env = [\n")
			envKeys := sortedStringKeys(svc.Environment)
			for _, k := range envKeys {
				v := renderTemplateVariables(svc.Environment[k])
				fmt.Fprintf(&sb, "    %q,\n", hclString(k+"="+v))
			}
			sb.WriteString("  ]\n")
		}

		// Labels (including Traefik routing)
		labels := g.buildLabels(svcKey, svc, mc)
		if len(labels) > 0 {
			labelKeys := sortedStringKeys(labels)
			for _, k := range labelKeys {
				sb.WriteString("\n  labels {\n")
				fmt.Fprintf(&sb, "    label = %q\n", hclString(k))
				fmt.Fprintf(&sb, "    value = %q\n", hclString(labels[k]))
				sb.WriteString("  }\n")
			}
		}

		// Networks — connect to frontend by default
		sb.WriteString("\n  networks_advanced {\n")
		fmt.Fprintf(&sb, "    name = docker_network.%s.id\n", "frontend")
		sb.WriteString("  }\n")

		// Health check
		if svc.HealthCheck != nil && (len(svc.HealthCheck.Test) > 0 || svc.HealthCheck.Path != "") {
			sb.WriteString("\n  healthcheck {\n")
			if len(svc.HealthCheck.Test) > 0 {
				sb.WriteString("    test     = [")
				for i, part := range svc.HealthCheck.Test {
					if i > 0 {
						sb.WriteString(", ")
					}
					fmt.Fprintf(&sb, "%q", hclString(part))
				}
				sb.WriteString("]\n")
			} else if svc.HealthCheck.Path != "" {
				scheme := svc.HealthCheck.Scheme
				if scheme == "" {
					scheme = "http"
				}
				testCmd := fmt.Sprintf("curl -fsS -o /dev/null %s://localhost:%d%s || exit 1",
					scheme, svc.HealthCheck.Port, svc.HealthCheck.Path)
				fmt.Fprintf(&sb, "    test     = [\"CMD-SHELL\", %q]\n", hclString(testCmd))
			}
			fmt.Fprintf(&sb, "    interval = %q\n", svc.HealthCheck.Interval)
			fmt.Fprintf(&sb, "    timeout  = %q\n", svc.HealthCheck.Timeout)
			fmt.Fprintf(&sb, "    retries  = %d\n", svc.HealthCheck.Retries)
			if svc.HealthCheck.StartPeriod != "" {
				fmt.Fprintf(&sb, "    start_period = %q\n", svc.HealthCheck.StartPeriod)
			}
			sb.WriteString("  }\n")
		}

		// Resource limits
		if svc.Resources != nil && g.dockerMemoryLimits {
			if svc.Resources.Memory != "" {
				fmt.Fprintf(&sb, "\n  memory = %d\n", parseMemoryMB(svc.Resources.Memory))
			}
			if svc.Resources.MemoryMax != "" {
				fmt.Fprintf(&sb, "  memory_swap = %d\n", parseMemoryMB(svc.Resources.MemoryMax))
			}
		}

		// Dependencies via depends_on lifecycle
		deps := graph.DependenciesOf(name)
		if len(deps) > 0 {
			sb.WriteString("\n  depends_on = [\n")
			for _, dep := range deps {
				// Reference a container from the dependency module
				depMod := graph.Modules[dep]
				for depSvc := range depMod.Services {
					fmt.Fprintf(&sb, "    docker_container.%s,\n", tfResourceName(depSvc))
					break // one container per dependency is enough for ordering
				}
			}
			sb.WriteString("  ]\n")
		}

		sb.WriteString("}\n\n")
	}

	// Provisioners
	for provName, prov := range mc.Provisioners {
		tfName := tfResourceName(name + "_provisioner_" + provName)
		fmt.Fprintf(&sb, "resource \"docker_image\" %q {\n", tfName)
		fmt.Fprintf(&sb, "  count = var.enable_%s ? 1 : 0\n", varName)
		fmt.Fprintf(&sb, "  name  = %q\n", prov.Image)
		sb.WriteString("}\n\n")

		fmt.Fprintf(&sb, "resource \"docker_container\" %q {\n", tfName)
		fmt.Fprintf(&sb, "  count   = var.enable_%s ? 1 : 0\n", varName)
		fmt.Fprintf(&sb, "  name    = %q\n", name+"-"+provName)
		fmt.Fprintf(&sb, "  image   = docker_image.%s[0].image_id\n", tfName)
		sb.WriteString("  restart = \"no\"\n")
		sb.WriteString("  must_run = false\n")

		if prov.Command != "" {
			fmt.Fprintf(&sb, "  command = [\"sh\", \"-c\", %q]\n", hclString(prov.Command))
		}

		if len(prov.Environment) > 0 {
			sb.WriteString("\n  env = [\n")
			envKeys := sortedStringKeys(prov.Environment)
			for _, k := range envKeys {
				v := renderTemplateVariables(prov.Environment[k])
				fmt.Fprintf(&sb, "    %q,\n", hclString(k+"="+v))
			}
			sb.WriteString("  ]\n")
		}

		sb.WriteString("\n  networks_advanced {\n")
		fmt.Fprintf(&sb, "    name = docker_network.%s.id\n", "frontend")
		sb.WriteString("  }\n")

		if prov.DependsOn != "" {
			depTF := tfResourceName(prov.DependsOn)
			fmt.Fprintf(&sb, "\n  depends_on = [docker_container.%s]\n", depTF)
		}

		sb.WriteString("}\n\n")
	}

	outputPath := filepath.Join(modulesDir, name+".tf")
	return writeFile(outputPath, string(hclwrite.Format([]byte(sb.String()))))
}

// buildLabels creates Traefik routing labels and custom labels for a service.
func (g *Generator) buildLabels(svcKey string, svc ServiceDef, mc ModuleContract) map[string]string {
	labels := make(map[string]string)

	// Copy explicit labels
	for k, v := range svc.Labels {
		v = strings.ReplaceAll(v, "{{.domain}}", "${var.domain}")
		labels[k] = v
	}

	// Traefik routing labels
	if svc.TraefikRule != "" || svc.TraefikPort > 0 {
		labels["traefik.enable"] = "true"

		rule := svc.TraefikRule
		if rule == "" {
			rule = fmt.Sprintf("Host(`%s.${var.domain}`)", svcKey)
		} else {
			rule = renderTemplateVariables(rule)
		}
		labels[fmt.Sprintf("traefik.http.routers.%s.rule", svcKey)] = rule

		if svc.TraefikPort > 0 {
			labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", svcKey)] = fmt.Sprintf("%d", svc.TraefikPort)
		}
	}

	return labels
}

// --- Helpers ---

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}

var templateVarPattern = regexp.MustCompile(`\{\{\.\s*([A-Za-z0-9_]+)\s*\}\}`)

func renderTemplateVariables(value string) string {
	return templateVarPattern.ReplaceAllString(value, "${var.$1}")
}

func hclString(value string) string {
	return strings.ReplaceAll(value, "%{", "%%{")
}

func (g *Generator) extraVariableNames(graph *ModuleGraph) []string {
	names := make(map[string]bool)
	for k := range g.variables {
		names[k] = true
	}
	for _, name := range graph.Ordered {
		mc := graph.Modules[name]
		for _, svc := range mc.Services {
			collectTemplateVars(names, svc.Environment)
			collectStringTemplateVars(names, svc.Labels)
			collectListTemplateVars(names, svc.Command)
			if svc.TraefikRule != "" {
				collectTemplateVarsFromString(names, svc.TraefikRule)
			}
		}
		for _, prov := range mc.Provisioners {
			collectTemplateVars(names, prov.Environment)
		}
	}

	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func collectTemplateVars(names map[string]bool, values map[string]string) {
	for _, value := range values {
		collectTemplateVarsFromString(names, value)
	}
}

func collectStringTemplateVars(names map[string]bool, values map[string]string) {
	collectTemplateVars(names, values)
}

func collectListTemplateVars(names map[string]bool, values []string) {
	for _, value := range values {
		collectTemplateVarsFromString(names, value)
	}
}

func collectTemplateVarsFromString(names map[string]bool, value string) {
	for _, match := range templateVarPattern.FindAllStringSubmatch(value, -1) {
		if len(match) == 2 {
			names[match[1]] = true
		}
	}
}

// moduleVarName converts a module name to a valid OpenTofu variable name part.
func moduleVarName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// tfResourceName converts a service/resource name to a valid OpenTofu resource name.
func tfResourceName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// parseMemoryMB parses memory strings like "128m", "1g" into megabytes.
func parseMemoryMB(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "g") {
		s = strings.TrimSuffix(s, "g")
		var n int
		_, _ = fmt.Sscanf(s, "%d", &n)
		return n * 1024
	}
	if strings.HasSuffix(s, "m") {
		s = strings.TrimSuffix(s, "m")
		var n int
		_, _ = fmt.Sscanf(s, "%d", &n)
		return n
	}
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
