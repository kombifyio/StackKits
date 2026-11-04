package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	vpnaddon "github.com/kombifyio/stackkits/addons/vpn"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The tools of the VPN add-on on the Authority's host. WireGuard runs on the
// host's own wireguard-tools (device_overlay_link.go); Headscale, Tailscale
// and Pangolin run the upstream images pinned in addons/vpn/tools.cue as one
// Compose project beside their custody. Every tool's secret stays in the
// add-on's custody and reaches a container only through a 0600 env file.
const (
	vpnComposeProject       = "stackkit-vpn"
	vpnHeadscaleContainer   = "stackkit-vpn-headscale"
	vpnNodeContainer        = "stackkit-vpn-node"
	vpnNewtContainer        = "stackkit-vpn-newt"
	vpnHeadscaleLocal       = "127.0.0.1:18080"
	vpnNodeInterface        = "tailscale0"
	vpnNodeTag              = "tag:stackkit-node"
	vpnDeviceTag            = "tag:stackkit-device"
	vpnHeadscaleUser        = "stackkit"
	vpnPangolinClientPrefix = "stackkit-device-"
	vpnJoinKeyLifetime      = 10 * time.Minute
	vpnToolTimeout          = 60 * time.Second
)

var vpnImagePattern = regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)

// vpnToolImage reads one pinned component image from the embedded tool
// catalog as ref@digest; the tag never selects what runs.
func vpnToolImage(tool, component string) (string, error) {
	value := cuecontext.New().CompileString(vpnaddon.ToolsCUE)
	if value.Err() != nil {
		return "", fmt.Errorf("VPN add-on: the embedded tool catalog does not evaluate: %w", value.Err())
	}
	var pin struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	}
	path := cue.MakePath(cue.Def("Tools"), cue.Str(tool), cue.Str("components"), cue.Str(component), cue.Str("image"))
	if err := value.LookupPath(path).Decode(&pin); err != nil {
		return "", fmt.Errorf("VPN add-on: no pinned image for %s/%s: %w", tool, component, err)
	}
	image := pin.Ref + "@" + pin.Digest
	if !vpnImagePattern.MatchString(image) {
		return "", fmt.Errorf("VPN add-on: the pinned image of %s/%s is not ref@sha256", tool, component)
	}
	return image, nil
}

// vpnDriver is one tool on the Authority's host.
type vpnDriver interface {
	// up starts the tool's runtime and registers the Authority as its node.
	up(ctx context.Context) error
	// down removes every device peer the tool holds and its runtime.
	down(ctx context.Context) error
	// issue creates one device's join material; it runs under the ledger lock
	// and only for an active device. The answer carries the secret once.
	issue(ctx context.Context, deviceID string) (identity.HomeVPNPeer, map[string]any, error)
	// live lists the tool's device nodes.
	live(ctx context.Context) ([]identity.HomeVPNLiveNode, error)
	// remove deletes nodes and join keys; a node or key already gone is no
	// error.
	remove(ctx context.Context, removals identity.HomeVPNRemovals) error
	// control is the tool's control endpoint the peer service carries, or nil.
	control() http.Handler
	// guard (re)applies the tool's confinement from the current ledger.
	guard(ctx context.Context) error
	// probeFrom reports whether a probe from this source came through the
	// tool (for tools whose binding is not by source address).
	probeFrom(ctx context.Context, source netip.Addr) bool
}

// vpnStopRuntime stops the add-on's containers and drops its guard whatever
// else failed, so a tool never keeps running without its opt-in.
func vpnStopRuntime(ctx context.Context, h vpnHost) []error {
	var errs []error
	if _, err := os.Stat(h.composeFile()); err == nil {
		if err := vpnCompose(ctx, h, "down", "--remove-orphans"); err != nil {
			errs = append(errs, fmt.Errorf("stop the containers: %w", err))
		}
	}
	for _, name := range []string{vpnHeadscaleContainer, vpnNodeContainer, vpnNewtContainer} {
		if _, err := homeOverlayRun(ctx, "docker", "inspect", "-f", "{{.Id}}", name); err == nil {
			if _, err := homeOverlayRun(ctx, "docker", "rm", "-f", name); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			}
		}
	}
	if err := removeVPNGuard(ctx); err != nil {
		errs = append(errs, err)
	}
	return errs
}

type vpnHost struct {
	root      string
	selection identity.HomeVPNSelection
	site      netip.Addr
	domain    string
}

func loadVPNHost(root string) (vpnHost, error) {
	selection, err := identity.LoadHomeVPNSelection(root)
	if err != nil {
		return vpnHost{}, err
	}
	return loadVPNHostFor(root, selection)
}

