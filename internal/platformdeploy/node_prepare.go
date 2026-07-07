package platformdeploy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const komodoPeripherySetupURL = "https://raw.githubusercontent.com/moghtech/komodo/main/scripts/setup-periphery.py"

type SSHRunner interface {
	Run(ctx context.Context, target SSHBootstrap, script string) ([]byte, error)
}

type DefaultSSHRunner struct{}

func (DefaultSSHRunner) Run(ctx context.Context, target SSHBootstrap, script string) ([]byte, error) {
	target = normalizeSSHBootstrap(target)
	keyPath, cleanup, err := materializeNodeSSHKey(target)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if keyPath == "" {
		return nil, fmt.Errorf("supplemental node SSH key path or private key is required")
	}
	args := nodeSSHArgs(target, keyPath)
	args = append(args, "sh", "-c", shellQuote(script))
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- SSH argv is assembled without shell interpolation except a quoted script payload.
	return cmd.CombinedOutput()
}

func PrepareSupplementalNodeTargets(ctx context.Context, platform string, nodes []SupplementalNodeTarget, cfg HTTPConfig, runner SSHRunner) ([]NodePrepareResult, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if runner == nil {
		runner = DefaultSSHRunner{}
	}
	results := []NodePrepareResult{}
	for _, node := range normalizeSupplementalNodeTargets(nodes) {
		result, err := prepareSupplementalNodeTarget(ctx, platform, node, cfg, runner)
		if result.NodeName != "" {
			results = append(results, result)
		}
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func prepareSupplementalNodeTarget(ctx context.Context, platform string, node SupplementalNodeTarget, cfg HTTPConfig, runner SSHRunner) (NodePrepareResult, error) {
	result := NodePrepareResult{
		NodeName: node.Name,
		Role:     node.Role,
		Platform: platform,
		Services: append([]string(nil), node.Services...),
	}
	switch platform {
	case "coolify":
		if hasCoolifyNodePlatformIdentity(node.Platform) {
			result.Status = "observed"
			result.Detail = "coolify node uses existing platform server/destination identifiers"
			stampNodePreparePlatformResult(&result, node.Platform)
			return result, nil
		}
		return prepareCoolifySupplementalNode(ctx, node, cfg, runner, result)
	case "dokploy":
		if strings.TrimSpace(node.Platform.EnvironmentID) != "" {
			result.Status = "observed"
			result.Detail = "dokploy node uses existing environment identifier"
			stampNodePreparePlatformResult(&result, node.Platform)
			return result, nil
		}
		if len(node.Services) > 0 {
			return result, fmt.Errorf("dokploy supplemental node %q requires a real environment ID before services %s can be placed", node.Name, strings.Join(node.Services, ","))
		}
		result.Status = "skipped"
		result.Detail = "dokploy node has no requested services and no environment identifier"
		return result, nil
	case "komodo":
		return prepareKomodoSupplementalNode(ctx, node, cfg, runner, result)
	case "", "none":
		return result, fmt.Errorf("supplemental node %q requires a configured platform", node.Name)
	default:
		return result, fmt.Errorf("supplemental node preparation is not implemented for platform %q", platform)
	}
}

func prepareCoolifySupplementalNode(ctx context.Context, node SupplementalNodeTarget, cfg HTTPConfig, runner SSHRunner, result NodePrepareResult) (NodePrepareResult, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Token) == "" {
		if len(node.Services) > 0 {
			return result, fmt.Errorf("coolify supplemental node %q requires Coolify API base URL and token before services %s can be placed", node.Name, strings.Join(node.Services, ","))
		}
		result.Status = "skipped"
		result.Detail = "coolify node has no requested services and no platform API config"
		return result, nil
	}
	if node.Bootstrap == nil || node.Bootstrap.SSH == nil {
		if len(node.Services) > 0 {
			return result, fmt.Errorf("coolify supplemental node %q requires SSH bootstrap target before services %s can be placed", node.Name, strings.Join(node.Services, ","))
		}
		result.Status = "skipped"
		result.Detail = "coolify node has no requested services and no SSH bootstrap target"
		return result, nil
	}
	ssh := normalizeSSHBootstrap(*node.Bootstrap.SSH)
	if ssh.Host == "" {
		return result, fmt.Errorf("coolify supplemental node %q requires SSH host", node.Name)
	}
	script := coolifyDockerBootstrapScript()
	output, err := runner.Run(ctx, ssh, script)
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return result, fmt.Errorf("coolify supplemental node %q SSH bootstrap failed: %w: %s", node.Name, err, detail)
		}
		return result, fmt.Errorf("coolify supplemental node %q SSH bootstrap failed: %w", node.Name, err)
	}

	keyMaterial, err := coolifyPrivateKeyMaterial(ssh)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(keyMaterial) == "" {
		return result, fmt.Errorf("coolify supplemental node %q requires SSH private key material for Coolify server registration", node.Name)
	}
	client := apiClient{cfg: cfg, authMode: authBearer}
	keyUUID, err := createCoolifyPrivateKey(ctx, client, node.Name, keyMaterial)
	if err != nil {
		return result, err
	}
	serverUUID, err := createCoolifyServer(ctx, client, node, ssh, keyUUID)
	if err != nil {
		return result, err
	}
	if err := validateCoolifyServer(ctx, client, serverUUID); err != nil {
		return result, err
	}
	result.Status = "bootstrapped"
	result.Detail = "coolify server registered and validation started through API"
	result.ServerID = serverUUID
	result.PrivateKeyUUID = keyUUID
	stampNodePreparePlatformResult(&result, nodePlatformTargetWithConfigDefaults(node.Platform, cfg))
	return result, nil
}

