// Package remotereach runs the Cloudflare Tunnel connector of a home server
// (homelab identity target §7, §8, §14 decisions 4 and 10): kombify.me tunnel
// mode (a remotely managed tunnel under the homelab's base, its token sealed to
// this node's local custody or delivered through the kombify Wallet), or a
// tunnel in the Owner's own Cloudflare account (provisioned by Techstack from a
// token in custody, or a tunnel token the Owner supplies in Standard Mode,
// whose ingress stays in the Owner's dashboard).
//
// The connector runs the pinned cloudflared image as its own Compose project,
// joined only to the connector network, where the alias
// stackkit-remote-listener.internal is the remote router's remote entrypoint;
// the Home firewall drops everything that bridge sends to the host or a
// private address. It holds no host port, no capability and no other network.
// Its token stays in Owner custody, readable only by the connector's account,
// and is never printed, logged or written to a receipt.
package remotereach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostsecurity"
)

const (
	// ComposeProject is the connector's Compose project.
	ComposeProject = "stackkit-remote-reach"
	// ConnectorContainer is the connector's container name.
	ConnectorContainer = "stackkit-remote-reach-connector"
	// ConnectorNetwork is the Basement core's connector network.
	ConnectorNetwork = "stackkit-remote-connector"
	// ListenerAlias is the remote listener's name on the connector network.
	ListenerAlias = "stackkit-remote-listener.internal"
	// ListenerService is the ingress every reach tunnel targets.
	ListenerService = "http://" + ListenerAlias + ":8000"

	// The cloudflared image runs as its nonroot account.
	connectorUID = 65532
	connectorGID = 65532

	stateAPIVersion = "stackkit.remote-reach/v1"
	custodyDirName  = "remote-reach"
	tokenMount      = "/run/secrets/stackkit-remote-reach-token"
	maxTokenBytes   = 8 << 10
)

// Modes of the reach tunnel.
const (
	// ModeKombifyMe is kombify.me tunnel mode with the homelab's own Me
	// registration; the token is sealed to this node's local custody.
	ModeKombifyMe = "kombify-me"
	// ModeWalletItem takes the token from a Wallet item delivered to this node
	// (a kombify account's kombify.me tunnel, or a tunnel Techstack
	// provisioned in the Owner's Cloudflare account).
	ModeWalletItem = "wallet-item"
	// ModeOwnerToken runs a tunnel token the Owner supplies (Standard Mode).
	ModeOwnerToken = "owner-token"
)

// tunnelToken is the shape of a Cloudflare Tunnel token: base64 of a JSON
// object, never anything with whitespace or a line break.
var tunnelToken = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)

// State is the public record of the running reach tunnel.
type State struct {
	APIVersion   string `json:"apiVersion"`
	Mode         string `json:"mode"`
	Base         string `json:"base,omitempty"`
	TunnelID     string `json:"tunnelId,omitempty"`
	WalletTenant string `json:"walletTenant,omitempty"`
	WalletItem   string `json:"walletItem,omitempty"`
	EnabledAt    string `json:"enabledAt"`
}

// Runner runs one host command and answers its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Host binds the connector lifecycle to one workspace.
type Host struct {
	Workspace string
	Image     string
	Run       Runner
	Now       func() time.Time
}

func (h Host) dir(parts ...string) string {
	return filepath.Join(append([]string{h.Workspace, ".stackkit", "custody", custodyDirName}, parts...)...)
}

func (h Host) composeFile() string { return h.dir("compose.yaml") }
func (h Host) tokenFile() string   { return h.dir("connector", "token") }
func (h Host) stateFile() string   { return h.dir("state.json") }

// ValidToken reports whether raw has the shape of a tunnel token.
func ValidToken(raw []byte) bool {
	return len(raw) >= 40 && len(raw) <= maxTokenBytes && tunnelToken.Match(raw)
}

// Load reads the running reach tunnel's state; os.ErrNotExist when off.
func (h Host) Load() (State, error) {
	raw, err := os.ReadFile(h.stateFile())
	if err != nil {
		return State{}, err
	}
	var state State
	if json.Unmarshal(raw, &state) != nil || state.APIVersion != stateAPIVersion {
		return State{}, errors.New("remotereach: the reach state in custody is invalid")
	}
	return state, nil
}

// remoteRouterContainer is the Basement core's remote router, the only
// member of the connector network besides the connector itself.
const (
	remoteRouterContainer = "stackkit-basement-core-router-remote-1"
	bridgeName            = hostsecurity.RemoteConnectorBridge
	firewallTable         = hostsecurity.HomeFirewallTable
	// firewallConnectorChain is the forward hook hostsecurity renders for the
	// connector bridge (renderRemoteConnectorChain).
	firewallConnectorChain = "stackkits_home_remote_connector"
)

// RequireConnectorNetwork fails unless the connector can run isolated: the
// applied Basement core's connector network (bridge skremote0, not internal,
// whose only other member is the remote router) and the Home firewall that
// drops everything that bridge sends to the host or a private address
// (homelab identity target §7, §14 decision 10).
func (h Host) RequireConnectorNetwork(ctx context.Context) error {
	out, err := h.Run(ctx, "docker", "network", "inspect", "-f",
		`{{index .Labels "com.docker.compose.project"}}|{{index .Options "com.docker.network.bridge.name"}}|{{.Internal}}|{{range .Containers}}{{.Name}} {{end}}`, ConnectorNetwork)
	if err != nil {
		return fmt.Errorf("remotereach: the connector network %s does not exist; apply the Basement core first", ConnectorNetwork)
	}
	fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 4)
	if len(fields) != 4 || fields[0] != "stackkit-basement-core" || fields[1] != bridgeName || fields[2] != "false" {
		return fmt.Errorf("remotereach: %s is not the Basement core's connector network with outbound reach", ConnectorNetwork)
	}
	for _, member := range strings.Fields(fields[3]) {
		if member != remoteRouterContainer && member != ConnectorContainer {
			return fmt.Errorf("remotereach: container %s joined the connector network; only the remote router and the connector may", member)
		}
	}
	rules, err := h.Run(ctx, "nft", "list", "table", "inet", firewallTable)
	if err != nil {
		return errors.New("remotereach: the Home firewall table is not loaded; run 'stackkit host security repair' before enabling reach")
	}
	text := string(rules)
	if !strings.Contains(text, "chain "+firewallConnectorChain+" {") || !strings.Contains(text, `iifname "`+bridgeName+`" drop`) {
		return errors.New("remotereach: the Home firewall does not isolate the connector bridge from the host and the LAN; run 'stackkit host security repair' first")
	}
	return nil
}

