package cue

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

// GenerateAppsTF writes Docker/OpenTofu resources for user applications declared
// in StackSpec.apps. The first supported app kind is the kombify standard
// SvelteKit app behind Traefik and the login gateway.
func GenerateAppsTF(spec *models.StackSpec, outputDir string) error {
	if spec == nil || len(spec.Apps) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("# Auto-generated user applications from StackSpec.apps\n\n")

	names := make([]string, 0, len(spec.Apps))
	for name := range spec.Apps {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, appName := range names {
		app := normalizedAppSpec(spec, appName, spec.Apps[appName])
		writeAppSecretVariables(&sb, appName, app)
		writeAppResources(&sb, appName, app)
	}

	return writeFile(filepath.Join(outputDir, "apps.tf"), sb.String())
}

func normalizedAppSpec(spec *models.StackSpec, name string, app models.AppSpec) models.AppSpec {
	if app.Kind == "" {
		app.Kind = appKindSvelteKit
	}
	if app.Port == 0 {
		app.Port = 3000
	}
	if app.Health.Path == "" {
		app.Health.Path = "/health"
	}
	if app.Route.Auth == "" {
		app.Route.Auth = routeAuthLogin
	}
	if app.Route.Host == "" {
		domain := spec.Domain
		if domain == "" {
			domain = models.DomainHomeLab
		}
		app.Route.Host = name + "." + domain
	}
	return app
}

func writeAppSecretVariables(sb *strings.Builder, appName string, app models.AppSpec) {
	keys := sortedMapKeys(app.Secrets)
	for _, key := range keys {
		varName := appSecretVarName(appName, key)
		fmt.Fprintf(sb, "variable %q {\n", varName)
		sb.WriteString("  type      = string\n")
		sb.WriteString("  sensitive = true\n")
		fmt.Fprintf(sb, "  description = %q\n", fmt.Sprintf("Secret value for app %s env %s (source ref: %s)", appName, key, app.Secrets[key]))
		sb.WriteString("}\n\n")
	}
}

func writeAppResources(sb *strings.Builder, appName string, app models.AppSpec) {
	tfName := "app_" + tfResourceName(appName)
	containerName := "app-" + appName
	routerName := "app-" + appName

	fmt.Fprintf(sb, "resource \"docker_image\" %q {\n", tfName)
	fmt.Fprintf(sb, "  name = %q\n", app.Image)
	sb.WriteString("}\n\n")

	fmt.Fprintf(sb, "resource \"docker_container\" %q {\n", tfName)
	fmt.Fprintf(sb, "  name     = %q\n", containerName)
	fmt.Fprintf(sb, "  image    = docker_image.%s.image_id\n", tfName)
	sb.WriteString("  restart  = \"unless-stopped\"\n")
	sb.WriteString("  must_run = true\n")

	writeAppEnvironment(sb, appName, app)
	writeAppLabels(sb, appName, routerName, app)
	writeAppNetwork(sb)
	writeAppHealthcheck(sb, app)
	writeAppDependsOn(sb, app)

	sb.WriteString("}\n\n")
}

func writeAppEnvironment(sb *strings.Builder, appName string, app models.AppSpec) {
	envKeys := sortedMapKeys(app.Env)
	secretKeys := sortedMapKeys(app.Secrets)
	if len(envKeys) == 0 && len(secretKeys) == 0 {
		return
	}

	sb.WriteString("\n  env = [\n")
	for _, key := range envKeys {
		fmt.Fprintf(sb, "    %q,\n", hclString(key+"="+app.Env[key]))
	}
	for _, key := range secretKeys {
		fmt.Fprintf(sb, "    %q,\n", hclString(key+"=${var."+appSecretVarName(appName, key)+"}"))
	}
	sb.WriteString("  ]\n")
}

func writeAppLabels(sb *strings.Builder, appName, routerName string, app models.AppSpec) {
	labels := map[string]string{
		"traefik.enable": fmt.Sprintf("%t", true),
		fmt.Sprintf("traefik.http.routers.%s.rule", routerName):                      fmt.Sprintf("Host(`%s`)", app.Route.Host),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", routerName):               "web",
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName): fmt.Sprintf("%d", app.Port),
		"com.kombify.stackkits.app":                                                  appName,
		"com.kombify.stackkits.app.kind":                                             app.Kind,
		"com.kombify.stackkits.app.health":                                           app.Health.Path,
	}
	if app.Route.Auth == routeAuthLogin {
		labels[fmt.Sprintf("traefik.http.routers.%s.middlewares", routerName)] = "tinyauth@docker"
	}

	keys := sortedMapKeys(labels)
	for _, key := range keys {
		sb.WriteString("\n  labels {\n")
		fmt.Fprintf(sb, "    label = %q\n", hclString(key))
		fmt.Fprintf(sb, "    value = %q\n", hclString(labels[key]))
		sb.WriteString("  }\n")
	}
}

func writeAppNetwork(sb *strings.Builder) {
	sb.WriteString("\n  networks_advanced {\n")
	sb.WriteString("    name = docker_network.frontend.id\n")
	sb.WriteString("  }\n")
}

func writeAppHealthcheck(sb *strings.Builder, app models.AppSpec) {
	check := fmt.Sprintf("node -e \"fetch('http://127.0.0.1:%d%s').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))\"", app.Port, app.Health.Path)
	sb.WriteString("\n  healthcheck {\n")
	fmt.Fprintf(sb, "    test         = [\"CMD-SHELL\", %q]\n", hclString(check))
	sb.WriteString("    interval     = \"30s\"\n")
	sb.WriteString("    timeout      = \"5s\"\n")
	sb.WriteString("    retries      = 3\n")
	sb.WriteString("    start_period = \"20s\"\n")
	sb.WriteString("  }\n")
}

func writeAppDependsOn(sb *strings.Builder, app models.AppSpec) {
	sb.WriteString("\n  depends_on = [\n")
	sb.WriteString("    docker_container.traefik,\n")
	if app.Route.Auth == routeAuthLogin {
		sb.WriteString("    docker_container.tinyauth,\n")
	}
	sb.WriteString("  ]\n")
}

func appSecretVarName(appName, key string) string {
	return "app_" + tfResourceName(appName) + "_secret_" + strings.ToLower(tfResourceName(key))
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
