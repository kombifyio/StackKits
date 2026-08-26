package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

const (
	basementCoreModuleID = "stackkits-basement-core-runtime"

	basementCoreComposeUnitID      = "compose"
	basementCoreComposeTemplateRef = "builtin://basement/core/compose/v1.yaml"
	basementCoreComposeOutputRef   = "platform/basement-core/compose.yaml"

	basementCoreOpenTofuUnitID      = "opentofu"
	basementCoreOpenTofuTemplateRef = "builtin://basement/core/opentofu/v1.tf"
	basementCoreOpenTofuOutputRef   = "platform/basement-core/main.tf"

	basementCoreRendererRef = "stackkit"
	basementCoreVersion     = "1.0.0"
)

const basementCoreComposeSchema = `stackkit.basement-core-compose/v1|artifact-revision:17|resolved-network-domain:required|runtime-listeners:catalog-bound|services:router,socket-proxy,pocketid,tinyauth,step-ca,coolify,coolify-postgres,coolify-redis,coolify-realtime,kopia-agent,hub|networks:basement-core-host-reachable,basement-control-internal,basement-backup-internal-no-peer|coolify-control-plane:owner-signed-local-hub-404|coolify-hosts:closed-dual-stack-sinkholes|kopia:idle-owner-command,deterministic-source-hostname,read-only-managed-volume-allowlist,owner-local-repository,isolated-restore-staging,internal-no-peer|hub-endpoints:healthz,verification|healthchecks:container-and-module|credentials:service-scoped-owner-signed-runtime-custody|step-ca:owner-rooted-online-intermediate|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`
const basementCoreOpenTofuSchema = `stackkit.basement-core-opentofu/v1|artifact-revision:17|resolved-network-domain:required|runtime-listeners:catalog-bound|local-file:compose|terraform-data:docker-compose-up-wait|networks:basement-core-host-reachable,basement-control-internal,basement-backup-internal-no-peer|coolify-control-plane:owner-signed-local-hub-404|coolify-hosts:closed-dual-stack-sinkholes|kopia:idle-owner-command,deterministic-source-hostname,read-only-managed-volume-allowlist,owner-local-repository,isolated-restore-staging,internal-no-peer|healthchecks:docker-compose-wait|credentials:service-scoped-owner-signed-runtime-custody|step-ca:owner-rooted-online-intermediate|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`

