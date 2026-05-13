package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	appAddImage      string
	appAddKind       string
	appAddPort       int
	appAddHost       string
	appAddAuth       string
	appAddHealthPath string
	appAddEnv        []string
	appAddSecrets    []string
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage platform-deployed StackKit applications",
}

var appAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update a SvelteKit app in stack-spec.yaml",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppAdd,
}

type appAddOptions struct {
	Name       string
	Kind       string
	Image      string
	Port       int
	Host       string
	Auth       string
	HealthPath string
	Env        map[string]string
	Secrets    map[string]string
}

func init() {
	appAddCmd.Flags().StringVar(&appAddImage, "image", "", "Container image for the SvelteKit app")
	appAddCmd.Flags().StringVar(&appAddKind, "kind", "sveltekit", "App kind (currently only sveltekit)")
	appAddCmd.Flags().IntVar(&appAddPort, "port", 3000, "Container port exposed by the app")
	appAddCmd.Flags().StringVar(&appAddHost, "host", "", "Route host for the app")
	appAddCmd.Flags().StringVar(&appAddAuth, "auth", "login-gateway", "Route auth mode: login-gateway or public")
	appAddCmd.Flags().StringVar(&appAddHealthPath, "health-path", "/health", "HTTP health path")
	appAddCmd.Flags().StringArrayVar(&appAddEnv, "env", nil, "Plain environment variable as KEY=value; repeatable")
	appAddCmd.Flags().StringArrayVar(&appAddSecrets, "secret", nil, "Secret reference as KEY=env:NAME|doppler:NAME|vault:NAME|file:PATH; repeatable")
	appCmd.AddCommand(appAddCmd)
}

func runAppAdd(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()
	loader := config.NewLoader(wd)
	specPath, _, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("load stack spec: %w", err)
	}
	env, err := parseKeyValueFlags(appAddEnv, "--env")
	if err != nil {
		return err
	}
	secrets, err := parseKeyValueFlags(appAddSecrets, "--secret")
	if err != nil {
		return err
	}

	if err := addAppToSpec(spec, appAddOptions{
		Name:       args[0],
		Kind:       appAddKind,
		Image:      appAddImage,
		Port:       appAddPort,
		Host:       appAddHost,
		Auth:       appAddAuth,
		HealthPath: appAddHealthPath,
		Env:        env,
		Secrets:    secrets,
	}); err != nil {
		return err
	}
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		return fmt.Errorf("save stack spec: %w", err)
	}
	printSuccess("Added app %s to %s", args[0], specPath)
	return nil
}

func addAppToSpec(spec *models.StackSpec, opts appAddOptions) error {
	name := strings.TrimSpace(opts.Name)
	if !isAppDNSLabel(name) {
		return fmt.Errorf("app name must be a DNS-safe label")
	}
	kind := strings.TrimSpace(opts.Kind)
	if kind == "" {
		kind = "sveltekit"
	}
	if kind != "sveltekit" {
		return fmt.Errorf("only kind 'sveltekit' is supported")
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		return fmt.Errorf("image is required")
	}
	port := opts.Port
	if port == 0 {
		port = 3000
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	auth := strings.TrimSpace(opts.Auth)
	if auth == "" {
		auth = "login-gateway"
	}
	if auth != "login-gateway" && auth != "public" {
		return fmt.Errorf("auth must be 'login-gateway' or 'public'")
	}
	healthPath := strings.TrimSpace(opts.HealthPath)
	if healthPath == "" {
		healthPath = "/health"
	}
	if !strings.HasPrefix(healthPath, "/") {
		return fmt.Errorf("health path must start with '/'")
	}
	host := strings.TrimSpace(opts.Host)
	if host != "" && !isAppRouteHost(host) {
		return fmt.Errorf("route host must be a DNS hostname without scheme or path")
	}
	for key := range opts.Env {
		if !isAppEnvName(key) {
			return fmt.Errorf("environment variable %q must match [A-Za-z_][A-Za-z0-9_]*", key)
		}
	}
	for key, ref := range opts.Secrets {
		if !isAppEnvName(key) {
			return fmt.Errorf("secret variable %q must match [A-Za-z_][A-Za-z0-9_]*", key)
		}
		if !isAppSecretRef(ref) {
			return fmt.Errorf("secret reference for %q must start with env:, doppler:, vault:, or file:", key)
		}
	}

	if spec.Apps == nil {
		spec.Apps = map[string]models.AppSpec{}
	}
	spec.Apps[name] = models.AppSpec{
		Kind:  kind,
		Image: image,
		Port:  port,
		Route: models.AppRouteSpec{
			Host: host,
			Auth: auth,
		},
		Health:  models.AppHealthSpec{Path: healthPath},
		Env:     cloneStringMap(opts.Env),
		Secrets: cloneStringMap(opts.Secrets),
	}
	ensureStandardPAASForApps(spec)
	return nil
}

func ensureStandardPAASForApps(spec *models.StackSpec) {
	if spec.PAAS == "" || models.IsStandardPAAS(spec.PAAS) {
		return
	}
	spec.PAAS = models.PAASDokploy
	if spec.Services == nil {
		spec.Services = map[string]any{}
	}
	service, _ := spec.Services["dokploy"].(map[string]any)
	if service == nil {
		service = map[string]any{}
	}
	service["enabled"] = true
	spec.Services["dokploy"] = service
}

func parseKeyValueFlags(values []string, flagName string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%s must use KEY=value", flagName)
		}
		parsed[key] = strings.TrimSpace(val)
	}
	return parsed, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func isAppDNSLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	if value[0] < 'a' || value[0] > 'z' {
		return false
	}
	if value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isAppEnvName(value string) bool {
	if value == "" {
		return false
	}
	first := value[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || first == '_') {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isAppSecretRef(value string) bool {
	for _, prefix := range []string{"env:", "doppler:", "vault:", "file:"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return true
		}
	}
	return false
}

func isAppRouteHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/:\\") {
		return false
	}
	value = strings.TrimSuffix(value, ".")
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if !isAppDNSLabel(label) {
			return false
		}
	}
	return true
}
