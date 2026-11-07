package architecturev2renderer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"gopkg.in/yaml.v3"
)

const coreDataProfileContractSalt = "|data-profile:hash-bound-full-local,selfhost-oss,optional-readonly-offline-bundle-v1" + fullLocalNetworkContractSalt

const (
	coreProfileAnchor = "      STACKKITS_RUNTIME_PROFILE: production\n"
	coreBundleAnchor  = "        target: /run/stackkit-server\n        read_only: true\n"
)

// renderStackKitServerDataProfile receives only the verified plan selection.
// Standard output remains byte-identical. Apply verifies the source bundle
// before a generated readonly mount can be used.
func renderStackKitServerDataProfile(unit RenderUnit, compose []byte) ([]byte, error) {
	if !unit.DataProfile().FullLocal() {
		return compose, nil
	}
	bundle, hasBundle := unit.OfflineBundle()
	if hasBundle {
		if err := bundle.Validate(); err != nil {
			return nil, err
		}
	}
	environment, volume := stackKitServerDataProfileFragments(bundle, hasBundle)
	if bytes.Count(compose, []byte(coreProfileAnchor)) != 1 ||
		(hasBundle && bytes.Count(compose, []byte(coreBundleAnchor)) != 1) {
		return nil, fail(ErrOutputChanged, "renderer.stackkit-server.dataProfile", "missing unique governed server insertion point")
	}
	compose = bytes.Replace(compose, []byte(coreProfileAnchor), []byte(coreProfileAnchor+environment), 1)
	if hasBundle {
		compose = bytes.Replace(compose, []byte(coreBundleAnchor), []byte(coreBundleAnchor+volume), 1)
	}
	return renderFullLocalCoreNetwork(unit, compose)
}

func stackKitServerDataProfileFragments(bundle runtimeprofile.OfflineBundleRef, hasBundle bool) (string, string) {
	environment := "      KOMBIFY_DATA_PROFILE: full-local\n      KOMBIFY_EDITION: selfhost-oss\n"
	if !hasBundle {
		return environment, ""
	}
	environment += fmt.Sprintf("      STACKKIT_OFFLINE_BUNDLE_PATH: %q\n      STACKKIT_OFFLINE_BUNDLE_MANIFEST_SHA256: %q\n", bundle.Path, bundle.ManifestSHA256)
	volume := fmt.Sprintf("      - type: bind\n        source: %q\n        target: %q\n        read_only: true\n        bind:\n          create_host_path: false\n", bundle.Path, bundle.Path)
	return environment, volume
}

// Strip only the exact governed projection for closed artifact comparison.
// This validates rendering shape; it does not authenticate a bundle's bytes.
func stripStackKitServerDataProfile(content []byte) ([]byte, bool) {
	var networkOK bool
	content, networkOK = stripFullLocalCoreNetwork(content)
	if !networkOK {
		return nil, false
	}
	var document struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(content, &document) != nil {
		return nil, false
	}
	env := document.Services[StackKitServerComponentRef].Environment
	profile, hasProfile := env["KOMBIFY_DATA_PROFILE"]
	_, hasEdition := env["KOMBIFY_EDITION"]
	refPath, hasPath := env["STACKKIT_OFFLINE_BUNDLE_PATH"]
	hash, hasHash := env["STACKKIT_OFFLINE_BUNDLE_MANIFEST_SHA256"]
	if !hasProfile && !hasEdition && !hasPath && !hasHash {
		return content, true
	}
	if profile != "full-local" || env["KOMBIFY_EDITION"] != "selfhost-oss" || hasPath != hasHash {
		return nil, false
	}
	bundle := runtimeprofile.OfflineBundleRef{Path: refPath, ManifestSHA256: hash}
	if hasPath && bundle.Validate() != nil {
		return nil, false
	}
	environment, volume := stackKitServerDataProfileFragments(bundle, hasPath)
	if bytes.Count(content, []byte(coreProfileAnchor+environment)) != 1 {
		return nil, false
	}
	content = bytes.Replace(content, []byte(coreProfileAnchor+environment), []byte(coreProfileAnchor), 1)
	if hasPath {
		if bytes.Count(content, []byte(coreBundleAnchor+volume)) != 1 {
			return nil, false
		}
		content = bytes.Replace(content, []byte(coreBundleAnchor+volume), []byte(coreBundleAnchor), 1)
	}
	return content, true
}