// basementCoreComponentsJSON is the closed component graph accepted by both
// target-specific renderers. It mirrors the CUE catalog and intentionally
// contains image identities and runtime topology, but no credential material.
const basementCoreComponentsJSON = `[
{"id":"router","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/traefik/traefik:v3","digest":"sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"},"dependsOn":["socket-proxy"],"networkRefs":["basement-core","basement-control"],"health":{"kind":"http","path":"/ping","port":8080},"resources":{"memoryLimit":"256m"}},
{"id":"socket-proxy","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/tecnativa/docker-socket-proxy:v0.4.2","digest":"sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"},"dependsOn":[],"networkRefs":["basement-control"],"health":{"kind":"image"},"resources":{"memoryLimit":"128m"}},
{"id":"pocketid","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/pocket-id/pocket-id:v2.7.0","digest":"sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"},"dependsOn":[],"networkRefs":["basement-core"],"volumes":[{"id":"pocketid-data","target":"/app/data","class":"persistent","backup":true}],"health":{"kind":"http","path":"/health","port":1411},"resources":{"memoryLimit":"512m"}},
{"id":"tinyauth","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/steveiliop56/tinyauth:v5.0.7","digest":"sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"},"dependsOn":["pocketid"],"networkRefs":["basement-core"],"volumes":[{"id":"tinyauth-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["tinyauth","healthcheck"]},"resources":{"memoryLimit":"256m"}},
{"id":"step-ca","role":"application","lifecycle":"daemon","image":{"ref":"smallstep/step-ca:0.30.2","digest":"sha256:a2b17872915c193259b75a5474c398326f41bd199f0842093e52cf4182bc8270"},"dependsOn":[],"networkRefs":["basement-core"],"volumes":[{"id":"step-ca-db","target":"/home/step/db","class":"persistent","backup":true}],"health":{"kind":"http","path":"/health","port":9000},"resources":{"memoryLimit":"256m"}},
{"id":"coolify","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify:4.1.2","digest":"sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"},"dependsOn":["coolify-postgres","coolify-redis","coolify-realtime"],"networkRefs":["basement-core","basement-control"],"environment":{"AUTOUPDATE":"false","CDN_URL":"http://hub/.stackkit/offline/coolify/cdn","VERSIONS_URL":"http://hub/.stackkit/offline/coolify/versions.json","UPGRADE_SCRIPT_URL":"http://hub/.stackkit/offline/coolify/upgrade.sh","RELEASES_URL":"http://hub/.stackkit/offline/coolify/releases.json"},"volumes":[{"id":"coolify-data","target":"/var/www/html/storage","class":"persistent","backup":true},{"id":"coolify-ssh","target":"/var/www/html/storage/app/ssh","class":"persistent","backup":true},{"id":"coolify-applications","target":"/var/www/html/storage/app/applications","class":"persistent","backup":true},{"id":"coolify-databases","target":"/var/www/html/storage/app/databases","class":"persistent","backup":true},{"id":"coolify-services","target":"/var/www/html/storage/app/services","class":"persistent","backup":true},{"id":"coolify-backups","target":"/var/www/html/storage/app/backups","class":"persistent","backup":true}],"health":{"kind":"http","path":"/api/health","port":8080},"resources":{"memoryLimit":"1g"}},
{"id":"coolify-postgres","role":"database","lifecycle":"daemon","image":{"ref":"docker.io/library/postgres:15-alpine","digest":"sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"},"dependsOn":[],"networkRefs":["basement-control"],"volumes":[{"id":"coolify-postgres-data","target":"/var/lib/postgresql/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["pg_isready","-U","coolify"]},"resources":{"memoryLimit":"512m"}},
{"id":"coolify-redis","role":"cache","lifecycle":"daemon","image":{"ref":"docker.io/library/redis:7-alpine","digest":"sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"},"dependsOn":[],"networkRefs":["basement-control"],"volumes":[{"id":"coolify-redis-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["redis-cli","ping"]},"resources":{"memoryLimit":"256m"}},
{"id":"coolify-realtime","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify-realtime:1.0.16","digest":"sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"},"dependsOn":["coolify-redis"],"networkRefs":["basement-control"],"health":{"kind":"http","path":"/ready","port":6001}},
{"id":"kopia-agent","role":"application","lifecycle":"daemon","image":{"ref":"docker.io/kopia/kopia:0.18.2","digest":"sha256:b6cb1f09a5fa832a320ee06d7803e82cdd7f69ac6f61d76a0d55fbbf1495c043"},"dependsOn":[],"networkRefs":["basement-backup"],"volumes":[{"id":"kopia-repository","target":"/app/repository","class":"persistent","backup":false},{"id":"kopia-config","target":"/app/config","class":"persistent","backup":false},{"id":"kopia-cache","target":"/app/cache","class":"cache","backup":false},{"id":"kopia-restore-staging","target":"/restore-staging","class":"persistent","backup":false}],"health":{"kind":"command","command":["kopia","--version"]},"resources":{"memoryLimit":"256m"}},
{"id":"hub","role":"application","lifecycle":"daemon","image":{"ref":"docker.io/library/nginx:alpine","digest":"sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"},"dependsOn":["tinyauth"],"networkRefs":["basement-core"],"health":{"kind":"http","path":"/healthz","port":80},"resources":{"memoryLimit":"256m"}}
]`

