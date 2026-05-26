package cue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

const platformComposeCoolifyEntrypoint = "http"

// GenerateAppsTF writes PaaS handoff manifests for user applications declared
// in StackSpec.apps. StackKit produces the compose/manifest boundary; the
// selected PaaS owns user app deployment and lifecycle.
func GenerateAppsTF(spec *models.StackSpec, outputDir string) error {
	if spec == nil || len(spec.Apps) == 0 {
		return nil
	}

	platform := effectiveAppPlatform(spec)
	if !models.IsSupportedPAAS(platform) {
		return fmt.Errorf("apps require a supported platform adapter, got %q", platform)
	}

	manifestDir := filepath.Join(outputDir, "platform-apps")
	if err := os.MkdirAll(manifestDir, 0750); err != nil {
		return fmt.Errorf("create platform app manifest directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# Auto-generated user application PaaS handoff manifests from StackSpec.apps\n")
	sb.WriteString("# Customer apps are PaaS/Admin handoff metadata. Apps installed outside this manifest are state-unmanaged by StackKit.\n\n")

	names := make([]string, 0, len(spec.Apps))
	for name := range spec.Apps {
		names = append(names, name)
	}
	sort.Strings(names)

	bundle := platformdeploy.BundleManifest{
		Version:  "stackkit.platform-apps/v2",
		Platform: platform,
		Apps:     make([]platformdeploy.AppManifest, 0, len(names)),
	}

	for _, appName := range names {
		app := normalizedAppSpec(spec, appName, spec.Apps[appName])
		writeAppSecretVariables(&sb, appName, app)

		compose := renderPlatformCompose(appName, app, platform)
		composeRelPath := filepath.ToSlash(filepath.Join("platform-apps", appName+".compose.yaml"))
		if err := writeFile(filepath.Join(outputDir, composeRelPath), compose); err != nil {
			return fmt.Errorf("write app compose manifest %s: %w", appName, err)
		}

		bundle.Apps = append(bundle.Apps, platformdeploy.AppManifest{
			Name:        appName,
			Kind:        app.Kind,
			Ownership:   platformdeploy.AppOwnershipCustomer,
			Platform:    platform,
			ManagedBy:   platform,
			Image:       app.Image,
			Port:        app.Port,
			Host:        app.Route.Host,
			URL:         appRouteURL(app.Route.Host),
			Auth:        app.Route.Auth,
			HealthPath:  app.Health.Path,
			ComposePath: composeRelPath,
			ComposeYAML: compose,
			Env:         app.Env,
			Secrets:     app.Secrets,
			SetupPolicy: app.Setup.Policy,
			SetupDrops:  appSetupDropsToManifest(app.Setup.Drops),
		})
		writePlatformComposeResource(&sb, appName, composeRelPath, compose)
	}

	manifestData, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal platform app manifest: %w", err)
	}
	manifest := string(append(manifestData, '\n'))
	manifestRelPath := filepath.ToSlash(filepath.Join("platform-apps", "manifest.json"))
	if err := writeFile(filepath.Join(outputDir, manifestRelPath), manifest); err != nil {
		return fmt.Errorf("write platform app manifest: %w", err)
	}
	writePlatformManifestResource(&sb, manifestRelPath, manifest)

	return writeFile(filepath.Join(outputDir, "apps.tf"), sb.String())
}

func appRouteURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

func effectiveAppPlatform(spec *models.StackSpec) string {
	ctx := models.NodeContext(spec.Context)
	if ctx == "" {
		if spec.HasCustomPublicDomain() {
			ctx = models.ContextCloud
		} else {
			ctx = models.ContextLocal
		}
	}
	return spec.ResolvePAASForContext(ctx)
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
	if app.Setup.Policy == "" {
		app.Setup.Policy = platformdeploy.SetupPolicyManual
	}
	if app.Route.Auth == "" {
		app.Route.Auth = routeAuthLogin
	}
	if app.Route.Host == "" {
		app.Route.Host = models.DefaultAppHost(spec.Domain, spec.SubdomainPrefix, name)
	}
	return app
}