var vpnPlainPath = regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`)

func loadVPNHostFor(root string, selection identity.HomeVPNSelection) (vpnHost, error) {
	// The workspace path lands in Compose, systemd and nftables text; it
	// must be absolute and plain.
	root, err := filepath.Abs(root)
	if err != nil {
		return vpnHost{}, err
	}
	if !vpnPlainPath.MatchString(root) {
		return vpnHost{}, errors.New("VPN add-on: the workspace path must hold only letters, digits, '/', '.', '_' and '-'")
	}
	raw, err := localevidence.BasementLANDNSResolverAddress(root)
	if err != nil {
		return vpnHost{}, fmt.Errorf("VPN add-on: no node site address in custody (apply the StackKit first): %w", err)
	}
	site, err := netip.ParseAddr(raw)
	if err != nil || !site.Is4() {
		return vpnHost{}, errors.New("VPN add-on: the node site address must be IPv4")
	}
	host := vpnHost{root: root, selection: selection, site: site}
	if address, addressErr := localevidence.LocalIdentityRuntimeAddress(root); addressErr == nil {
		host.domain = address.Domain
	}
	return host, nil
}

// localOnly reports a homelab declared local-only, which lab.home is by
// contract (homelab identity target §5).
func (h vpnHost) localOnly() bool { return strings.EqualFold(h.domain, "lab.home") }

func (h vpnHost) dir(parts ...string) string {
	return filepath.Join(append([]string{h.root, filepath.FromSlash(identity.HomeVPNCustodyDir)}, parts...)...)
}

// peerServiceOrigins are the origins a device's proof may name: the node
// site address and, where the Owner declared it, the public address.
func (h vpnHost) peerServiceOrigins() []string {
	origins := []string{"https://" + net.JoinHostPort(h.site.String(), strconv.Itoa(identity.HomeVPNPeerServicePort))}
	if public := h.publicPeerService(); public != "" {
		origins = append(origins, public)
	}
	return origins
}

// publicPeerService is the peer service's public origin: Headscale's control
// endpoint is the peer service itself, WireGuard's peer service shares the
// UDP endpoint's host.
func (h vpnHost) publicPeerService() string {
	host, port, err := net.SplitHostPort(h.selection.Endpoint)
	if err != nil {
		return ""
	}
	switch h.selection.Tool {
	case identity.HomeVPNToolWireGuard:
		port = strconv.Itoa(identity.HomeVPNPeerServicePort)
	case identity.HomeVPNToolHeadscale:
	default:
		return ""
	}
	if port == "443" {
		return "https://" + vpnURLHost(host)
	}
	return "https://" + net.JoinHostPort(host, port)
}

func vpnURLHost(host string) string {
	if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
		return "[" + host + "]"
	}
	return host
}

func (h vpnHost) endpointHost() string {
	host, _, err := net.SplitHostPort(h.selection.Endpoint)
	if err != nil {
		return ""
	}
	return host
}

func (h vpnHost) probeURL() string {
	return "https://" + net.JoinHostPort(h.site.String(), strconv.Itoa(identity.HomeVPNPeerServicePort)) + identity.HomeVPNConfirmPath
}

func (h vpnHost) dnsHint() map[string]any {
	if !h.localOnly() {
		return nil
	}
	return map[string]any{"servers": []string{h.site.String()}, "domains": []string{h.domain}}
}

func newVPNDriver(h vpnHost) (vpnDriver, error) {
	switch h.selection.Tool {
	case identity.HomeVPNToolWireGuard:
		return wireguardVPNDriver{h: h}, nil
	case identity.HomeVPNToolHeadscale:
		return &headscaleVPNDriver{h: h}, nil
	case identity.HomeVPNToolTailscale:
		return &tailscaleVPNDriver{h: h, api: tailscaleAPIBase, client: &http.Client{Timeout: 30 * time.Second}}, nil
	case identity.HomeVPNToolPangolin:
		return &pangolinVPNDriver{h: h, client: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	return nil, fmt.Errorf("VPN add-on: unknown tool %q", h.selection.Tool)
}

// ---- WireGuard --------------------------------------------------------------

type wireguardVPNDriver struct{ h vpnHost }

func (d wireguardVPNDriver) up(ctx context.Context) error {
	_, err := ensureHomeOverlayInterface(ctx, d.h.root, time.Now())
	return err
}

func (d wireguardVPNDriver) down(ctx context.Context) error { return teardownHomeOverlayInterface(ctx) }

func (wireguardVPNDriver) issue(context.Context, string) (identity.HomeVPNPeer, map[string]any, error) {
	return identity.HomeVPNPeer{}, nil, errors.New("VPN add-on: WireGuard peers are bound by their public key")
}
func (wireguardVPNDriver) live(context.Context) ([]identity.HomeVPNLiveNode, error) { return nil, nil }
func (wireguardVPNDriver) remove(context.Context, identity.HomeVPNRemovals) error   { return nil }
func (wireguardVPNDriver) control() http.Handler                                    { return nil }

// guard is applied by every resync of the interface.
func (wireguardVPNDriver) guard(context.Context) error { return nil }

// WireGuard binds by the source address of the device's own peer.
func (wireguardVPNDriver) probeFrom(context.Context, netip.Addr) bool { return false }

// applyVPNNodeGuard confines the Tailscale node's interface: only the bound
// peers of active devices pass beyond the peer service.
func applyVPNNodeGuard(ctx context.Context, root, tool string) error {
	sources, err := identity.HomeVPNBoundAddresses(root, tool, time.Now().UTC())
	if err != nil {
		return err
	}
	return applyVPNGuard(ctx, root, vpnGuardSpec{iface: vpnNodeInterface, restricted: true, sources: sources})
}

// ---- Compose runtime shared by the container tools --------------------------

func (h vpnHost) composeFile() string { return h.dir("compose.yaml") }

func vpnCompose(ctx context.Context, h vpnHost, args ...string) error {
	full := append([]string{"compose", "-p", vpnComposeProject, "-f", h.composeFile()}, args...)
	_, err := homeOverlayRun(ctx, "docker", full...)
	return err
}

// writeVPNPrivateFile writes a 0600 file below the add-on's custody.
func writeVPNPrivateFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".vpn-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(raw)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// envFile renders KEY=value lines; values are checked to one line.
func envFile(values map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var out bytes.Buffer
	for _, key := range keys {
		if strings.ContainsAny(values[key], "\n\r\x00") {
			return nil, fmt.Errorf("VPN add-on: %s holds a line break", key)
		}
		fmt.Fprintf(&out, "%s=%s\n", key, values[key])
	}
	return out.Bytes(), nil
}

// vpnNodeService is the Authority's Tailscale node: kernel networking on the
// standard interface (the host firewall's overlay trust names it), the site
// address as its one route, no DNS takeover.
func vpnNodeService(h vpnHost, image, extraArgs string, caMounted bool) string {
	// The host's netfilter backend decides (nftables on Ubuntu 26.04; the
	// image's legacy iptables cannot initialize there).
	environment := "      TS_STATE_DIR: /var/lib/tailscale\n      TS_USERSPACE: \"false\"\n      TS_ACCEPT_DNS: \"false\"\n      TS_DEBUG_FIREWALL_MODE: auto\n" +
		"      TS_HOSTNAME: stackkit-authority\n      TS_ROUTES: " + h.site.String() + "/32\n" +
		// A fixed port the Owner can forward for direct paths.
		"      TS_TAILSCALED_EXTRA_ARGS: --port=41641\n"
	if extraArgs != "" {
		environment += "      TS_EXTRA_ARGS: \"" + extraArgs + "\"\n"
	}
	volumes := "      - " + h.dir("node", "state") + ":/var/lib/tailscale\n"
	if caMounted {
		environment += "      SSL_CERT_FILE: /etc/stackkit-vpn/ca.pem\n"
		volumes += "      - " + h.dir("ca.pem") + ":/etc/stackkit-vpn/ca.pem:ro\n"
	}
	return "  node:\n    image: " + image + "\n    container_name: " + vpnNodeContainer + "\n    network_mode: host\n    restart: unless-stopped\n" +
		"    cap_add: [NET_ADMIN, NET_RAW]\n    devices: [\"/dev/net/tun:/dev/net/tun\"]\n" +
		"    env_file: [" + h.dir("node.env") + "]\n    environment:\n" + environment + "    volumes:\n" + volumes
}

// requireVPNNodeInterfaceFree refuses a host that already runs its own
// Tailscale: two tailscaled on one host fight over the interface, routing
// table 52 and the ts-input and ts-forward chains.
func requireVPNNodeInterfaceFree(ctx context.Context) error {
	if _, err := homeOverlayRun(ctx, "ip", "link", "show", "dev", vpnNodeInterface); err != nil {
		return nil
	}
	if out, err := homeOverlayRun(ctx, "docker", "inspect", "-f", "{{.State.Running}}", vpnNodeContainer); err == nil && strings.TrimSpace(string(out)) == "true" {
		return nil
	}
	return errors.New("VPN add-on: this host already runs Tailscale (" + vpnNodeInterface + " exists); the Headscale and Tailscale tools need the host's Tailscale node for the add-on alone")
}

// ensureVPNNodeKey writes the node's single-use join key into its env file
// only while the node has no state yet; a registered node never needs it.
func ensureVPNNodeKey(h vpnHost, mint func() (string, error)) error {
	entries, err := os.ReadDir(h.dir("node", "state"))
	if err == nil && len(entries) > 0 {
		return writeVPNPrivateFile(h.dir("node.env"), []byte{})
	}
	if err := os.MkdirAll(h.dir("node", "state"), 0o700); err != nil {
		return err
	}
	key, err := mint()
	if err != nil {
		return err
	}
	env, err := envFile(map[string]string{"TS_AUTHKEY": key})
	if err != nil {
		return err
	}
	return writeVPNPrivateFile(h.dir("node.env"), env)
}

// ---- Headscale --------------------------------------------------------------

type headscaleVPNDriver struct {
	h    vpnHost
	mu   sync.Mutex
	user uint64
}

type headscaleNode struct {
	ID          uint64   `json:"id"`
	IPAddresses []string `json:"ip_addresses"`
	Tags        []string `json:"tags"`
	Online      bool     `json:"online"`
	PreAuthKey  *struct {
		ID uint64 `json:"id"`
	} `json:"pre_auth_key"`
	CreatedAt *struct {
		Seconds int64 `json:"seconds"`
	} `json:"created_at"`
	ApprovedRoutes []string `json:"approved_routes"`
}

func (d *headscaleVPNDriver) headscale(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, vpnToolTimeout)
	defer cancel()
	return homeOverlayRun(ctx, "docker", append([]string{"exec", vpnHeadscaleContainer, "headscale", "-o", "json"}, args...)...)
}

func (d *headscaleVPNDriver) config() string {
	host := d.h.endpointHost()
	derpIPv4 := ""
	if address, err := netip.ParseAddr(host); err == nil && address.Is4() {
		derpIPv4 = "    ipv4: " + address.String() + "\n"
	}
	split := "    split: {}\n"
	if d.h.localOnly() {
		split = "    split:\n      " + d.h.domain + ": [" + d.h.site.String() + "]\n"
	}
	return "# Rendered by stackkit for the VPN add-on (Headscale). Upstream reference: config-example.yaml of the pinned release.\n" +
		"server_url: " + d.h.publicPeerService() + "\n" +
		"listen_addr: 0.0.0.0:8080\nmetrics_listen_addr: 127.0.0.1:9090\ngrpc_listen_addr: 127.0.0.1:50443\ngrpc_allow_insecure: false\n" +
		"noise:\n  private_key_path: /var/lib/headscale/noise_private.key\n" +
		"prefixes:\n  v4: 100.64.0.0/10\n  v6: fd7a:115c:a1e0::/48\n  allocation: sequential\n" +
		"derp:\n  server:\n    enabled: true\n    region_id: 999\n    region_code: stackkit\n    region_name: StackKits Authority\n" +
		"    verify_clients: true\n    stun_listen_addr: 0.0.0.0:3478\n    private_key_path: /var/lib/headscale/derp_server_private.key\n" +
		"    automatically_add_embedded_derp_region: true\n" + derpIPv4 +
		"  urls: []\n  paths: []\n  auto_update_enabled: false\n  update_frequency: 24h\n" +
		"disable_check_updates: true\n" +
		"node:\n  expiry: 0\n  ephemeral:\n    inactivity_timeout: 30m\n" +
		"database:\n  type: sqlite\n  sqlite:\n    path: /var/lib/headscale/db.sqlite\n    write_ahead_log: true\n" +
		"log:\n  level: info\n  format: text\n" +
		"policy:\n  mode: file\n  path: /etc/headscale/policy.hujson\n" +
		"dns:\n  magic_dns: false\n  base_domain: stackkit-vpn.internal\n  override_local_dns: false\n  nameservers:\n    global: []\n" + split +
		"  search_domains: []\n  extra_records: []\n" +
		"unix_socket: /var/run/headscale/headscale.sock\nunix_socket_permission: \"0770\"\n" +
		"logtail:\n  enabled: false\ntaildrop:\n  enabled: false\n"
}

// policy lets devices reach only the node site address. Tags come from the
// join key alone (Headscale ignores advertised tags of a tagged key), and no
// route is approved automatically: up() approves the Authority node's single
// host route by its node ID, so no device can become a router.
func (d *headscaleVPNDriver) policy() string {
	site := d.h.site.String() + "/32"
	return "{\n  \"tagOwners\": {\"" + vpnNodeTag + "\": [\"" + vpnHeadscaleUser + "@\"], \"" + vpnDeviceTag + "\": [\"" + vpnHeadscaleUser + "@\"]},\n" +
		"  \"acls\": [{\"action\": \"accept\", \"src\": [\"" + vpnDeviceTag + "\"], \"dst\": [\"" + site + ":*\"]}]\n}\n"
}

func (d *headscaleVPNDriver) guard(ctx context.Context) error {
	return applyVPNNodeGuard(ctx, d.h.root, identity.HomeVPNToolHeadscale)
}

func (d *headscaleVPNDriver) probeFrom(context.Context, netip.Addr) bool { return false }

func (d *headscaleVPNDriver) up(ctx context.Context) error {
	if d.h.publicPeerService() == "" {
		return errors.New("VPN add-on: Headscale needs --endpoint host:port, the public address of the peer service that carries its control endpoint")
	}
	headscaleImage, err := vpnToolImage(identity.HomeVPNToolHeadscale, "headscale")
	if err != nil {
		return err
	}
	nodeImage, err := vpnToolImage(identity.HomeVPNToolHeadscale, "tailscale-node")
	if err != nil {
		return err
	}
	root, err := localevidence.HomeVPNCARootPEM(d.h.root)
	if err != nil {
		return err
	}
	for path, content := range map[string]string{
		d.h.dir("headscale", "config", "config.yaml"):   d.config(),
		d.h.dir("headscale", "config", "policy.hujson"): d.policy(),
		d.h.dir("ca.pem"): root,
	} {
		if err := writeVPNPrivateFile(path, []byte(content)); err != nil {
			return err
		}
	}
	for _, dir := range []string{d.h.dir("headscale", "data"), d.h.dir("headscale", "run")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if _, err := os.Stat(d.h.dir("node.env")); errors.Is(err, os.ErrNotExist) {
		if err := writeVPNPrivateFile(d.h.dir("node.env"), []byte{}); err != nil {
			return err
		}
	}
	compose := "name: " + vpnComposeProject + "\nservices:\n" +
		"  headscale:\n    image: " + headscaleImage + "\n    container_name: " + vpnHeadscaleContainer + "\n    command: [\"serve\"]\n    restart: unless-stopped\n" +
		"    ports: [\"" + vpnHeadscaleLocal + ":8080\", \"3478:3478/udp\"]\n    volumes:\n" +
		"      - " + d.h.dir("headscale", "config") + ":/etc/headscale:ro\n" +
		"      - " + d.h.dir("headscale", "data") + ":/var/lib/headscale\n" +
		"      - " + d.h.dir("headscale", "run") + ":/var/run/headscale\n" +
		vpnNodeService(d.h, nodeImage, "--login-server=http://"+vpnHeadscaleLocal, true)
	if err := writeVPNPrivateFile(d.h.composeFile(), []byte(compose)); err != nil {
		return err
	}
	if err := vpnCompose(ctx, d.h, "up", "-d", "--remove-orphans", "headscale"); err != nil {
		return fmt.Errorf("VPN add-on: start Headscale: %w", err)
	}
	if err := waitVPN(ctx, "Headscale", func() bool {
		response, err := http.Get("http://" + vpnHeadscaleLocal + "/health") //nolint:noctx // bounded by waitVPN
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}); err != nil {
		return err
	}
	// The policy is read at start; a changed site address restarts it.
	if _, err := homeOverlayRun(ctx, "docker", "restart", vpnHeadscaleContainer); err != nil {
		return err
	}
	if err := waitVPN(ctx, "Headscale", func() bool {
		_, err := d.headscale(ctx, "users", "list")
		return err == nil
	}); err != nil {
		return err
	}
	if err := d.ensureUser(ctx); err != nil {
		return err
	}
	if err := requireVPNNodeInterfaceFree(ctx); err != nil {
		return err
	}
	if err := ensureVPNNodeKey(d.h, func() (string, error) {
		key, err := d.createKey(ctx, vpnNodeTag)
		return key.Key, err
	}); err != nil {
		return err
	}
	if err := vpnCompose(ctx, d.h, "up", "-d", "--remove-orphans"); err != nil {
		return fmt.Errorf("VPN add-on: start the Authority's node: %w", err)
	}
	var node headscaleNode
	if err := waitVPN(ctx, "the Authority's node", func() bool {
		nodes, err := d.nodes(ctx)
		if err != nil {
			return false
		}
		for _, candidate := range nodes {
			if slices.Contains(candidate.Tags, vpnNodeTag) && candidate.Online {
				node = candidate
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	if !slices.Contains(node.ApprovedRoutes, d.h.site.String()+"/32") {
		if _, err := d.headscale(ctx, "nodes", "approve-routes", "-i", strconv.FormatUint(node.ID, 10), "-r", d.h.site.String()+"/32"); err != nil {
			return fmt.Errorf("VPN add-on: approve the site address route: %w", err)
		}
	}
	if err := writeVPNPrivateFile(d.h.dir("node.env"), []byte{}); err != nil {
		return err
	}
	return d.guard(ctx)
}

func (d *headscaleVPNDriver) ensureUser(ctx context.Context) error {
	find := func() bool {
		raw, err := d.headscale(ctx, "users", "list")
		if err != nil {
			return false
		}
		var users []struct {
			ID   uint64 `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &users) != nil {
			return false
		}
		for _, user := range users {
			if user.Name == vpnHeadscaleUser {
				d.mu.Lock()
				d.user = user.ID
				d.mu.Unlock()
				return true
			}
		}
		return false
	}
	if find() {
		return nil
	}
	if _, err := d.headscale(ctx, "users", "create", vpnHeadscaleUser); err != nil {
		return fmt.Errorf("VPN add-on: create the Headscale tag owner: %w", err)
	}
	if !find() {
		return errors.New("VPN add-on: the Headscale tag owner was not created")
	}
	return nil
}