const basementCoreCompose = `name: stackkit-basement-core
services:
  socket-proxy:
    image: ghcr.io/tecnativa/docker-socket-proxy:v0.4.2@sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -500
    mem_limit: 128m
    environment:
      CONTAINERS: "1"
      EVENTS: "1"
      INFO: "1"
      NETWORKS: "1"
      PING: "1"
      VERSION: "1"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks: [basement-control]
  router:
    image: ghcr.io/traefik/traefik:v3@sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -800
    mem_limit: 256m
    depends_on: [socket-proxy]
    command:
      - --api.insecure=true
      - --ping=true
      - --providers.docker.endpoint=tcp://socket-proxy:2375
      - --providers.docker.exposedbydefault=false
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
    ports: ["0.0.0.0:80:80", "0.0.0.0:443:443", "127.0.0.1:8080:8080"]
    healthcheck:
      test: ["CMD", "traefik", "healthcheck", "--ping"]
      interval: 5s
      timeout: 3s
      retries: 12
      start_period: 5s
    networks: [basement-core, basement-control]
  pocketid:
    image: ghcr.io/pocket-id/pocket-id:v2.7.0@sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -600
    mem_limit: 512m
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/basement-runtime/pocketid.env"]
    volumes: [pocketid-data:/app/data]
    ports: ["0.0.0.0:1411:1411"]
    healthcheck:
      test: ["CMD", "/app/pocket-id", "healthcheck"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 10s
    labels:
      - traefik.enable=true
      - traefik.http.routers.pocketid.rule=Host(` + "`id.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.pocketid.loadbalancer.server.port=1411
    networks: [basement-core]
  tinyauth:
    image: ghcr.io/steveiliop56/tinyauth:v5.0.7@sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -500
    mem_limit: 256m
    depends_on: [pocketid]
    env_file:
      - path: "${STACKKIT_CUSTODY_DIR:?}/basement-runtime/tinyauth.env"
      - path: "${STACKKIT_CUSTODY_DIR:?}/tinyauth-pocketid/tinyauth.env"
        required: false
    volumes: [tinyauth-data:/data]
    ports: ["0.0.0.0:4000:3000"]
    healthcheck:
      test: ["CMD", "tinyauth", "healthcheck"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 10s
    labels:
      - traefik.enable=true
      - traefik.http.routers.tinyauth.rule=Host(` + "`auth.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.tinyauth.loadbalancer.server.port=3000
    networks: [basement-core]
  step-ca:
    image: smallstep/step-ca:0.30.2@sha256:a2b17872915c193259b75a5474c398326f41bd199f0842093e52cf4182bc8270
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -700
    mem_limit: 256m
    user: "0:0"
    command: ["/usr/local/bin/step-ca", "--password-file", "/home/step/secrets/password", "/home/step/config/ca.json"]
    volumes:
      - ${STACKKIT_CUSTODY_DIR:?}/basement-runtime/step-ca:/home/step:ro
      - step-ca-db:/home/step/db
    ports: ["0.0.0.0:9000:9000"]
    healthcheck:
      test: ["CMD", "step-ca", "version"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 10s
    networks: [basement-core]
  coolify-postgres:
    image: docker.io/library/postgres:15-alpine@sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -700
    mem_limit: 512m
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/basement-runtime/coolify.env"]
    volumes: [coolify-postgres-data:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${DB_USERNAME} -d $${DB_DATABASE:-coolify}"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 10s
    networks: [basement-control]
  coolify-redis:
    image: docker.io/library/redis:7-alpine@sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -400
    mem_limit: 256m
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/basement-runtime/coolify.env"]
    command: ["sh", "-c", "exec redis-server --save 20 1 --loglevel warning --requirepass \"$${REDIS_PASSWORD}\""]
    volumes: [coolify-redis-data:/data]
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a $${REDIS_PASSWORD} ping | grep PONG"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 10s
    networks: [basement-control]
  coolify-realtime:
    image: ghcr.io/coollabsio/coolify-realtime:1.0.16@sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -100
    depends_on: [coolify-redis]
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/basement-runtime/coolify.env"]
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:6001/ready && wget -qO- http://127.0.0.1:6002/ready"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 10s
    networks: [basement-control]
  coolify:
    image: ghcr.io/coollabsio/coolify:4.1.2@sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -100
    mem_limit: 1g
    depends_on:
      coolify-postgres: {condition: service_healthy}
      coolify-redis: {condition: service_healthy}
      coolify-realtime: {condition: service_healthy}
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/basement-runtime/coolify.env"]
    extra_hosts:
      - "raw.githubusercontent.com=127.0.0.1"
      - "raw.githubusercontent.com=[::1]"
      - "undead.coolify.io=127.0.0.1"
      - "undead.coolify.io=[::1]"
      - "ifconfig.io=127.0.0.1"
      - "ifconfig.io=[::1]"
      - "cdn.coollabs.io=127.0.0.1"
      - "cdn.coollabs.io=[::1]"
      - "host.docker.internal=host-gateway"
    volumes:
      - ${STACKKIT_CUSTODY_DIR:?}/basement-runtime/coolify.env:/var/www/html/.env:ro
      - coolify-data:/var/www/html/storage
      - coolify-ssh:/var/www/html/storage/app/ssh
      - coolify-applications:/var/www/html/storage/app/applications
      - coolify-databases:/var/www/html/storage/app/databases
      - coolify-services:/var/www/html/storage/app/services
      - coolify-backups:/var/www/html/storage/app/backups
    ports: ["0.0.0.0:8000:8080"]
    healthcheck:
      test: ["CMD-SHELL", "curl --fail http://127.0.0.1:8080/api/health"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 10s
    labels:
      - traefik.enable=true
      - traefik.http.routers.coolify.rule=Host(` + "`coolify.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.coolify.loadbalancer.server.port=8080
    networks: [basement-core, basement-control]
  kopia-agent:
    image: docker.io/kopia/kopia:0.18.2@sha256:b6cb1f09a5fa832a320ee06d7803e82cdd7f69ac6f61d76a0d55fbbf1495c043
    hostname: ` + localbackuppolicy.Hostname + `
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: 300
    mem_limit: 256m
    entrypoint: ["/bin/sh", "-c"]
    command: ["trap : TERM INT; sleep infinity & wait"]
    volumes:
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-applications/_data:/source/docker-volumes/stackkit-basement-core_coolify-applications/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-backups/_data:/source/docker-volumes/stackkit-basement-core_coolify-backups/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-data/_data:/source/docker-volumes/stackkit-basement-core_coolify-data/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-databases/_data:/source/docker-volumes/stackkit-basement-core_coolify-databases/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-postgres-data/_data:/source/docker-volumes/stackkit-basement-core_coolify-postgres-data/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-redis-data/_data:/source/docker-volumes/stackkit-basement-core_coolify-redis-data/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-services/_data:/source/docker-volumes/stackkit-basement-core_coolify-services/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_coolify-ssh/_data:/source/docker-volumes/stackkit-basement-core_coolify-ssh/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_pocketid-data/_data:/source/docker-volumes/stackkit-basement-core_pocketid-data/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_step-ca-db/_data:/source/docker-volumes/stackkit-basement-core_step-ca-db/_data:ro
      - /var/lib/docker/volumes/stackkit-basement-core_tinyauth-data/_data:/source/docker-volumes/stackkit-basement-core_tinyauth-data/_data:ro
      - kopia-repository:/app/repository
      - kopia-config:/app/config
      - kopia-cache:/app/cache
      - kopia-restore-staging:/restore-staging
    healthcheck:
      test: ["CMD", "kopia", "--version"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 5s
    networks: [basement-backup]
  hub:
    image: docker.io/library/nginx:alpine@sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    oom_score_adj: -300
    mem_limit: 256m
    depends_on: [tinyauth]
    command:
      - /bin/sh
      - -ec
      - |
        printf '%s\n' '<!doctype html><title>StackKit Basement Hub</title><h1>StackKit Basement</h1><ul><li><a href="http://id.{{STACKKIT_DOMAIN}}">PocketID</a></li><li><a href="http://auth.{{STACKKIT_DOMAIN}}">TinyAuth</a></li><li><a href="http://coolify.{{STACKKIT_DOMAIN}}">Coolify</a></li></ul>' > /usr/share/nginx/html/index.html
        printf '%s\n' '{"status":"ok","service":"basement-hub"}' > /usr/share/nginx/html/healthz
        printf '%s\n' '{"apiVersion":"stackkit.service-verification/v1","status":"pending","authority":"stackkit verify"}' > /usr/share/nginx/html/verification
        exec nginx -g 'daemon off;'
    labels:
      - traefik.enable=true
      - traefik.http.routers.hub.rule=PathPrefix(` + "`/`" + `)
      - traefik.http.routers.hub.priority=1
      - traefik.http.services.hub.loadbalancer.server.port=80
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1/healthz | grep '\"status\":\"ok\"'"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 5s
    networks: [basement-core]
networks:
  basement-core:
    name: stackkit-basement-core
  basement-control:
    name: stackkit-basement-control
    internal: true
  basement-backup:
    name: stackkit-basement-backup
    internal: true
volumes:
  pocketid-data: {}
  tinyauth-data: {}
  step-ca-db: {}
  coolify-data: {}
  coolify-ssh: {}
  coolify-applications: {}
  coolify-databases: {}
  coolify-services: {}
  coolify-backups: {}
  coolify-postgres-data: {}
  coolify-redis-data: {}
  kopia-repository: {}
  kopia-config: {}
  kopia-cache: {}
  kopia-restore-staging: {}
`

type basementCoreRenderer struct {
	contract  RendererContract
	unitID    string
	outputRef string
	render    func(RenderUnit) []byte
}

type closedLocalCoreEndpoint struct {
	port      int
	healthRef string
}

type closedLocalCoreProfile struct {
	displayName      string
	moduleID         string
	runtimeEngine    string
	imageRef         string
	imageDigest      string
	entryComponent   string
	componentsJSON   string
	serviceEndpoints map[string]closedLocalCoreEndpoint
	renderCompose    func(string) []byte
}

func basementClosedLocalCoreProfile() closedLocalCoreProfile {
	return closedLocalCoreProfile{
		displayName: "Basement core", moduleID: basementCoreModuleID, runtimeEngine: "docker",
		imageRef:       "ghcr.io/coollabsio/coolify:4.1.2",
		imageDigest:    "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f",
		entryComponent: "coolify", componentsJSON: basementCoreComponentsJSON,
		serviceEndpoints: map[string]closedLocalCoreEndpoint{
			"basement-hub": {port: 80, healthRef: "basement-hub-http"},
			"id":           {port: 1411, healthRef: "pocketid-http"},
			"auth":         {port: 3000, healthRef: "tinyauth-http"},
			"coolify":      {port: 8080, healthRef: "coolify-http"},
		},
		renderCompose: func(domain string) []byte { return RenderBasementCoreComposeForDomain(domain) },
	}
}

func BasementCoreComposeRendererContract() RendererContract {
	return basementCoreContract("compose", basementCoreComposeTemplateRef, basementCoreComposeSchema)
}

func BasementCoreOpenTofuRendererContract() RendererContract {
	return basementCoreContract("opentofu", basementCoreOpenTofuTemplateRef, basementCoreOpenTofuSchema)
}

// ExpectedBasementCoreComposeArtifact returns the immutable built-in Compose
// definition used by both generation and the local runtime-owner admission
// boundary. Callers receive a defensive copy.
func ExpectedBasementCoreComposeArtifact() []byte {
	return RenderBasementCoreComposeForDomain("home.test")
}

var basementDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func RenderBasementCoreComposeForDomain(domain string) []byte {
	if !basementDomainPattern.MatchString(domain) {
		return nil
	}
	return []byte(strings.ReplaceAll(basementCoreCompose, "{{STACKKIT_DOMAIN}}", domain))
}

func ValidateBasementCoreComposeArtifact(content []byte) bool {
	match := regexp.MustCompile("id\\.([a-z0-9.-]+)`").FindSubmatch(content)
	return len(match) == 2 && bytes.Equal(content, RenderBasementCoreComposeForDomain(string(match[1])))
}

// BasementCoreServiceContract is the secret-free, pinned service identity
// enforced again by the local runtime owner before Docker can be reached.
type BasementCoreServiceContract struct {
	Ref         string
	ImageRef    string
	ImageDigest string
	// HealthRequired is false only for image-health components that declare no
	// executable Compose healthcheck. Every command/HTTP health contract must
	// be observed as healthy before readiness can pass.
	HealthRequired bool
}

// BasementCoreServiceContracts returns the closed service set in stable ID
// order. The CUE-owned renderer remains the source of this projection.
func BasementCoreServiceContracts() []BasementCoreServiceContract {
	var components []struct {
		ID    string `json:"id"`
		Image struct {
			Ref    string `json:"ref"`
			Digest string `json:"digest"`
		} `json:"image"`
		Health struct {
			Kind string `json:"kind"`
		} `json:"health"`
	}
	if err := json.Unmarshal([]byte(basementCoreComponentsJSON), &components); err != nil {
		panic("invalid built-in Basement core component contract: " + err.Error())
	}
	result := make([]BasementCoreServiceContract, len(components))
	for index, component := range components {
		result[index] = BasementCoreServiceContract{
			Ref: component.ID, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
			HealthRequired: component.Health.Kind != "image",
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}

func basementCoreContract(kind, templateRef, schema string) RendererContract {
	sum := sha256.Sum256([]byte(schema))
	return RendererContract{
		Kind: kind, RendererRef: basementCoreRendererRef, TemplateRef: templateRef,
		Version: basementCoreVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func newBasementCoreComposeRenderer() basementCoreRenderer {
	return basementCoreRenderer{
		contract: BasementCoreComposeRendererContract(), unitID: basementCoreComposeUnitID,
		outputRef: basementCoreComposeOutputRef,
		render: func(unit RenderUnit) []byte {
			domain, _ := unit.NetworkDomainBase()
			return RenderBasementCoreComposeForDomain(domain)
		},
	}
}

func newBasementCoreOpenTofuRenderer() basementCoreRenderer {
	return basementCoreRenderer{
		contract: BasementCoreOpenTofuRendererContract(), unitID: basementCoreOpenTofuUnitID,
		outputRef: basementCoreOpenTofuOutputRef, render: func(unit RenderUnit) []byte {
			domain, _ := unit.NetworkDomainBase()
			return renderBasementCoreOpenTofu(domain)
		},
	}
}

func (r basementCoreRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateBasementCoreUnit(unit, r.contract, r.unitID, r.outputRef); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: r.outputRef, Bytes: r.render(unit)}}, nil
}

func validateBasementCoreUnit(unit RenderUnit, contract RendererContract, unitID, outputRef string) error {
	return validateBasementCoreUnitOutputs(unit, contract, unitID, []string{outputRef})
}

func validateBasementCoreUnitOutputs(unit RenderUnit, contract RendererContract, unitID string, outputRefs []string) error {
	return validateClosedLocalCoreUnitOutputs(unit, contract, unitID, outputRefs, basementClosedLocalCoreProfile())
}

func validateClosedLocalCoreUnitOutputs(unit RenderUnit, contract RendererContract, unitID string, outputRefs []string, profile closedLocalCoreProfile) error {
	path := "resolvedPlan.modules." + profile.moduleID + ".renderUnits." + unitID
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasDomain || !basementDomainPattern.MatchString(domain) {
		return fail(ErrInvalidPlan, "resolvedPlan.network.configuration.domain.base", "%s requires a canonical resolved network domain", profile.displayName)
	}
	if unit.ModuleID() != profile.moduleID || unit.ID() != unitID {
		return fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", profile.moduleID, unitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered %s contract", profile.displayName)
	}
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" {
		return fail(ErrInvalidPlan, path+".runtime", "%s requires exact container/stackkit delivery", profile.displayName)
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if !hasEngine || engine != profile.runtimeEngine || !hasImage || imageRef != profile.imageRef ||
		!hasDigest || imageDigest != profile.imageDigest || !hasEntry || entry != profile.entryComponent {
		return fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the governed %s graph", profile.displayName)
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != unitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) ||
		!containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return fail(ErrInvalidPlan, path+".instances", "%s requires one exact node-local target", profile.displayName)
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 ||
		!emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "%s consumes no caller or secret material; Apply supplies local custody out of band", profile.displayName)
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return fail(ErrInvalidPlan, path+".interfaces", "%s renderer receives no provider or privileged host authority", profile.displayName)
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); !exactStringList(outputs, outputRefs) {
		return fail(ErrInvalidPlan, path+".outputs", "requires exactly the governed outputs")
	}
	if err := validateClosedLocalCoreComponents(unit.RuntimeComponentsJSON(), profile.componentsJSON, path+".runtime.components", profile.displayName); err != nil {
		return err
	}
	var endpoints []rawModuleServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != len(profile.serviceEndpoints) {
		return fail(ErrInvalidPlan, path+".serviceEndpoints", "requires the exact governed %s service endpoints", profile.displayName)
	}
	expectedEndpoints := make(map[string]closedLocalCoreEndpoint, len(profile.serviceEndpoints))
	for ref, endpoint := range profile.serviceEndpoints {
		expectedEndpoints[ref] = endpoint
	}
	for _, endpoint := range endpoints {
		expected, ok := expectedEndpoints[endpoint.ServiceRef]
		if !ok || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != expected.port ||
			endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
			endpoint.HealthRef != expected.healthRef ||
			!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
			!exactStringList(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
			return fail(ErrInvalidPlan, path+".serviceEndpoints", "%s service route differs from the closed local contract", profile.displayName)
		}
		delete(expectedEndpoints, endpoint.ServiceRef)
	}
	if len(expectedEndpoints) != 0 {
		return fail(ErrInvalidPlan, path+".serviceEndpoints", "%s service endpoint set is incomplete", profile.displayName)
	}
	if err := validateRuntimeListenerComposeParity(unit.RuntimeListenersJSON(), profile.renderCompose(domain), path+".runtimeListeners"); err != nil {
		return err
	}
	return nil
}

func validateClosedLocalCoreComponents(data []byte, expectedJSON, path, displayName string) error {
	var actual []map[string]any
	var expected []map[string]any
	if err := json.Unmarshal(data, &actual); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Basement core components", err)
	}
	if err := json.Unmarshal([]byte(expectedJSON), &expected); err != nil {
		return wrap(ErrRendererFailure, path, "decode built-in "+displayName+" contract", err)
	}
	normalizeBasementCoreComponentSets(actual)
	normalizeBasementCoreComponentSets(expected)
	sort.Slice(actual, func(i, j int) bool { return fmt.Sprint(actual[i]["id"]) < fmt.Sprint(actual[j]["id"]) })
	sort.Slice(expected, func(i, j int) bool { return fmt.Sprint(expected[i]["id"]) < fmt.Sprint(expected[j]["id"]) })
	actualCanonical, err := json.Marshal(actual)
	if err != nil {
		return wrap(ErrInvalidPlan, path, "canonicalize Basement core components", err)
	}
	expectedCanonical, err := json.Marshal(expected)
	if err != nil {
		return wrap(ErrRendererFailure, path, "canonicalize built-in Basement core contract", err)
	}
	if !bytes.Equal(actualCanonical, expectedCanonical) {
		actualHash := sha256.Sum256(actualCanonical)
		expectedHash := sha256.Sum256(expectedCanonical)
		return fail(
			ErrInvalidPlan, path,
			"component graph digest sha256:%s differs from governed digest sha256:%s",
			hex.EncodeToString(actualHash[:]), hex.EncodeToString(expectedHash[:]),
		)
	}
	return nil
}

func validateBasementCoreComponents(data []byte, path string) error {
	return validateClosedLocalCoreComponents(data, basementCoreComponentsJSON, path, "Basement core")
}

func normalizeBasementCoreComponentSets(components []map[string]any) {
	for _, component := range components {
		for _, field := range []string{"dependsOn", "networkRefs"} {
			values, _ := component[field].([]any)
			sort.Slice(values, func(i, j int) bool { return fmt.Sprint(values[i]) < fmt.Sprint(values[j]) })
		}
		volumes, _ := component["volumes"].([]any)
		sort.Slice(volumes, func(i, j int) bool {
			left, _ := volumes[i].(map[string]any)
			right, _ := volumes[j].(map[string]any)
			return fmt.Sprint(left["id"]) < fmt.Sprint(right["id"])
		})
	}
}

func renderBasementCoreOpenTofu(domains ...string) []byte {
	domain := "home.test"
	if len(domains) == 1 {
		domain = domains[0]
	}
	return renderBasementCoreOpenTofuFromCompose(RenderBasementCoreComposeForDomain(domain))
}

func renderBasementCoreOpenTofuFromCompose(compose []byte) []byte {
	escapedCompose := strings.ReplaceAll(string(compose), "${", "$${")
	return []byte(fmt.Sprintf(`terraform {
  required_version = ">= 1.10.0"
  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

resource "local_file" "basement_core_compose" {
  filename        = "${path.module}/compose.yaml"
  file_permission = "0640"
  content         = <<-YAML
%sYAML
}

resource "terraform_data" "basement_core" {
  triggers_replace = [sha256(local_file.basement_core_compose.content)]

  provisioner "local-exec" {
    command = "docker compose -f ${local_file.basement_core_compose.filename} up -d --wait"
    environment = {
      STACKKIT_CUSTODY_DIR = abspath("${path.module}/../../../../../../.stackkit/custody")
    }
  }

  provisioner "local-exec" {
    when    = destroy
    command = "docker compose -f ${self.input} down"
    environment = {
      STACKKIT_CUSTODY_DIR = abspath("${path.module}/../../../../../../.stackkit/custody")
    }
  }

  input = local_file.basement_core_compose.filename
}
`, escapedCompose))
}

var _ UnitRenderer = basementCoreRenderer{}
