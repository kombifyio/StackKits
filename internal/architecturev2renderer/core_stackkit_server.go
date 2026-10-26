package architecturev2renderer

import (
	"bytes"
	"fmt"

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
	stackKitServerComposeServiceEnvironment = `    environment:
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
    volumes:
      - type: bind
        source: ./stackkit-server
        target: /usr/local/bin/stackkit-server
        read_only: true
      - type: bind
        source: ${STACKKIT_CUSTODY_DIR:?}/stackkit-server
        target: /run/stackkit-server
        read_only: true
    tmpfs: [/var/lib/stackkit-server, /tmp]
`
	stackKitServerComposeServiceTail = `    healthcheck:
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
      - traefik.http.routers.stackkit-mcp.middlewares=stackkit-mcp-ratelimit@docker
      - traefik.http.middlewares.stackkit-mcp-ratelimit.ratelimit.average=5
      - traefik.http.middlewares.stackkit-mcp-ratelimit.ratelimit.burst=20
      - traefik.http.services.stackkit-mcp.loadbalancer.server.port=8082
`
)

// The Cloud Core serves only the loopback API. The Basement Core adds the Home
// device enrollment listener (catalog listener stackkit-server-home-enroll):
// TLS 1.3 on port 9443, published only on the node site address, which the
// renderer substitutes for 0.0.0.0 from the verified plan.
const (
	cloudStackKitServerComposeService = stackKitServerComposeServiceHead +
		`    command: ["/usr/local/bin/stackkit-server", "--port", "8082", "--base-dir", "/var/lib/stackkit-server"]
` + stackKitServerComposeServiceEnvironment + `    ports: ["127.0.0.1:8082:8082"]
` + stackKitServerComposeServiceTail + "    networks: [cloud-core]\n"
	basementStackKitServerComposeService = stackKitServerComposeServiceHead +
		`    command: ["/usr/local/bin/stackkit-server", "--port", "8082", "--base-dir", "/var/lib/stackkit-server", "--home-enroll-listen", "0.0.0.0:9443"]
` + stackKitServerComposeServiceEnvironment + `    ports: ["127.0.0.1:8082:8082", "0.0.0.0:9443:9443"]
` + stackKitServerComposeServiceTail + "    networks: [basement-core]\n"
)