type headscaleKey struct {
	ID  uint64 `json:"id"`
	Key string `json:"key"`
}

func (d *headscaleVPNDriver) createKey(ctx context.Context, tag string) (headscaleKey, error) {
	d.mu.Lock()
	user := d.user
	d.mu.Unlock()
	if user == 0 {
		if err := d.ensureUser(ctx); err != nil {
			return headscaleKey{}, err
		}
		d.mu.Lock()
		user = d.user
		d.mu.Unlock()
	}
	raw, err := d.headscale(ctx, "preauthkeys", "create", "--user", strconv.FormatUint(user, 10), "--tags", tag,
		"--expiration", strconv.Itoa(int(vpnJoinKeyLifetime/time.Minute))+"m")
	if err != nil {
		return headscaleKey{}, fmt.Errorf("VPN add-on: create a Headscale join key: %w", err)
	}
	var key headscaleKey
	if json.Unmarshal(raw, &key) != nil || key.ID == 0 || key.Key == "" {
		return headscaleKey{}, errors.New("VPN add-on: Headscale returned no join key")
	}
	return key, nil
}

func (d *headscaleVPNDriver) nodes(ctx context.Context) ([]headscaleNode, error) {
	raw, err := d.headscale(ctx, "nodes", "list")
	if err != nil {
		return nil, err
	}
	var nodes []headscaleNode
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("VPN add-on: unreadable Headscale node list: %w", err)
	}
	return nodes, nil
}

