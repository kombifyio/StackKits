package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const (
	cloudCoreModuleID         = "stackkits-cloud-core-runtime"
	cloudCoreComposeUnitID    = "compose"
	cloudCoreComposeTemplate  = "builtin://cloud/core/compose/v1.yaml"
	cloudCoreComposeOutputRef = "platform/cloud-core/compose.yaml"
	cloudCoreRendererRef      = "stackkit"
	cloudCoreVersion          = "1.0.0"
	cloudCoreComposeSchema    = `stackkit.cloud-core-compose/v1|artifact-revision:10|resolved-network-domain:required|resolved-subdomain-prefix:optional|runtime-listeners:catalog-bound,direct-loopback-only|services:router,socket-proxy,pocketid,tinyauth,coolify,coolify-postgres,coolify-redis,coolify-realtime,hub|networks:cloud-core-host-reachable,cloud-control-internal|public-routes:declared-default-closed|credentials:service-scoped-owner-signed-cloud-runtime-custody|external-backup:required-before-apply|public-tls:separate-owner-traefik-acme-http-01|ingress:forward-auth-bound|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`
)

const cloudCoreComponentsJSON = `[
{"id":"router","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/traefik/traefik:v3","digest":"sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"},"dependsOn":["socket-proxy"],"networkRefs":["cloud-core","cloud-control"],"health":{"kind":"http","path":"/ping","port":8080},"resources":{"memoryLimit":"256m"}},
{"id":"socket-proxy","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/tecnativa/docker-socket-proxy:v0.4.2","digest":"sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"},"dependsOn":[],"networkRefs":["cloud-control"],"health":{"kind":"image"},"resources":{"memoryLimit":"128m"}},
{"id":"pocketid","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/pocket-id/pocket-id:v2.7.0","digest":"sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"},"dependsOn":[],"networkRefs":["cloud-core"],"volumes":[{"id":"pocketid-data","target":"/app/data","class":"persistent","backup":true}],"health":{"kind":"http","path":"/health","port":1411},"resources":{"memoryLimit":"512m"}},
{"id":"tinyauth","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/steveiliop56/tinyauth:v5.0.7","digest":"sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"},"dependsOn":["pocketid"],"networkRefs":["cloud-core"],"volumes":[{"id":"tinyauth-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["tinyauth","healthcheck"]},"resources":{"memoryLimit":"256m"}},
{"id":"coolify","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify:4.1.2","digest":"sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"},"dependsOn":["coolify-postgres","coolify-redis","coolify-realtime"],"networkRefs":["cloud-core","cloud-control"],"volumes":[{"id":"coolify-data","target":"/var/www/html/storage","class":"persistent","backup":true},{"id":"coolify-ssh","target":"/var/www/html/storage/app/ssh","class":"persistent","backup":true},{"id":"coolify-applications","target":"/var/www/html/storage/app/applications","class":"persistent","backup":true},{"id":"coolify-databases","target":"/var/www/html/storage/app/databases","class":"persistent","backup":true},{"id":"coolify-services","target":"/var/www/html/storage/app/services","class":"persistent","backup":true},{"id":"coolify-backups","target":"/var/www/html/storage/app/backups","class":"persistent","backup":true}],"health":{"kind":"http","path":"/api/health","port":8080},"resources":{"memoryLimit":"1g"}},
{"id":"coolify-postgres","role":"database","lifecycle":"daemon","image":{"ref":"docker.io/library/postgres:15-alpine","digest":"sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"},"dependsOn":[],"networkRefs":["cloud-control"],"volumes":[{"id":"coolify-postgres-data","target":"/var/lib/postgresql/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["pg_isready","-U","coolify"]},"resources":{"memoryLimit":"512m"}},
{"id":"coolify-redis","role":"cache","lifecycle":"daemon","image":{"ref":"docker.io/library/redis:7-alpine","digest":"sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"},"dependsOn":[],"networkRefs":["cloud-control"],"volumes":[{"id":"coolify-redis-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["redis-cli","ping"]},"resources":{"memoryLimit":"256m"}},
{"id":"coolify-realtime","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify-realtime:1.0.16","digest":"sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"},"dependsOn":["coolify-redis"],"networkRefs":["cloud-control"],"health":{"kind":"http","path":"/ready","port":6001}},
{"id":"hub","role":"application","lifecycle":"daemon","image":{"ref":"docker.io/library/nginx:alpine","digest":"sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"},"dependsOn":["tinyauth"],"networkRefs":["cloud-core"],"health":{"kind":"http","path":"/healthz","port":80},"resources":{"memoryLimit":"256m"}}
]`