func appSetupDropsToManifest(drops []models.SetupDropSpec) []platformdeploy.SetupDropManifest {
	if len(drops) == 0 {
		return nil
	}
	manifestDrops := make([]platformdeploy.SetupDropManifest, 0, len(drops))
	for _, drop := range drops {
		manifestDrops = append(manifestDrops, platformdeploy.SetupDropManifest{
			Name:        drop.Name,
			Version:     drop.Version,
			Runner:      drop.Runner,
			Description: drop.Description,
			Command:     append([]string(nil), drop.Command...),
			Env:         drop.Env,
			Secrets:     drop.Secrets,
		})
	}
	return manifestDrops
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

func renderPlatformCompose(appName string, app models.AppSpec, platform string) string {
	serviceName := "app-" + appName
	routerName := "app-" + appName
	entrypoint := platformComposeEntrypoint(platform)
	networkName := platformComposeNetwork(platform)

	labels := map[string]string{
		"traefik.enable":         fmt.Sprintf("%t", true),
		"traefik.docker.network": platformComposeNetwork(platform),
		fmt.Sprintf("traefik.http.routers.%s.rule", routerName):                      fmt.Sprintf("Host(`%s`)", app.Route.Host),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", routerName):               entrypoint,
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerName): fmt.Sprintf("%d", app.Port),
		"stackkit.managed-by":              platform,
		"com.kombify.stackkits.app":        appName,
		"com.kombify.stackkits.app.kind":   app.Kind,
		"com.kombify.stackkits.app.health": app.Health.Path,
	}
	if app.Route.Auth == routeAuthLogin {
		middleware := platformComposeAuthMiddleware(platform)
		labels[fmt.Sprintf("traefik.http.routers.%s.middlewares", routerName)] = middleware
		if platform == models.PAASCoolify {
			labels["coolify.traefik.middlewares"] = middleware
		}
	}

	var sb strings.Builder
	sb.WriteString("services:\n")
	fmt.Fprintf(&sb, "  %s:\n", serviceName)
	fmt.Fprintf(&sb, "    image: %s\n", strconv.Quote(app.Image))
	fmt.Fprintf(&sb, "    container_name: %s\n", serviceName)
	sb.WriteString("    restart: unless-stopped\n")
	writeComposeEnvironment(&sb, appName, app)
	writeComposeHealthcheck(&sb, app)
	sb.WriteString("    labels:\n")
	for _, key := range sortedMapKeys(labels) {
		fmt.Fprintf(&sb, "      - %s\n", strconv.Quote(key+"="+labels[key]))
	}
	sb.WriteString("    networks:\n")
	sb.WriteString("      - stackkit\n")
	sb.WriteString("\nnetworks:\n")
	sb.WriteString("  stackkit:\n")
	fmt.Fprintf(&sb, "    name: %s\n", networkName)
	sb.WriteString("    external: true\n")
	return sb.String()
}

func platformComposeAuthMiddleware(platform string) string {
	if platform == models.PAASCoolify {
		return "tinyauth@file"
	}
	return "tinyauth@docker"
}

func platformComposeEntrypoint(platform string) string {
	if platform == models.PAASCoolify {
		return platformComposeCoolifyEntrypoint
	}
	return "web"
}

func platformComposeNetwork(platform string) string {
	switch platform {
	case models.PAASCoolify:
		return "coolify"
	case models.PAASDokploy:
		return "dokploy-network"
	default:
		return "base_net"
	}
}

func writeComposeEnvironment(sb *strings.Builder, appName string, app models.AppSpec) {
	envKeys := sortedMapKeys(app.Env)
	secretKeys := sortedMapKeys(app.Secrets)
	if len(envKeys) == 0 && len(secretKeys) == 0 {
		return
	}

	sb.WriteString("    environment:\n")
	for _, key := range envKeys {
		fmt.Fprintf(sb, "      %s: %s\n", key, strconv.Quote(app.Env[key]))
	}
	for _, key := range secretKeys {
		fmt.Fprintf(sb, "      %s: %s\n", key, strconv.Quote("${"+strings.ToUpper(appSecretVarName(appName, key))+"}"))
	}
}

func writeComposeHealthcheck(sb *strings.Builder, app models.AppSpec) {
	check := fmt.Sprintf("node -e \"fetch('http://127.0.0.1:%d%s').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))\"", app.Port, app.Health.Path)
	sb.WriteString("    healthcheck:\n")
	fmt.Fprintf(sb, "      test: [\"CMD-SHELL\", %s]\n", strconv.Quote(check))
	sb.WriteString("      interval: 30s\n")
	sb.WriteString("      timeout: 5s\n")
	sb.WriteString("      retries: 3\n")
	sb.WriteString("      start_period: 20s\n")
}

func writePlatformComposeResource(sb *strings.Builder, appName, relPath, compose string) {
	tfName := "platform_app_" + tfResourceName(appName) + "_compose"
	fmt.Fprintf(sb, "resource \"local_file\" %q {\n", tfName)
	fmt.Fprintf(sb, "  filename = \"${path.module}/%s\"\n", relPath)
	sb.WriteString("  content  = <<-STACKKIT_COMPOSE\n")
	sb.WriteString(escapeTerraformTemplate(compose))
	sb.WriteString("STACKKIT_COMPOSE\n")
	sb.WriteString("}\n\n")
}

func writePlatformManifestResource(sb *strings.Builder, relPath, manifest string) {
	sb.WriteString("resource \"local_file\" \"platform_apps_manifest\" {\n")
	fmt.Fprintf(sb, "  filename = \"${path.module}/%s\"\n", relPath)
	sb.WriteString("  content  = <<-STACKKIT_MANIFEST\n")
	sb.WriteString(escapeTerraformTemplate(manifest))
	sb.WriteString("STACKKIT_MANIFEST\n")
	sb.WriteString("}\n")
}

func escapeTerraformTemplate(value string) string {
	value = strings.ReplaceAll(value, "${", "$${")
	value = strings.ReplaceAll(value, "%{", "%%{")
	if !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	return value
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
