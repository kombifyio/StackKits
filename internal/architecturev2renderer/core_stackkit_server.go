package architecturev2renderer

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

const stackKitServerComposeServiceBody = `  stackkit-server:
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
    command: ["/usr/local/bin/stackkit-server", "--port", "8082", "--base-dir", "/var/lib/stackkit-server"]
    environment:
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
    ports: ["127.0.0.1:8082:8082"]
    healthcheck:
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

const (
	cloudStackKitServerComposeService    = stackKitServerComposeServiceBody + "    networks: [cloud-core]\n"
	basementStackKitServerComposeService = stackKitServerComposeServiceBody + "    networks: [basement-core]\n"
)