const cloudCoreCompose = `name: stackkit-cloud-core
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
    environment: {CONTAINERS: "1", EVENTS: "1", INFO: "1", NETWORKS: "1", PING: "1", VERSION: "1"}
    volumes: [/var/run/docker.sock:/var/run/docker.sock:ro]
    networks: [cloud-control]
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
      - --certificatesresolvers.stackkits.acme.httpchallenge=true
      - --certificatesresolvers.stackkits.acme.httpchallenge.entrypoint=web
      - --certificatesresolvers.stackkits.acme.storage=/letsencrypt/acme.json
    volumes: [public-tls-acme:/letsencrypt]
    ports: ["0.0.0.0:80:80", "0.0.0.0:443:443", "127.0.0.1:8080:8080"]
    healthcheck: {test: ["CMD", "traefik", "healthcheck", "--ping"], interval: 5s, timeout: 3s, retries: 12, start_period: 5s}
    networks: [cloud-core, cloud-control]
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
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/pocketid.env"]
    volumes: [pocketid-data:/app/data]
    ports: ["127.0.0.1:1411:1411"]
    healthcheck: {test: ["CMD", "/app/pocket-id", "healthcheck"], interval: 10s, timeout: 5s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.pocketid.rule=Host(` + "`id.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.routers.pocketid.entrypoints=websecure
      - traefik.http.routers.pocketid.tls=true
      - traefik.http.routers.pocketid.tls.certresolver=stackkits
      - traefik.http.services.pocketid.loadbalancer.server.port=1411
    networks: [cloud-core]
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
      - path: "${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/tinyauth.env"
      - path: "${STACKKIT_CUSTODY_DIR:?}/tinyauth-pocketid/tinyauth.env"
        required: false
    volumes: [tinyauth-data:/data]
    ports: ["127.0.0.1:4000:3000"]
    healthcheck: {test: ["CMD", "tinyauth", "healthcheck"], interval: 10s, timeout: 5s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.tinyauth.rule=Host(` + "`auth.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.routers.tinyauth.entrypoints=websecure
      - traefik.http.routers.tinyauth.tls=true
      - traefik.http.routers.tinyauth.tls.certresolver=stackkits
      - traefik.http.services.tinyauth.loadbalancer.server.port=3000
      - traefik.http.middlewares.stackkit-forward-auth.forwardauth.address=http://tinyauth:3000/api/auth/traefik
      - traefik.http.middlewares.stackkit-forward-auth.forwardauth.trustForwardHeader=true
      - traefik.http.middlewares.stackkit-forward-auth.forwardauth.authResponseHeaders=remote-user,remote-sub,remote-name,remote-email,remote-groups
    networks: [cloud-core]
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
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    volumes: [coolify-postgres-data:/var/lib/postgresql/data]
    healthcheck: {test: ["CMD-SHELL", "pg_isready -U $${DB_USERNAME} -d $${DB_DATABASE:-coolify}"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
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
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    command: ["sh", "-c", "exec redis-server --save 20 1 --loglevel warning --requirepass \"$${REDIS_PASSWORD}\""]
    volumes: [coolify-redis-data:/data]
    healthcheck: {test: ["CMD-SHELL", "redis-cli -a $${REDIS_PASSWORD} ping | grep PONG"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
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
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    healthcheck: {test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:6001/ready && wget -qO- http://127.0.0.1:6002/ready"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
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
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    volumes:
      - ${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env:/var/www/html/.env:ro
      - coolify-data:/var/www/html/storage
      - coolify-ssh:/var/www/html/storage/app/ssh
      - coolify-applications:/var/www/html/storage/app/applications
      - coolify-databases:/var/www/html/storage/app/databases
      - coolify-services:/var/www/html/storage/app/services
      - coolify-backups:/var/www/html/storage/app/backups
    ports: ["127.0.0.1:8000:8080"]
    healthcheck: {test: ["CMD-SHELL", "curl --fail http://127.0.0.1:8080/api/health"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.coolify.rule=Host(` + "`coolify.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.routers.coolify.entrypoints=websecure
      - traefik.http.routers.coolify.tls=true
      - traefik.http.routers.coolify.tls.certresolver=stackkits
      - traefik.http.routers.coolify.middlewares=stackkit-forward-auth@docker
      - traefik.http.services.coolify.loadbalancer.server.port=8080
    networks: [cloud-core, cloud-control]
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
        printf '%s\n' '<!doctype html><title>StackKit Cloud Hub</title><h1>StackKit Cloud</h1><ul><li><a href="https://id.{{STACKKIT_DOMAIN}}">PocketID</a></li><li><a href="https://auth.{{STACKKIT_DOMAIN}}">TinyAuth</a></li><li><a href="https://coolify.{{STACKKIT_DOMAIN}}">Coolify</a></li></ul>' > /usr/share/nginx/html/index.html
        printf '%s\n' '{"status":"ok","service":"cloud-hub"}' > /usr/share/nginx/html/healthz
        exec nginx -g 'daemon off;'
    ports: ["127.0.0.1:8081:80"]
    labels:
      - traefik.enable=true
      - traefik.http.routers.hub.rule=Host(` + "`base.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.routers.hub.entrypoints=websecure
      - traefik.http.routers.hub.tls=true
      - traefik.http.routers.hub.tls.certresolver=stackkits
      - traefik.http.routers.hub.middlewares=stackkit-forward-auth@docker
      - traefik.http.services.hub.loadbalancer.server.port=80
    healthcheck: {test: ["CMD-SHELL", "wget -qO- http://127.0.0.1/healthz | grep '\"status\":\"ok\"'"], interval: 5s, timeout: 2s, retries: 12, start_period: 5s}
    networks: [cloud-core]
networks:
  cloud-core: {name: stackkit-cloud-core}
  cloud-control: {name: stackkit-cloud-control, internal: true}
volumes:
  pocketid-data: {}
  tinyauth-data: {}
  coolify-data: {}
  coolify-ssh: {}
  coolify-applications: {}
  coolify-databases: {}
  coolify-services: {}
  coolify-backups: {}
  coolify-postgres-data: {}
  coolify-redis-data: {}
  public-tls-acme: {}
`

type cloudCoreRenderer struct {
	contract RendererContract
	profile  cloudCoreRenderProfile
}

func CloudCoreComposeRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(cloudCoreComposeSchema))
	return RendererContract{Kind: "compose", RendererRef: cloudCoreRendererRef, TemplateRef: cloudCoreComposeTemplate, Version: cloudCoreVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:])}
}

func RenderCloudCoreComposeForDomain(domain string) []byte {
	return RenderCloudCoreComposeForAddress(domain, "")
}

func RenderCloudCoreComposeForAddress(domain, prefix string) []byte {
	if !basementDomainPattern.MatchString(domain) {
		return nil
	}
	serviceDomain := domain
	if prefix != "" {
		serviceDomain = prefix + "-{{STACKKIT_SERVICE}}." + domain
	}
	output := strings.ReplaceAll(cloudCoreCompose, "{{STACKKIT_DOMAIN}}", serviceDomain)
	for _, service := range []string{"id", "auth", "coolify", "base"} {
		output = strings.ReplaceAll(output, service+"."+prefix+"-{{STACKKIT_SERVICE}}", prefix+"-"+service)
	}
	return []byte(strings.ReplaceAll(output, "{{STACKKIT_SERVICE}}", ""))
}

// ExpectedCloudCoreComposeArtifact returns the immutable default-domain
// artifact used by executor contract tests.
func ExpectedCloudCoreComposeArtifact() []byte {
	return RenderCloudCoreComposeForDomain("home.test")
}

func ValidateCloudCoreComposeArtifact(content []byte) bool {
	match := regexp.MustCompile("routers[.]pocketid[.]rule=Host\\(`([a-z0-9.-]+)`\\)").FindSubmatch(content)
	if len(match) != 2 {
		return false
	}
	host := string(match[1])
	domain, prefix := strings.TrimPrefix(host, "id."), ""
	if domain == host {
		separator := strings.Index(host, "-id.")
		if separator < 1 {
			return false
		}
		prefix, domain = host[:separator], host[separator+4:]
	}
	return bytes.Equal(content, RenderCloudCoreComposeForAddress(domain, prefix))
}

func CloudCoreServiceContracts() []BasementCoreServiceContract {
	var components []struct {
		ID     string                       `json:"id"`
		Image  struct{ Ref, Digest string } `json:"image"`
		Health struct {
			Kind string `json:"kind"`
		} `json:"health"`
	}
	if err := json.Unmarshal([]byte(cloudCoreComponentsJSON), &components); err != nil {
		panic("invalid built-in Cloud core component contract: " + err.Error())
	}
	result := make([]BasementCoreServiceContract, len(components))
	for index, component := range components {
		result[index] = BasementCoreServiceContract{Ref: component.ID, ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest, HealthRequired: component.Health.Kind != "image"}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}

func newCloudCoreComposeRenderer() cloudCoreRenderer {
	return cloudCoreRenderer{
		contract: CloudCoreComposeRendererContract(),
		profile:  cloudCoreRenderProfileForCloudCore(),
	}
}

func (r cloudCoreRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	return renderCloudCoreUnit(ctx, unit, r.contract, r.profile)
}

func validateCloudCoreComponents(data []byte, path string) error {
	var actual, expected []map[string]any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal([]byte(cloudCoreComponentsJSON), &expected) != nil {
		return fail(ErrInvalidPlan, path, "Cloud core component graph is malformed")
	}
	normalizeBasementCoreComponentSets(actual)
	normalizeBasementCoreComponentSets(expected)
	sort.Slice(actual, func(i, j int) bool { return actual[i]["id"].(string) < actual[j]["id"].(string) })
	sort.Slice(expected, func(i, j int) bool { return expected[i]["id"].(string) < expected[j]["id"].(string) })
	actualCanonical, _ := json.Marshal(actual)
	expectedCanonical, _ := json.Marshal(expected)
	if !bytes.Equal(actualCanonical, expectedCanonical) {
		return fail(ErrInvalidPlan, path, "component graph differs from the governed Cloud core contract")
	}
	return nil
}

var _ UnitRenderer = cloudCoreRenderer{}