// Every v2 Core runs stackkit-server so the installation's existing router can
// publish the StackKits MCP on the base host (ADR-0044). The pinned image is
// only a runtime base: the executable is the stackkit-server binary of the
// release that applied, which the local executor stages next to compose.yaml.
//
// The route is the base host plus the exact /mcp path. It carries no
// forward-auth middleware because agents cannot complete a browser login; the
// server's dedicated MCP token is the only credential. The router rate limit
// damps brute force per client address before a request reaches the server.
const (
	// StackKitServerComponentRef is the Core component and Compose service.
	StackKitServerComponentRef = "stackkit-server"
	// StackKitServerStagedBinary is the executable the local executor stages
	// in the Compose project directory; Compose mounts it read-only.
	StackKitServerStagedBinary = "stackkit-server"
	// StackKitServerMCPRouter is the Traefik router that publishes /mcp.
	StackKitServerMCPRouter = "stackkit-mcp"
)

const (
	stackKitServerComposeServiceHead = `  stackkit-server:
    image: docker.io/library/alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -300
    mem_limit: 256m
    # Runs as the account that owns the 0700/0600 credentials, so reading
    # them needs no capability; Apply supplies the value.
    user: "${STACKKIT_SERVER_USER:?stackkit-server credentials are missing; run stackkit apply}"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
`
	stackKitServerComposeServiceEnvironmentHead = `    environment:
      STACKKITS_RUNTIME_PROFILE: production
      # Credentials arrive as files, never as environment values that
      # container inspection would expose.
      STACKKITS_API_KEY_FILE: /run/stackkit-server/api-key
      STACKKIT_MCP_TOKEN_FILE: /run/stackkit-server/mcp/token
      # The router's per-client rate limit governs /mcp; behind the router
      # every request shares its address, so a server limit would throttle
      # all agents together.
      STACKKITS_RATE_LIMIT: "0"
      # Tools serve only the configured workspace, never a caller path.
      STACKKIT_MCP_PIN_BASE_DIR: "true"
`
	stackKitServerComposeServiceVolumes = `    volumes:
      - type: bind
        source: ./stackkit-server
        target: /usr/local/bin/stackkit-server
        read_only: true
      - type: bind
        source: ${STACKKIT_CUSTODY_DIR:?}/stackkit-server
        target: /run/stackkit-server
        read_only: true
`
	stackKitServerComposeServiceEnvironment = stackKitServerComposeServiceEnvironmentHead + stackKitServerComposeServiceVolumes +
		"    tmpfs: [/var/lib/stackkit-server, /tmp]\n"
	// The Basement core is the Authority (homelab identity target §4). Its
	// enrollment listener, Access Gate verifier and Hub work on the
	// Authority's verification custody only (StackKits enpy): the device
	// registry, device issuer, Home trust, sessions and app passes the host
	// CLI shares (read-write), and the public Authority record (read-only).
	// The Owner key, the step-ca root, the Pocket ID admin key and the
	// recovery state stay on the host with the identity signer, which the
	// core reaches only through its socket (read-only mount; the signer
	// answers the core server's account only). The applied plan (device
	// enrollment policy) is read-only. Pocket ID is reached on the core
	// network for the Hub's code exchange. The API and MCP keep their empty
	// workspace.
	basementStackKitServerIdentityEnvironment = `      STACKKIT_POCKETID_LOCAL_API: http://pocketid:1411
`
	basementStackKitServerIdentityVolumes = `      - type: bind
        source: ${STACKKIT_CUSTODY_DIR:?}/home-verifier
        target: /var/lib/stackkit-identity/.stackkit/custody/home-verifier
        bind:
          create_host_path: false
      - type: bind
        source: ${STACKKIT_CUSTODY_DIR:?}/home-authority
        target: /var/lib/stackkit-identity/.stackkit/custody/home-authority
        read_only: true
        bind:
          create_host_path: false
      - type: bind
        source: ${STACKKIT_WORKSPACE_DIR:?}/.stackkit/identity-signer
        target: /run/stackkit-identity-signer
        read_only: true
        bind:
          create_host_path: false
      - type: bind
        source: ${STACKKIT_WORKSPACE_DIR:?}/deploy/.stackkit
        target: /var/lib/stackkit-identity/deploy/.stackkit
        read_only: true
        bind:
          create_host_path: false
      - type: bind
        source: ${STACKKIT_CUSTODY_DIR:?}/lan-tls/live
        target: /var/lib/stackkit-identity/.stackkit/custody/lan-tls/live
        read_only: true
        bind:
          create_host_path: false
    tmpfs: [/var/lib/stackkit-server, /var/lib/stackkit-identity, /tmp]
`
	basementStackKitServerComposeServiceEnvironment = stackKitServerComposeServiceEnvironmentHead + basementStackKitServerIdentityEnvironment +
		stackKitServerComposeServiceVolumes + basementStackKitServerIdentityVolumes
	stackKitServerComposeServiceTailHead = `    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8082/health"]
      interval: 5s
      timeout: 3s
      retries: 12
      start_period: 5s
    labels:
      - traefik.enable=true
      - traefik.http.routers.stackkit-mcp.rule=Host(` + "`base.{{STACKKIT_DOMAIN}}`" + `) && Path(` + "`/mcp`" + `)
      - traefik.http.routers.stackkit-mcp.priority=1000
      - traefik.http.routers.stackkit-mcp.entrypoints=websecure
      - traefik.http.routers.stackkit-mcp.tls=true
      - traefik.http.routers.stackkit-mcp.tls.certresolver=stackkits
      - traefik.http.routers.stackkit-mcp.service=stackkit-mcp
`
	stackKitServerComposeServiceTailRest = `      - traefik.http.middlewares.stackkit-mcp-ratelimit.ratelimit.average=5
      - traefik.http.middlewares.stackkit-mcp-ratelimit.ratelimit.burst=20
      - traefik.http.services.stackkit-mcp.loadbalancer.server.port=8082
      # The Access Gate (homelab identity target §7). Each listener calls its
      # own gate path; trustForwardHeader is off, so the gate reads the method,
      # host and URI of the request itself, and every authResponseHeaders name
      # is removed from the request before the gate's value is copied, so
      # neither identity nor listener class can be spoofed.
      - traefik.http.middlewares.stackkit-access-gate-remote.forwardauth.address=http://stackkit-server:8083/v1/gate/remote
      - traefik.http.middlewares.stackkit-access-gate-remote.forwardauth.trustForwardHeader=false
      - traefik.http.middlewares.stackkit-access-gate-remote.forwardauth.authResponseHeaders=` + accessGateResponseHeaders + `
`
	// Only a Home core has a LAN listener: a Cloud core defines no LAN gate,
	// and its gate refuses the LAN path as well.
	stackKitServerComposeServiceTailLAN = `      - traefik.http.middlewares.stackkit-access-gate-lan.forwardauth.address=http://stackkit-server:8083/v1/gate/lan
      - traefik.http.middlewares.stackkit-access-gate-lan.forwardauth.trustForwardHeader=false
      - traefik.http.middlewares.stackkit-access-gate-lan.forwardauth.authResponseHeaders=` + accessGateResponseHeaders + `
      # Relay and tunnel paths end TLS at the edge; the remote listener sets
      # the scheme itself instead of trusting a forwarded one.
      - traefik.http.middlewares.stackkit-remote-scheme.headers.customrequestheaders.X-Forwarded-Proto=https
      # A Cloudflare edge adds its CF-* headers to every tunneled request (and
      # True-Client-IP where enabled); no backend behind the remote listener
      # receives one (the gate answers none, so a match only removes them).
      - traefik.http.middlewares.stackkit-access-gate-remote.forwardauth.authResponseHeadersRegex=^(?i)(cf-|true-client-ip$)
      # The Homelab Hub (the device client PWA) and the device-facing
      # identity routes on the Hub host. They are a sign-in surface: the shell
      # grants nothing and every identity route verifies the device proof or
      # the passkey sign-in itself, so neither TinyAuth nor a device proof
      # stands before them; the gate still enforces the listener and exposure.
      - traefik.http.routers.stackkit-hub.rule=` + "Host(`base.{{STACKKIT_DOMAIN}}`) && (PathPrefix(`/device/`) || PathPrefix(`/api/v1/identity/`))" + `
      - traefik.http.routers.stackkit-hub.priority=900
      - traefik.http.routers.stackkit-hub.entrypoints=websecure
      - traefik.http.routers.stackkit-hub.tls=true
      - traefik.http.routers.stackkit-hub.tls.certresolver=stackkits
      - traefik.http.routers.stackkit-hub.middlewares=stackkit-access-gate-lan@docker
      - traefik.http.routers.stackkit-hub.service=stackkit-hub
      - traefik.http.routers.stackkit-hub-remote.rule=` + "Host(`base.{{STACKKIT_DOMAIN}}`) && (PathPrefix(`/device/`) || PathPrefix(`/api/v1/identity/`))" + `
      - traefik.http.routers.stackkit-hub-remote.priority=900
      - traefik.http.routers.stackkit-hub-remote.entrypoints=remote
      - traefik.http.routers.stackkit-hub-remote.service=stackkit-hub
      - traefik.http.services.stackkit-hub.loadbalancer.server.port=8084
      # The Gate's own paths (the launch exchange and the browser-native
      # session endpoints under /.well-known/stackkit/) on every gated host:
      # one router per host and listener, inserted by the compiler below,
      # reaches the Gate listener whatever routes the host's workloads own.
      - traefik.http.services.stackkit-gate.loadbalancer.server.port=8083
      # stackkit-gate-launch-routers
      # The remote router reads the gate middlewares from this container.
      - io.stackkit.remote-listener=true
`
	// accessGateResponseHeaders is accessgatetable.ResponseHeaders; init proves it.
	accessGateResponseHeaders = "Stackkit-Access-Listener,Stackkit-Access-Subject,Stackkit-Access-Device,Authorization,DPoP,Stackkit-Agent-Envelope,Stackkit-App-Pass,Remote-User,Remote-Sub,Remote-Name,Remote-Email,Remote-Groups"
	// The Basement /mcp router is on the LAN listener, where a native
	// endpoint authenticates itself. On a Cloud Kit the listener is public,
	// so the remote gate is in front of it as of every other route.
	basementStackKitServerComposeServiceTail = stackKitServerComposeServiceTailHead +
		"      - traefik.http.routers.stackkit-mcp.middlewares=stackkit-mcp-ratelimit@docker\n" + stackKitServerComposeServiceTailRest +
		stackKitServerComposeServiceTailLAN
	cloudStackKitServerComposeServiceTail = stackKitServerComposeServiceTailHead +
		"      - traefik.http.routers.stackkit-mcp.middlewares=stackkit-mcp-ratelimit@docker,stackkit-access-gate-remote@docker\n" + stackKitServerComposeServiceTailRest
)