func prepareKomodoSupplementalNode(ctx context.Context, node SupplementalNodeTarget, cfg HTTPConfig, runner SSHRunner, result NodePrepareResult) (NodePrepareResult, error) {
	if strings.TrimSpace(node.Platform.ServerID) != "" {
		result.Status = "observed"
		result.Detail = "komodo node uses existing server id"
		stampNodePreparePlatformResult(&result, node.Platform)
		return result, nil
	}
	if node.Bootstrap == nil {
		return result, fmt.Errorf("komodo supplemental node %q requires either a real server_id or bootstrap config", node.Name)
	}
	coreAddress := strings.TrimSpace(node.Bootstrap.KomodoCoreAddress)
	if coreAddress == "" {
		coreAddress = strings.TrimSpace(cfg.BaseURL)
	}
	if coreAddress == "" {
		return result, fmt.Errorf("komodo supplemental node %q requires komodo_core_address", node.Name)
	}
	onboardingKey := strings.TrimSpace(node.Bootstrap.KomodoOnboardingKey)
	if onboardingKey == "" {
		return result, fmt.Errorf("komodo supplemental node %q requires a real onboarding key", node.Name)
	}
	if node.Bootstrap.SSH == nil {
		return result, fmt.Errorf("komodo supplemental node %q requires SSH bootstrap target", node.Name)
	}
	ssh := normalizeSSHBootstrap(*node.Bootstrap.SSH)
	if ssh.Host == "" {
		return result, fmt.Errorf("komodo supplemental node %q requires SSH host", node.Name)
	}
	script := komodoPeripheryBootstrapScript(coreAddress, firstNonEmptyNodeName(node.Name, node.Host, node.IP), onboardingKey)
	output, err := runner.Run(ctx, ssh, script)
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return result, fmt.Errorf("komodo supplemental node %q bootstrap failed: %w: %s", node.Name, err, detail)
		}
		return result, fmt.Errorf("komodo supplemental node %q bootstrap failed: %w", node.Name, err)
	}
	result.Status = "bootstrapped"
	result.Detail = "komodo periphery setup completed through onboarding key"
	return result, nil
}

func normalizeSupplementalNodeTargets(nodes []SupplementalNodeTarget) []SupplementalNodeTarget {
	out := make([]SupplementalNodeTarget, 0, len(nodes))
	for _, node := range nodes {
		node.Name = strings.TrimSpace(node.Name)
		node.Role = normalizeSupplementalNodeRole(node.Role)
		node.IP = strings.TrimSpace(node.IP)
		node.Host = strings.TrimSpace(node.Host)
		node.Services = normalizeNodeServices(node.Services)
		node.Platform.ServerID = strings.TrimSpace(node.Platform.ServerID)
		node.Platform.DestinationUUID = strings.TrimSpace(node.Platform.DestinationUUID)
		node.Platform.EnvironmentID = strings.TrimSpace(node.Platform.EnvironmentID)
		node.Platform.ProjectUUID = strings.TrimSpace(node.Platform.ProjectUUID)
		node.Platform.EnvironmentUUID = strings.TrimSpace(node.Platform.EnvironmentUUID)
		if node.Bootstrap != nil {
			node.Bootstrap.KomodoCoreAddress = strings.TrimSpace(node.Bootstrap.KomodoCoreAddress)
			node.Bootstrap.KomodoOnboardingKey = strings.TrimSpace(node.Bootstrap.KomodoOnboardingKey)
			if node.Bootstrap.SSH != nil {
				ssh := normalizeSSHBootstrap(*node.Bootstrap.SSH)
				node.Bootstrap.SSH = &ssh
			}
		}
		if node.Name == "" {
			node.Name = firstNonEmptyNodeName(node.Host, node.IP, node.Role)
		}
		if isMainNodeRole(node.Role) || node.Name == "" {
			continue
		}
		out = append(out, node)
	}
	return out
}

func normalizeSupplementalNodeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "foundation", "standalone", "main", "control-plane", "control_plane":
		return "main"
	case "storage":
		return "storage"
	case "":
		return "worker"
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

func isMainNodeRole(role string) bool {
	return normalizeSupplementalNodeRole(role) == "main"
}

func normalizeNodeServices(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func hasCoolifyNodePlatformIdentity(target NodePlatformTarget) bool {
	return strings.TrimSpace(target.ServerID) != "" ||
		strings.TrimSpace(target.DestinationUUID) != "" ||
		strings.TrimSpace(target.EnvironmentID) != "" ||
		strings.TrimSpace(target.EnvironmentUUID) != ""
}

func stampNodePreparePlatformResult(result *NodePrepareResult, target NodePlatformTarget) {
	if result == nil {
		return
	}
	if value := strings.TrimSpace(target.ServerID); value != "" {
		result.ServerID = value
	}
	if value := strings.TrimSpace(target.DestinationUUID); value != "" {
		result.DestinationUUID = value
	}
	if value := strings.TrimSpace(target.EnvironmentID); value != "" {
		result.EnvironmentID = value
	}
	if value := strings.TrimSpace(target.ProjectUUID); value != "" {
		result.ProjectUUID = value
	}
	if value := strings.TrimSpace(target.EnvironmentUUID); value != "" {
		result.EnvironmentUUID = value
	}
}

func nodePlatformTargetWithConfigDefaults(target NodePlatformTarget, cfg HTTPConfig) NodePlatformTarget {
	if strings.TrimSpace(target.ServerID) == "" {
		target.ServerID = strings.TrimSpace(cfg.ServerID)
	}
	if strings.TrimSpace(target.DestinationUUID) == "" {
		target.DestinationUUID = strings.TrimSpace(cfg.DestinationUUID)
	}
	if strings.TrimSpace(target.EnvironmentID) == "" {
		target.EnvironmentID = strings.TrimSpace(cfg.EnvironmentID)
	}
	if strings.TrimSpace(target.ProjectUUID) == "" {
		target.ProjectUUID = strings.TrimSpace(cfg.ProjectUUID)
	}
	if strings.TrimSpace(target.EnvironmentUUID) == "" {
		target.EnvironmentUUID = strings.TrimSpace(cfg.EnvironmentUUID)
	}
	return target
}

func coolifyDockerBootstrapScript() string {
	return "set -eu\n" +
		"if ! command -v docker >/dev/null 2>&1; then\n" +
		"  curl -fsSL https://get.docker.com | sh\n" +
		"fi\n" +
		"docker version >/dev/null\n"
}

func coolifyPrivateKeyMaterial(target SSHBootstrap) (string, error) {
	if material := firstNonEmptyNodeName(target.ClientPrivateKey, target.PrivateKey, target.KeyPEM); material != "" {
		return material, nil
	}
	if strings.TrimSpace(target.KeyPath) == "" {
		return "", nil
	}
	data, err := os.ReadFile(target.KeyPath)
	if err != nil {
		return "", fmt.Errorf("read coolify supplemental node SSH key: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func createCoolifyPrivateKey(ctx context.Context, client apiClient, nodeName, privateKey string) (string, error) {
	payload := map[string]any{
		"name":        "stackkit-" + firstNonEmptyNodeName(nodeName, "supplemental-node"),
		"description": "Managed by StackKit supplemental node bootstrap",
		"private_key": privateKey,
	}
	var created map[string]any
	status, body, err := client.postJSON(ctx, "/api/v1/security/keys", payload, &created)
	if err != nil {
		if status == http.StatusConflict {
			if uuid := idFromBody(body); uuid != "" {
				return uuid, nil
			}
		}
		return "", fmt.Errorf("coolify private key create for node %q: %w", nodeName, err)
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		return "", fmt.Errorf("coolify private key create for node %q returned no uuid", nodeName)
	}
	return uuid, nil
}

func createCoolifyServer(ctx context.Context, client apiClient, node SupplementalNodeTarget, ssh SSHBootstrap, privateKeyUUID string) (string, error) {
	host := firstNonEmptyNodeName(ssh.Host, node.Host, node.IP)
	payload := map[string]any{
		"name":             firstNonEmptyNodeName(node.Name, host),
		"description":      "Managed by StackKit supplemental node bootstrap",
		"ip":               host,
		"port":             ssh.Port,
		"user":             ssh.User,
		"private_key_uuid": privateKeyUUID,
		"is_build_server":  false,
		"instant_validate": true,
		"proxy_type":       "traefik",
	}
	var created map[string]any
	status, body, err := client.postJSON(ctx, "/api/v1/servers", payload, &created)
	if err != nil {
		if status == http.StatusConflict {
			if uuid := idFromBody(body); uuid != "" {
				return uuid, nil
			}
		}
		return "", fmt.Errorf("coolify server create for node %q: %w", node.Name, err)
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		return "", fmt.Errorf("coolify server create for node %q returned no uuid", node.Name)
	}
	return uuid, nil
}

func validateCoolifyServer(ctx context.Context, client apiClient, serverUUID string) error {
	if strings.TrimSpace(serverUUID) == "" {
		return fmt.Errorf("coolify server validation requires server uuid")
	}
	var out map[string]any
	if _, _, err := client.getJSON(ctx, "/api/v1/servers/"+url.PathEscape(serverUUID)+"/validate", &out); err != nil {
		return fmt.Errorf("coolify server validate %q: %w", serverUUID, err)
	}
	return nil
}

func komodoPeripheryBootstrapScript(coreAddress, connectAs, onboardingKey string) string {
	return "set -eu\n" +
		"curl -sSL " + shellQuote(komodoPeripherySetupURL) +
		" | python3 - --core-address " + shellQuote(coreAddress) +
		" --connect-as " + shellQuote(connectAs) +
		" --onboarding-key " + shellQuote(onboardingKey) + "\n"
}

func normalizeSSHBootstrap(target SSHBootstrap) SSHBootstrap {
	target.Host = strings.TrimSpace(target.Host)
	target.User = strings.TrimSpace(target.User)
	target.KeyPath = strings.TrimSpace(target.KeyPath)
	target.KeyPEM = strings.TrimSpace(target.KeyPEM)
	target.PrivateKey = strings.TrimSpace(target.PrivateKey)
	target.ClientPrivateKey = strings.TrimSpace(target.ClientPrivateKey)
	target.ProxyJump = strings.TrimSpace(target.ProxyJump)
	if target.User == "" {
		target.User = "root"
	}
	if target.Port <= 0 {
		target.Port = 22
	}
	return target
}

func materializeNodeSSHKey(target SSHBootstrap) (string, func(), error) {
	if target.KeyPath != "" {
		return target.KeyPath, nil, nil
	}
	key := strings.TrimSpace(firstNonEmptyNodeName(target.ClientPrivateKey, target.PrivateKey, target.KeyPEM))
	if key == "" {
		return "", nil, nil
	}
	dir, err := os.MkdirTemp("", "stackkits-node-ssh-")
	if err != nil {
		return "", nil, fmt.Errorf("create supplemental node SSH key dir: %w", err)
	}
	keyPath := filepath.Join(dir, "id_node")
	if err := os.WriteFile(keyPath, []byte(key+"\n"), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("write supplemental node SSH key: %w", err)
	}
	return keyPath, func() { _ = os.RemoveAll(dir) }, nil
}

func nodeSSHArgs(target SSHBootstrap, keyPath string) []string {
	args := []string{
		"-i", keyPath,
		"-p", strconv.Itoa(target.Port),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=20",
	}
	if target.ProxyJump != "" {
		args = append(args, "-J", target.ProxyJump)
	}
	return append(args, target.User+"@"+target.Host)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func firstNonEmptyNodeName(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