// Install writes the token into custody for the connector's account only,
// writes the connector project and starts it, replacing a running one.
func (h Host) Install(ctx context.Context, state State, token []byte) error {
	if !ValidToken(token) {
		return errors.New("remotereach: the tunnel token does not have the shape of a Cloudflare Tunnel token")
	}
	switch state.Mode {
	case ModeKombifyMe, ModeWalletItem, ModeOwnerToken:
	default:
		return fmt.Errorf("remotereach: unknown reach mode %q", state.Mode)
	}
	if err := h.RequireConnectorNetwork(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(h.dir("connector"), 0o700); err != nil {
		return err
	}
	if err := writeFile(h.tokenFile(), token, 0o400, connectorUID, connectorGID); err != nil {
		return fmt.Errorf("remotereach: store the tunnel token: %w", err)
	}
	if err := writeFile(h.composeFile(), []byte(h.compose()), 0o600, -1, -1); err != nil {
		return err
	}
	if _, err := h.Run(ctx, "docker", "compose", "-p", ComposeProject, "-f", h.composeFile(), "up", "-d", "--force-recreate", "--wait", "--wait-timeout", "120"); err != nil {
		return fmt.Errorf("remotereach: the connector did not become ready: %w", err)
	}
	state.APIVersion = stateAPIVersion
	state.EnabledAt = h.now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFile(h.stateFile(), raw, 0o600, -1, -1)
}

// Remove stops the connector and deletes its token, project and state; a
// connector that is already gone is no error.
func (h Host) Remove(ctx context.Context) error {
	var errs []error
	if _, err := os.Stat(h.composeFile()); err == nil {
		if _, err := h.Run(ctx, "docker", "compose", "-p", ComposeProject, "-f", h.composeFile(), "down", "--remove-orphans"); err != nil {
			errs = append(errs, fmt.Errorf("stop the connector: %w", err))
		}
	}
	if _, err := h.Run(ctx, "docker", "inspect", "-f", "{{.Id}}", ConnectorContainer); err == nil {
		if _, err := h.Run(ctx, "docker", "rm", "-f", ConnectorContainer); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", ConnectorContainer, err))
		}
	}
	if len(errs) != 0 {
		return errors.Join(errs...)
	}
	for _, path := range []string{h.tokenFile(), h.composeFile(), h.stateFile()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Health is the connector container's health as the engine reports it.
func (h Host) Health(ctx context.Context) string {
	out, err := h.Run(ctx, "docker", "inspect", "-f", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", ConnectorContainer)
	if err != nil {
		return "absent"
	}
	return strings.TrimSpace(string(out))
}

func (h Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// compose is the connector project: the pinned image, read-only, without
// capabilities, as the image's nonroot account, on the connector network only,
// resolving through public resolvers (the bridge may not reach a private one),
// the token from its custody file and metrics on its own loopback.
func (h Host) compose() string {
	return "name: " + ComposeProject + "\n" +
		"services:\n" +
		"  connector:\n" +
		"    image: " + h.Image + "\n" +
		"    container_name: " + ConnectorContainer + "\n" +
		"    restart: unless-stopped\n" +
		"    command: [\"tunnel\", \"--no-autoupdate\", \"--metrics\", \"127.0.0.1:20241\", \"run\", \"--token-file\", \"" + tokenMount + "\"]\n" +
		fmt.Sprintf("    user: \"%d:%d\"\n", connectorUID, connectorGID) +
		"    read_only: true\n" +
		"    cap_drop: [ALL]\n" +
		"    security_opt: [\"no-new-privileges:true\"]\n" +
		"    mem_limit: 256m\n" +
		"    logging:\n      driver: json-file\n      options:\n        max-size: \"10m\"\n        max-file: \"3\"\n" +
		"    dns: [\"1.1.1.1\", \"1.0.0.1\", \"2606:4700:4700::1111\"]\n" +
		"    volumes:\n" +
		"      - type: bind\n        source: " + strconv.Quote(h.tokenFile()) + "\n        target: " + tokenMount + "\n        read_only: true\n" +
		"        bind:\n          create_host_path: false\n" +
		"    healthcheck:\n" +
		"      test: [\"CMD\", \"cloudflared\", \"tunnel\", \"--metrics\", \"127.0.0.1:20241\", \"ready\"]\n" +
		"      interval: 10s\n      timeout: 5s\n      retries: 9\n      start_period: 10s\n" +
		"    networks: [remote-connector]\n" +
		"networks:\n" +
		"  remote-connector:\n" +
		"    name: " + ConnectorNetwork + "\n" +
		"    external: true\n"
}

// writeFile writes atomically with mode and, when uid >= 0, owner.
func writeFile(path string, raw []byte, mode os.FileMode, uid, gid int) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".remote-reach-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil && uid >= 0 {
		err = temp.Chown(uid, gid)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