func (d *headscaleVPNDriver) issue(ctx context.Context, _ string) (identity.HomeVPNPeer, map[string]any, error) {
	key, err := d.createKey(ctx, vpnDeviceTag)
	if err != nil {
		return identity.HomeVPNPeer{}, nil, err
	}
	root, err := localevidence.HomeVPNCARootPEM(d.h.root)
	if err != nil {
		return identity.HomeVPNPeer{}, nil, err
	}
	answer := map[string]any{
		"loginServer": d.h.publicPeerService(), "authKey": key.Key, "authKeyExpiresIn": int(vpnJoinKeyLifetime / time.Second),
		"caRootPem": root, "routes": []string{d.h.site.String() + "/32"}, "acceptRoutes": true,
		"join": "tailscale up --login-server=" + d.h.publicPeerService() + " --auth-key=<authKey> --accept-routes",
	}
	return identity.HomeVPNPeer{KeyRef: strconv.FormatUint(key.ID, 10)}, answer, nil
}

func (d *headscaleVPNDriver) live(ctx context.Context) ([]identity.HomeVPNLiveNode, error) {
	nodes, err := d.nodes(ctx)
	if err != nil {
		return nil, err
	}
	var live []identity.HomeVPNLiveNode
	for _, node := range nodes {
		if !slices.Contains(node.Tags, vpnDeviceTag) {
			continue
		}
		entry := identity.HomeVPNLiveNode{Ref: strconv.FormatUint(node.ID, 10), Addresses: node.IPAddresses}
		if node.PreAuthKey != nil && node.PreAuthKey.ID != 0 {
			entry.KeyRef = strconv.FormatUint(node.PreAuthKey.ID, 10)
		}
		if node.CreatedAt != nil {
			entry.CreatedAt = time.Unix(node.CreatedAt.Seconds, 0).UTC()
		}
		live = append(live, entry)
	}
	return live, nil
}