func init() {
	if accessGateResponseHeaders != strings.Join(accessgatetable.ResponseHeaders, ",") {
		panic("architecturev2renderer: the Access Gate middlewares must list accessgatetable.ResponseHeaders")
	}
}

// The Cloud Core serves only the loopback API. The Basement Core adds the Home
// device enrollment listener (catalog listener stackkit-server-home-enroll):
// TLS 1.3 on port 9443, published only on the node site address, which the
// renderer substitutes for 0.0.0.0 from the verified plan. Both run the Access
// Gate on port 8083 inside the core network only; no host port publishes it.
const (
	cloudStackKitServerComposeService = stackKitServerComposeServiceHead +
		`    command: ["/usr/local/bin/stackkit-server", "--port", "8082", "--base-dir", "/var/lib/stackkit-server", "--access-gate-listen", "0.0.0.0:8083"]
` + stackKitServerComposeServiceEnvironment + `    ports: ["127.0.0.1:8082:8082"]
` + cloudStackKitServerComposeServiceTail + "    networks: [cloud-core]\n"
	basementStackKitServerComposeService = stackKitServerComposeServiceHead +
		`    command: ["/usr/local/bin/stackkit-server", "--port", "8082", "--base-dir", "/var/lib/stackkit-server", "--identity-base-dir", "/var/lib/stackkit-identity", "--identity-signer-socket", "/run/stackkit-identity-signer/signer.sock", "--home-enroll-listen", "0.0.0.0:9443", "--access-gate-listen", "0.0.0.0:8083", "--hub-listen", "0.0.0.0:8084"]
` + basementStackKitServerComposeServiceEnvironment + `    ports: ["127.0.0.1:8082:8082", "0.0.0.0:9443:9443"]
` + basementStackKitServerComposeServiceTail + "    networks: [basement-core]\n"
)