func (d *headscaleVPNDriver) remove(ctx context.Context, removals identity.HomeVPNRemovals) error {
	var failures []string
	nodeFailed := false
	for _, node := range removals.Nodes {
		if _, err := d.headscale(ctx, "nodes", "delete", "-i", node.Ref, "--force"); err != nil {
			nodeFailed = true
		}
	}
	for _, key := range removals.Keys {
		if _, err := d.headscale(ctx, "preauthkeys", "delete", "--id", key); err != nil && !vpnNotFound(err) {
			failures = append(failures, "key "+key+": "+err.Error())
		}
	}
	if nodeFailed {
		// A node that is gone by now (or never existed) is removed; any
		// other is not.
		live, err := d.live(ctx)
		if err != nil {
			failures = append(failures, "node list: "+err.Error())
		} else {
			for _, node := range removals.Nodes {
				for _, current := range live {
					if current.Ref == node.Ref {
						failures = append(failures, "node "+node.Ref+" is still present")
					}
				}
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func vpnNotFound(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "record not found")
}

// down stops Headscale and the node: the coordination server and every
// device node in it go with its state.
func (d *headscaleVPNDriver) down(ctx context.Context) error {
	if errs := vpnStopRuntime(ctx, d.h); len(errs) > 0 {
		return errors.Join(errs...)
	}
	return os.RemoveAll(d.h.dir())
}

// control carries Headscale's client endpoints only: the Noise upgrade, its
// key, health and the embedded DERP relay. Its admin API, web registration
// and every other route stay unreachable.
func (d *headscaleVPNDriver) control() http.Handler {
	target, _ := url.Parse("http://" + vpnHeadscaleLocal)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	allowed := []string{"/ts2021", "/key", "/health", "/derp", "/derp/probe", "/derp/latency-check", "/bootstrap-dns"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(allowed, r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		r.Header.Del("X-Forwarded-For")
		proxy.ServeHTTP(w, r)
	})
}

// ---- Tailscale --------------------------------------------------------------

const tailscaleAPIBase = "https://api.tailscale.com/api/v2"

type tailscaleVPNDriver struct {
	h      vpnHost
	api    string
	client *http.Client
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (d *tailscaleVPNDriver) guard(ctx context.Context) error {
	return applyVPNNodeGuard(ctx, d.h.root, identity.HomeVPNToolTailscale)
}

func (d *tailscaleVPNDriver) probeFrom(context.Context, netip.Addr) bool { return false }

type tailscaleOAuthClient struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (d *tailscaleVPNDriver) bearer(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Until(d.expiry) > time.Minute {
		return d.token, nil
	}
	raw, err := identity.ReadHomeVPNSecret(d.h.root, "tailscale-oauth-client.json")
	if err != nil {
		return "", errors.New("VPN add-on: Tailscale needs the Owner's OAuth client (stackkit vpn enable --tool tailscale --oauth-client-file)")
	}
	var client tailscaleOAuthClient
	if json.Unmarshal(raw, &client) != nil || client.ClientID == "" || client.ClientSecret == "" {
		return "", errors.New("VPN add-on: the Tailscale OAuth client file needs clientId and clientSecret")
	}
	form := url.Values{"client_id": {client.ClientID}, "client_secret": {client.ClientSecret}, "grant_type": {"client_credentials"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.api+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := d.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("VPN add-on: Tailscale's coordination server is unreachable: %w", err)
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token) != nil || token.AccessToken == "" {
		return "", fmt.Errorf("VPN add-on: Tailscale refused the OAuth client (HTTP %d); the tool stays unavailable", response.StatusCode)
	}
	d.token, d.expiry = token.AccessToken, time.Now().Add(time.Duration(token.ExpiresIn)*time.Second)
	return d.token, nil
}

func (d *tailscaleVPNDriver) call(ctx context.Context, method, path string, body, out any) (int, error) {
	token, err := d.bearer(ctx)
	if err != nil {
		return 0, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.api+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("VPN add-on: Tailscale API %s %s: HTTP %d", method, path, response.StatusCode)
	}
	if out != nil {
		return response.StatusCode, json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out)
	}
	return response.StatusCode, nil
}

func (d *tailscaleVPNDriver) tailnet() string {
	if d.h.selection.Tailscale == nil {
		return "-"
	}
	return url.PathEscape(d.h.selection.Tailscale.Tailnet)
}

type tailscaleKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (d *tailscaleVPNDriver) createKey(ctx context.Context, tag string) (tailscaleKey, error) {
	body := map[string]any{
		"capabilities":  map[string]any{"devices": map[string]any{"create": map[string]any{"reusable": false, "ephemeral": false, "preauthorized": true, "tags": []string{tag}}}},
		"expirySeconds": int(vpnJoinKeyLifetime / time.Second), "description": "stackkit vpn add-on",
	}
	var key tailscaleKey
	if _, err := d.call(ctx, http.MethodPost, "/tailnet/"+d.tailnet()+"/keys", body, &key); err != nil {
		return tailscaleKey{}, err
	}
	if key.ID == "" || key.Key == "" {
		return tailscaleKey{}, errors.New("VPN add-on: Tailscale returned no join key")
	}
	return key, nil
}

type tailscaleDevice struct {
	ID        string    `json:"id"`
	Addresses []string  `json:"addresses"`
	Tags      []string  `json:"tags"`
	Hostname  string    `json:"hostname"`
	Created   time.Time `json:"created"`
}

func (d *tailscaleVPNDriver) devices(ctx context.Context) ([]tailscaleDevice, error) {
	var listing struct {
		Devices []tailscaleDevice `json:"devices"`
	}
	if _, err := d.call(ctx, http.MethodGet, "/tailnet/"+d.tailnet()+"/devices?fields=all", nil, &listing); err != nil {
		return nil, err
	}
	return listing.Devices, nil
}

func (d *tailscaleVPNDriver) up(ctx context.Context) error {
	if _, err := d.bearer(ctx); err != nil {
		return err
	}
	image, err := vpnToolImage(identity.HomeVPNToolTailscale, "tailscale-node")
	if err != nil {
		return err
	}
	if err := requireVPNNodeInterfaceFree(ctx); err != nil {
		return err
	}
	if err := ensureVPNNodeKey(d.h, func() (string, error) {
		key, err := d.createKey(ctx, vpnNodeTag)
		return key.Key, err
	}); err != nil {
		return err
	}
	compose := "name: " + vpnComposeProject + "\nservices:\n" + vpnNodeService(d.h, image, "", false)
	if err := writeVPNPrivateFile(d.h.composeFile(), []byte(compose)); err != nil {
		return err
	}
	if err := vpnCompose(ctx, d.h, "up", "-d", "--remove-orphans"); err != nil {
		return fmt.Errorf("VPN add-on: start the Authority's node: %w", err)
	}
	var node tailscaleDevice
	if err := waitVPN(ctx, "the Authority's node", func() bool {
		devices, err := d.devices(ctx)
		if err != nil {
			return false
		}
		for _, device := range devices {
			if slices.Contains(device.Tags, vpnNodeTag) && device.Hostname == "stackkit-authority" {
				node = device
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	if _, err := d.call(ctx, http.MethodPost, "/device/"+url.PathEscape(node.ID)+"/routes", map[string]any{"routes": []string{d.h.site.String() + "/32"}}, nil); err != nil {
		return fmt.Errorf("VPN add-on: approve the site address route in the tailnet: %w", err)
	}
	if err := writeVPNPrivateFile(d.h.dir("node.env"), []byte{}); err != nil {
		return err
	}
	return d.guard(ctx)
}

func (d *tailscaleVPNDriver) issue(ctx context.Context, _ string) (identity.HomeVPNPeer, map[string]any, error) {
	key, err := d.createKey(ctx, vpnDeviceTag)
	if err != nil {
		return identity.HomeVPNPeer{}, nil, err
	}
	answer := map[string]any{
		"authKey": key.Key, "authKeyExpiresIn": int(vpnJoinKeyLifetime / time.Second), "routes": []string{d.h.site.String() + "/32"}, "acceptRoutes": true,
		"join": "tailscale up --auth-key=<authKey> --accept-routes",
	}
	return identity.HomeVPNPeer{KeyRef: key.ID}, answer, nil
}

func (d *tailscaleVPNDriver) live(ctx context.Context) ([]identity.HomeVPNLiveNode, error) {
	devices, err := d.devices(ctx)
	if err != nil {
		return nil, err
	}
	var live []identity.HomeVPNLiveNode
	for _, device := range devices {
		if slices.Contains(device.Tags, vpnDeviceTag) {
			live = append(live, identity.HomeVPNLiveNode{Ref: device.ID, Addresses: device.Addresses, CreatedAt: device.Created})
		}
	}
	return live, nil
}

func (d *tailscaleVPNDriver) remove(ctx context.Context, removals identity.HomeVPNRemovals) error {
	var failures []string
	for _, node := range removals.Nodes {
		if status, err := d.call(ctx, http.MethodDelete, "/device/"+url.PathEscape(node.Ref), nil, nil); err != nil && status != http.StatusNotFound {
			failures = append(failures, err.Error())
		}
	}
	for _, key := range removals.Keys {
		if status, err := d.call(ctx, http.MethodDelete, "/tailnet/"+d.tailnet()+"/keys/"+url.PathEscape(key), nil, nil); err != nil && status != http.StatusNotFound {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// down stops the node first, whatever the tailnet answers, so no route to
// the site address outlives the opt-in; then it deletes the device nodes and
// the Authority's node in the tailnet. A tailnet that cannot be cleaned (a
// refused or revoked OAuth client) leaves entries that can no longer reach
// the homelab: they are reported for the Owner to delete, and the add-on still
// switches off.
func (d *tailscaleVPNDriver) down(ctx context.Context) error {
	if errs := vpnStopRuntime(ctx, d.h); len(errs) > 0 {
		return errors.Join(errs...)
	}
	var remote []error
	if live, err := d.live(ctx); err != nil {
		remote = append(remote, err)
	} else if err := d.remove(ctx, identity.HomeVPNRemovals{Nodes: live}); err != nil {
		remote = append(remote, err)
	}
	if devices, err := d.devices(ctx); err == nil {
		for _, device := range devices {
			if slices.Contains(device.Tags, vpnNodeTag) && device.Hostname == "stackkit-authority" {
				if status, err := d.call(ctx, http.MethodDelete, "/device/"+url.PathEscape(device.ID), nil, nil); err != nil && status != http.StatusNotFound {
					remote = append(remote, err)
				}
			}
		}
	}
	if len(remote) > 0 {
		return errors.Join(vpnLeftoversError{errors.Join(remote...)}, os.RemoveAll(d.h.dir()))
	}
	return os.RemoveAll(d.h.dir())
}

// vpnLeftoversError reports entries a coordination server still holds after
// the add-on's runtime stopped; they reach nothing, so switching off goes on.
type vpnLeftoversError struct{ err error }

func (e vpnLeftoversError) Error() string {
	return "the runtime is stopped; the coordination server could not be checked or cleaned of add-on entries, which reach nothing now (" + e.err.Error() + ")"
}

func (e vpnLeftoversError) Unwrap() error { return e.err }

func (d *tailscaleVPNDriver) control() http.Handler { return nil }

// ---- Pangolin ---------------------------------------------------------------

type pangolinVPNDriver struct {
	h      vpnHost
	client *http.Client
	once   sync.Once
}

func (d *pangolinVPNDriver) call(ctx context.Context, method, path string, body, out any) (int, error) {
	key, err := identity.ReadHomeVPNSecret(d.h.root, "pangolin-api-key")
	if err != nil {
		return 0, errors.New("VPN add-on: Pangolin needs the Owner's organization API key (stackkit vpn enable --tool pangolin --api-key-file)")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(d.h.selection.Pangolin.API, "/")+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.pangolinClient().Do(request)
	if err != nil {
		return 0, fmt.Errorf("VPN add-on: the Pangolin server is unreachable: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data    json.RawMessage `json:"data"`
		Success bool            `json:"success"`
		Message string          `json:"message"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope)
	if response.StatusCode >= 300 || decodeErr != nil || !envelope.Success {
		return response.StatusCode, fmt.Errorf("VPN add-on: Pangolin API %s %s: HTTP %d %s", method, path, response.StatusCode, envelope.Message)
	}
	if out != nil && len(envelope.Data) > 0 {
		return response.StatusCode, json.Unmarshal(envelope.Data, out)
	}
	return response.StatusCode, nil
}

// pangolinClient trusts the Owner's Pangolin CA when one was given (a
// self-hosted server with a private CA), otherwise the system roots. It is
// built once per driver, so the reconciler reuses its connections.
func (d *pangolinVPNDriver) pangolinClient() *http.Client {
	d.once.Do(func() {
		raw, err := identity.ReadHomeVPNSecret(d.h.root, "pangolin-ca.pem")
		if err != nil {
			return
		}
		if pool, poolErr := x509SystemPoolWith(raw); poolErr == nil {
			d.client = &http.Client{Timeout: d.client.Timeout, Transport: &http.Transport{TLSClientConfig: tlsConfigWithRoots(pool)}}
		}
	})
	return d.client
}

func (d *pangolinVPNDriver) org() string { return url.PathEscape(d.h.selection.Pangolin.OrgID) }

func (d *pangolinVPNDriver) up(ctx context.Context) error {
	image, err := vpnToolImage(identity.HomeVPNToolPangolin, "newt")
	if err != nil {
		return err
	}
	secret, err := identity.ReadHomeVPNSecret(d.h.root, "pangolin-newt-secret")
	if err != nil {
		return errors.New("VPN add-on: Pangolin needs the Newt secret of this Authority's site (--newt-secret-file)")
	}
	values := map[string]string{"NEWT_ID": d.h.selection.Pangolin.NewtID, "NEWT_SECRET": strings.TrimSpace(string(secret)), "PANGOLIN_ENDPOINT": d.h.selection.Pangolin.Endpoint}
	volumes := ""
	if ca, caErr := identity.ReadHomeVPNSecret(d.h.root, "pangolin-ca.pem"); caErr == nil {
		if err := writeVPNPrivateFile(d.h.dir("pangolin-ca.pem"), ca); err != nil {
			return err
		}
		values["SSL_CERT_FILE"] = "/etc/stackkit-vpn/pangolin-ca.pem"
		volumes = "    volumes:\n      - " + d.h.dir("pangolin-ca.pem") + ":/etc/stackkit-vpn/pangolin-ca.pem:ro\n"
	}
	env, err := envFile(values)
	if err != nil {
		return err
	}
	if err := writeVPNPrivateFile(d.h.dir("newt.env"), env); err != nil {
		return err
	}
	// Newt runs on its own bridge, confined by the guard to the site
	// address, its Pangolin server and the internet. A static host entry the
	// Authority has for the server travels with it.
	extraHosts := ""
	for _, entry := range vpnHostsFileEntries(d.endpointHost()) {
		extraHosts += "      - \"" + entry + "\"\n"
	}
	if extraHosts != "" {
		extraHosts = "    extra_hosts:\n" + extraHosts
	}
	compose := "name: " + vpnComposeProject + "\nservices:\n  newt:\n    image: " + image + "\n    container_name: " + vpnNewtContainer +
		"\n    restart: unless-stopped\n    env_file: [" + d.h.dir("newt.env") + "]\n    networks: [newt]\n" + extraHosts + volumes +
		"networks:\n  newt:\n    name: " + vpnComposeProject + "-newt\n    driver: bridge\n    driver_opts:\n      com.docker.network.bridge.name: " + vpnNewtBridge + "\n"
	if err := writeVPNPrivateFile(d.h.composeFile(), []byte(compose)); err != nil {
		return err
	}
	if err := d.guard(ctx); err != nil {
		return err
	}
	if err := vpnCompose(ctx, d.h, "up", "-d", "--remove-orphans"); err != nil {
		return fmt.Errorf("VPN add-on: start Newt: %w", err)
	}
	if id := d.h.selection.Pangolin.ResourceID; id != 0 {
		// The resource follows the site address (also after a LAN re-bind).
		_, err := d.call(ctx, http.MethodPost, "/site-resource/"+strconv.Itoa(id), map[string]any{"destination": d.h.site.String()}, nil)
		return err
	}
	var created struct {
		SiteResourceID int `json:"siteResourceId"`
	}
	body := map[string]any{
		"name": "stackkit-authority", "mode": "host", "siteId": d.h.selection.Pangolin.SiteID, "destination": d.h.site.String(),
		"userIds": []string{}, "roleIds": []int{}, "clientIds": []int{}, "tcpPortRangeString": "*", "udpPortRangeString": "*",
	}
	if _, err := d.call(ctx, http.MethodPut, "/org/"+d.org()+"/site-resource", body, &created); err != nil {
		return err
	}
	if created.SiteResourceID <= 0 {
		return errors.New("VPN add-on: Pangolin returned no site resource for the site address")
	}
	selection := d.h.selection
	pangolin := *selection.Pangolin
	pangolin.ResourceID = created.SiteResourceID
	selection.Pangolin = &pangolin
	if err := identity.SaveHomeVPNSelection(d.h.root, selection); err != nil {
		return err
	}
	d.h.selection = selection
	return nil
}

func (d *pangolinVPNDriver) issue(ctx context.Context, deviceID string) (identity.HomeVPNPeer, map[string]any, error) {
	if d.h.selection.Pangolin.ResourceID <= 0 {
		return identity.HomeVPNPeer{}, nil, errors.New("VPN add-on: Pangolin has no site address resource yet; run stackkit vpn enable again")
	}
	var defaults struct {
		OlmID     string `json:"olmId"`
		OlmSecret string `json:"olmSecret"`
		Subnet    string `json:"subnet"`
	}
	if _, err := d.call(ctx, http.MethodGet, "/org/"+d.org()+"/pick-client-defaults", nil, &defaults); err != nil {
		return identity.HomeVPNPeer{}, nil, err
	}
	if defaults.OlmID == "" || defaults.OlmSecret == "" {
		return identity.HomeVPNPeer{}, nil, errors.New("VPN add-on: Pangolin returned no client credentials")
	}
	var client struct {
		ClientID int `json:"clientId"`
	}
	body := map[string]any{"name": vpnPangolinClientPrefix + deviceID[:16], "olmId": defaults.OlmID, "secret": defaults.OlmSecret, "subnet": defaults.Subnet, "type": "olm"}
	if _, err := d.call(ctx, http.MethodPut, "/org/"+d.org()+"/client", body, &client); err != nil {
		return identity.HomeVPNPeer{}, nil, err
	}
	if client.ClientID <= 0 {
		return identity.HomeVPNPeer{}, nil, errors.New("VPN add-on: Pangolin returned no client")
	}
	ref := strconv.Itoa(client.ClientID)
	if _, err := d.call(ctx, http.MethodPost, "/site-resource/"+strconv.Itoa(d.h.selection.Pangolin.ResourceID)+"/clients/add", map[string]any{"clientId": client.ClientID}, nil); err != nil {
		_, _ = d.call(ctx, http.MethodDelete, "/client/"+ref, nil, nil)
		return identity.HomeVPNPeer{}, nil, err
	}
	answer := map[string]any{
		"endpoint": d.h.selection.Pangolin.Endpoint, "olmId": defaults.OlmID, "secret": defaults.OlmSecret, "resource": d.h.site.String(),
		"join": "olm --id <olmId> --secret <secret> --endpoint " + d.h.selection.Pangolin.Endpoint,
	}
	if ca, err := identity.ReadHomeVPNSecret(d.h.root, "pangolin-ca.pem"); err == nil {
		answer["caPem"] = string(ca)
	}
	return identity.HomeVPNPeer{KeyRef: defaults.OlmID, NodeRef: ref, State: identity.HomeVPNPeerBound}, answer, nil
}

type pangolinClient struct {
	ClientID int    `json:"clientId"`
	Name     string `json:"name"`
	OlmID    string `json:"olmId"`
}

func (d *pangolinVPNDriver) clients(ctx context.Context) ([]pangolinClient, error) {
	var listing struct {
		Clients []pangolinClient `json:"clients"`
	}
	if _, err := d.call(ctx, http.MethodGet, "/org/"+d.org()+"/clients?pageSize=1000", nil, &listing); err != nil {
		return nil, err
	}
	return listing.Clients, nil
}

func (d *pangolinVPNDriver) live(ctx context.Context) ([]identity.HomeVPNLiveNode, error) {
	clients, err := d.clients(ctx)
	if err != nil {
		return nil, err
	}
	var live []identity.HomeVPNLiveNode
	for _, client := range clients {
		if strings.HasPrefix(client.Name, vpnPangolinClientPrefix) {
			// A client is created for one device and bound at once, so one
			// the ledger does not keep goes at once (KeyRef set).
			keyRef := client.OlmID
			if keyRef == "" {
				keyRef = "client-" + strconv.Itoa(client.ClientID)
			}
			live = append(live, identity.HomeVPNLiveNode{Ref: strconv.Itoa(client.ClientID), KeyRef: keyRef})
		}
	}
	return live, nil
}

func (d *pangolinVPNDriver) remove(ctx context.Context, removals identity.HomeVPNRemovals) error {
	var failures []string
	nodes := slices.Clone(removals.Nodes)
	for _, settled := range removals.Settled {
		if settled.NodeRef != "" {
			nodes = append(nodes, identity.HomeVPNLiveNode{Ref: settled.NodeRef})
		}
	}
	seen := map[string]bool{}
	for _, node := range nodes {
		if seen[node.Ref] {
			continue
		}
		seen[node.Ref] = true
		if status, err := d.call(ctx, http.MethodDelete, "/client/"+url.PathEscape(node.Ref), nil, nil); err != nil && status != http.StatusNotFound {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// down stops Newt first, whatever the Pangolin server answers, so no device
// reaches the site address without the opt-in; then it deletes the clients
// and the site resource. Entries the server keeps (it refused the API key)
// reach nothing without Newt: they are reported for the Owner to delete.
func (d *pangolinVPNDriver) down(ctx context.Context) error {
	if errs := vpnStopRuntime(ctx, d.h); len(errs) > 0 {
		return errors.Join(errs...)
	}
	var remote []error
	if live, err := d.live(ctx); err != nil {
		remote = append(remote, err)
	} else if err := d.remove(ctx, identity.HomeVPNRemovals{Nodes: live}); err != nil {
		remote = append(remote, err)
	}
	if id := d.h.selection.Pangolin.ResourceID; id > 0 {
		if status, err := d.call(ctx, http.MethodDelete, "/site-resource/"+strconv.Itoa(id), nil, nil); err != nil && status != http.StatusNotFound {
			remote = append(remote, err)
		}
	}
	if len(remote) > 0 {
		return errors.Join(vpnLeftoversError{errors.Join(remote...)}, os.RemoveAll(d.h.dir()))
	}
	return os.RemoveAll(d.h.dir())
}

func (d *pangolinVPNDriver) control() http.Handler { return nil }

func (d *pangolinVPNDriver) endpointHost() string {
	parsed, err := url.Parse(d.h.selection.Pangolin.Endpoint)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// guard confines Newt's bridge; the Pangolin server's current addresses
// are its one private egress (a self-hosted server on the LAN or a lab).
func (d *pangolinVPNDriver) guard(ctx context.Context) error {
	var egress []string
	lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if addresses, err := net.DefaultResolver.LookupHost(lookup, d.endpointHost()); err == nil {
		for _, raw := range addresses {
			if address, parseErr := netip.ParseAddr(raw); parseErr == nil && address.Is4() && !slices.Contains(egress, address.String()) {
				egress = append(egress, address.String())
			}
		}
	}
	slices.Sort(egress)
	return applyVPNGuard(ctx, d.h.root, vpnGuardSpec{iface: vpnNewtBridge, bridge: true, egress: egress})
}

// probeFrom admits a probe that Newt forwarded: it comes from Newt's own
// address on its bridge.
func (d *pangolinVPNDriver) probeFrom(ctx context.Context, source netip.Addr) bool {
	out, err := homeOverlayRun(ctx, "docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", vpnNewtContainer)
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(string(out)) {
		if address, parseErr := netip.ParseAddr(field); parseErr == nil && address == source {
			return true
		}
	}
	return false
}

// vpnHostsFileEntries are the static /etc/hosts entries of one name, as
// "name:address" for a container's extra_hosts.
func vpnHostsFileEntries(name string) []string {
	if name == "" {
		return nil
	}
	raw, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return nil
	}
	var entries []string
	for _, line := range strings.Split(string(raw), "\n") {
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = line[:index]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		address, err := netip.ParseAddr(fields[0])
		if err != nil || !address.Is4() || address.IsLoopback() {
			continue
		}
		if slices.Contains(fields[1:], name) && !slices.Contains(entries, name+":"+address.String()) {
			entries = append(entries, name+":"+address.String())
		}
	}
	return entries
}

// ---- shared -----------------------------------------------------------------

func waitVPN(ctx context.Context, what string, ready func() bool) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if ready() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("VPN add-on: %s did not become ready within 3 minutes (see docker logs)", what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
