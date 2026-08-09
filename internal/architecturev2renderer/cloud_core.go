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
	cloudCoreComposeSchema    = `stackkit.cloud-core-compose/v1|artifact-revision:1|resolved-network-domain:required|services:router,socket-proxy,pocketid,tinyauth,coolify,coolify-postgres,coolify-redis,coolify-realtime,hub|networks:cloud-core-host-reachable,cloud-control-internal|public-routes:declared-default-closed|credentials:service-scoped-owner-signed-cloud-runtime-custody|external-backup:required-before-apply|public-tls:separate-owner|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned`
)

const cloudCoreComponentsJSON = `[
{"id":"router","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/traefik/traefik:v3","digest":"sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8"},"dependsOn":["socket-proxy"],"networkRefs":["cloud-core","cloud-control"],"health":{"kind":"http","path":"/ping","port":8080}},
{"id":"socket-proxy","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/tecnativa/docker-socket-proxy:v0.4.2","digest":"sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"},"dependsOn":[],"networkRefs":["cloud-control"],"health":{"kind":"image"}},
{"id":"pocketid","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/pocket-id/pocket-id:v2.7.0","digest":"sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e"},"dependsOn":[],"networkRefs":["cloud-core"],"volumes":[{"id":"pocketid-data","target":"/app/data","class":"persistent","backup":true}],"health":{"kind":"http","path":"/health","port":1411}},
{"id":"tinyauth","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/steveiliop56/tinyauth:v5.0.7","digest":"sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6"},"dependsOn":["pocketid"],"networkRefs":["cloud-core"],"volumes":[{"id":"tinyauth-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["tinyauth","healthcheck"]}},
{"id":"coolify","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify:4.1.2","digest":"sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"},"dependsOn":["coolify-postgres","coolify-redis","coolify-realtime"],"networkRefs":["cloud-core","cloud-control"],"volumes":[{"id":"coolify-data","target":"/var/www/html/storage","class":"persistent","backup":true},{"id":"coolify-ssh","target":"/var/www/html/storage/app/ssh","class":"persistent","backup":true},{"id":"coolify-applications","target":"/var/www/html/storage/app/applications","class":"persistent","backup":true},{"id":"coolify-databases","target":"/var/www/html/storage/app/databases","class":"persistent","backup":true},{"id":"coolify-services","target":"/var/www/html/storage/app/services","class":"persistent","backup":true},{"id":"coolify-backups","target":"/var/www/html/storage/app/backups","class":"persistent","backup":true}],"health":{"kind":"http","path":"/api/health","port":8080}},
{"id":"coolify-postgres","role":"database","lifecycle":"daemon","image":{"ref":"docker.io/library/postgres:15-alpine","digest":"sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f"},"dependsOn":[],"networkRefs":["cloud-control"],"volumes":[{"id":"coolify-postgres-data","target":"/var/lib/postgresql/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["pg_isready","-U","coolify"]}},
{"id":"coolify-redis","role":"cache","lifecycle":"daemon","image":{"ref":"docker.io/library/redis:7-alpine","digest":"sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99"},"dependsOn":[],"networkRefs":["cloud-control"],"volumes":[{"id":"coolify-redis-data","target":"/data","class":"persistent","backup":true}],"health":{"kind":"command","command":["redis-cli","ping"]}},
{"id":"coolify-realtime","role":"application","lifecycle":"daemon","image":{"ref":"ghcr.io/coollabsio/coolify-realtime:1.0.16","digest":"sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629"},"dependsOn":["coolify-redis"],"networkRefs":["cloud-control"],"health":{"kind":"http","path":"/ready","port":6001}},
{"id":"hub","role":"application","lifecycle":"daemon","image":{"ref":"docker.io/library/nginx:alpine","digest":"sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"},"dependsOn":["tinyauth"],"networkRefs":["cloud-core"],"health":{"kind":"http","path":"/healthz","port":80}}
]`

const cloudCoreCompose = `name: stackkit-cloud-core
services:
  socket-proxy:
    image: ghcr.io/tecnativa/docker-socket-proxy:v0.4.2@sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476
    restart: unless-stopped
    environment: {CONTAINERS: "1", EVENTS: "1", INFO: "1", NETWORKS: "1", PING: "1", VERSION: "1"}
    volumes: [/var/run/docker.sock:/var/run/docker.sock:ro]
    networks: [cloud-control]
  router:
    image: ghcr.io/traefik/traefik:v3@sha256:652929a140a32d7cafafb13c6cdfab5376cfeff800f51397b87b524501ed02a8
    restart: unless-stopped
    depends_on: [socket-proxy]
    command:
      - --api.insecure=true
      - --ping=true
      - --providers.docker.endpoint=tcp://socket-proxy:2375
      - --providers.docker.exposedbydefault=false
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
    ports: ["80:80", "443:443", "8080:8080"]
    healthcheck: {test: ["CMD", "traefik", "healthcheck", "--ping"], interval: 5s, timeout: 3s, retries: 12, start_period: 5s}
    networks: [cloud-core, cloud-control]
  pocketid:
    image: ghcr.io/pocket-id/pocket-id:v2.7.0@sha256:45bdeaf3fcd6d07cf8721e98785d93324bb8e65b586498874c05a3d489c8094e
    restart: unless-stopped
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/pocketid.env"]
    volumes: [pocketid-data:/app/data]
    ports: ["1411:1411"]
    healthcheck: {test: ["CMD", "/app/pocket-id", "healthcheck"], interval: 10s, timeout: 5s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.pocketid.rule=Host(` + "`id.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.pocketid.loadbalancer.server.port=1411
    networks: [cloud-core]
  tinyauth:
    image: ghcr.io/steveiliop56/tinyauth:v5.0.7@sha256:0793c71c49906e079d90c7e693cded9df569217a92d717dc9b171f2116fcd1c6
    restart: unless-stopped
    depends_on: [pocketid]
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/tinyauth.env"]
    volumes: [tinyauth-data:/data]
    ports: ["4000:3000"]
    healthcheck: {test: ["CMD", "tinyauth", "healthcheck"], interval: 10s, timeout: 5s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.tinyauth.rule=Host(` + "`auth.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.tinyauth.loadbalancer.server.port=3000
    networks: [cloud-core]
  coolify-postgres:
    image: docker.io/library/postgres:15-alpine@sha256:3d0f7584ed7d04e27fa050d6683a74746608faf21f202be78460d679cc56461f
    restart: unless-stopped
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    volumes: [coolify-postgres-data:/var/lib/postgresql/data]
    healthcheck: {test: ["CMD-SHELL", "pg_isready -U $${DB_USERNAME} -d $${DB_DATABASE:-coolify}"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
  coolify-redis:
    image: docker.io/library/redis:7-alpine@sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99
    restart: unless-stopped
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    command: ["sh", "-c", "exec redis-server --save 20 1 --loglevel warning --requirepass \"$${REDIS_PASSWORD}\""]
    volumes: [coolify-redis-data:/data]
    healthcheck: {test: ["CMD-SHELL", "redis-cli -a $${REDIS_PASSWORD} ping | grep PONG"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
  coolify-realtime:
    image: ghcr.io/coollabsio/coolify-realtime:1.0.16@sha256:b5bb9d1c95d9b4ca59773b82d1e1a2bf4ccac5fbed33be19b9b3906574db3629
    restart: unless-stopped
    depends_on: [coolify-redis]
    env_file: ["${STACKKIT_CUSTODY_DIR:?}/cloud-runtime/coolify.env"]
    healthcheck: {test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:6001/ready && wget -qO- http://127.0.0.1:6002/ready"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    networks: [cloud-control]
  coolify:
    image: ghcr.io/coollabsio/coolify:4.1.2@sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f
    restart: unless-stopped
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
    ports: ["8000:8080"]
    healthcheck: {test: ["CMD-SHELL", "curl --fail http://127.0.0.1:8080/api/health"], interval: 5s, timeout: 2s, retries: 12, start_period: 10s}
    labels:
      - traefik.enable=true
      - traefik.http.routers.coolify.rule=Host(` + "`coolify.{{STACKKIT_DOMAIN}}`" + `)
      - traefik.http.services.coolify.loadbalancer.server.port=8080
    networks: [cloud-core, cloud-control]
  hub:
    image: docker.io/library/nginx:alpine@sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752
    restart: unless-stopped
    depends_on: [tinyauth]
    command:
      - /bin/sh
      - -ec
      - |
        printf '%s\n' '<!doctype html><title>StackKit Cloud Hub</title><h1>StackKit Cloud</h1><ul><li><a href="https://id.{{STACKKIT_DOMAIN}}">PocketID</a></li><li><a href="https://auth.{{STACKKIT_DOMAIN}}">TinyAuth</a></li><li><a href="https://coolify.{{STACKKIT_DOMAIN}}">Coolify</a></li></ul>' > /usr/share/nginx/html/index.html
        printf '%s\n' '{"status":"ok","service":"cloud-hub"}' > /usr/share/nginx/html/healthz
        exec nginx -g 'daemon off;'
    labels:
      - traefik.enable=true
      - traefik.http.routers.hub.rule=Host(` + "`base.{{STACKKIT_DOMAIN}}`" + `)
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
`

type cloudCoreRenderer struct{ contract RendererContract }

func CloudCoreComposeRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(cloudCoreComposeSchema))
	return RendererContract{Kind: "compose", RendererRef: cloudCoreRendererRef, TemplateRef: cloudCoreComposeTemplate, Version: cloudCoreVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:])}
}

func RenderCloudCoreComposeForDomain(domain string) []byte {
	if !basementDomainPattern.MatchString(domain) {
		return nil
	}
	return []byte(strings.ReplaceAll(cloudCoreCompose, "{{STACKKIT_DOMAIN}}", domain))
}

// ExpectedCloudCoreComposeArtifact returns the immutable default-domain
// artifact used by executor contract tests.
func ExpectedCloudCoreComposeArtifact() []byte {
	return RenderCloudCoreComposeForDomain("home.test")
}

func ValidateCloudCoreComposeArtifact(content []byte) bool {
	match := regexp.MustCompile("id\\.([a-z0-9.-]+)`").FindSubmatch(content)
	return len(match) == 2 && bytes.Equal(content, RenderCloudCoreComposeForDomain(string(match[1])))
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
	return cloudCoreRenderer{contract: CloudCoreComposeRendererContract()}
}

func (r cloudCoreRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := "resolvedPlan.modules." + cloudCoreModuleID + ".renderUnits." + cloudCoreComposeUnitID
	domain, hasDomain := unit.NetworkDomainBase()
	if !hasDomain || !basementDomainPattern.MatchString(domain) || unit.ModuleID() != cloudCoreModuleID || unit.ID() != cloudCoreComposeUnitID ||
		unit.Kind() != r.contract.Kind || unit.RendererRef() != r.contract.RendererRef || unit.TemplateRef() != r.contract.TemplateRef ||
		unit.Version() != r.contract.Version || unit.ContractHash() != r.contract.ContractHash {
		return nil, fail(ErrInvalidPlan, path, "renderer accepts only the exact Cloud core Compose contract")
	}
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" {
		return nil, fail(ErrInvalidPlan, path+".runtime", "Cloud core requires exact container/stackkit delivery")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if !hasEngine || engine != "docker" || !hasImage || imageRef != "ghcr.io/coollabsio/coolify:4.1.2" || !hasDigest ||
		imageDigest != "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f" || !hasEntry || entry != "coolify" {
		return nil, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the governed Cloud core graph")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || unit.InstanceID() != cloudCoreComposeUnitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return nil, fail(ErrInvalidPlan, path+".instances", "Cloud core requires one exact node-local target")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 ||
		!emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONObject(unit.PlanInputsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) {
		return nil, fail(ErrInvalidPlan, path+".inputs", "Cloud core consumes no caller, provider, or secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) ||
		!exactStringList(unit.DeclaredOutputs(), []string{cloudCoreComposeOutputRef}) {
		return nil, fail(ErrInvalidPlan, path, "Cloud core receives no provider or privileged host authority and emits one governed artifact")
	}
	if err := validateCloudCoreComponents(unit.RuntimeComponentsJSON(), path+".runtime.components"); err != nil {
		return nil, err
	}
	var endpoints []rawModuleServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 4 {
		return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires the four exact Cloud service endpoints")
	}
	expected := map[string]struct {
		port   int
		health string
	}{
		"base": {80, "cloud-hub-http"}, "id": {1411, "cloud-pocketid-http"},
		"auth": {3000, "cloud-tinyauth-http"}, "coolify": {8080, "cloud-coolify-http"},
	}
	for _, endpoint := range endpoints {
		want, ok := expected[endpoint.ServiceRef]
		if !ok || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != want.port || endpoint.RequiredPrivilege != "user" ||
			endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != want.health ||
			!exactStringList(endpoint.AllowedIngressProtocols, []string{"http", "https"}) ||
			!exactStringList(endpoint.AllowedExposures, []string{"public", "remote-private"}) {
			return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "Cloud service route differs from the closed default-deny contract")
		}
		delete(expected, endpoint.ServiceRef)
	}
	if len(expected) != 0 {
		return nil, fail(ErrInvalidPlan, path+".serviceEndpoints", "Cloud service endpoint set is incomplete")
	}
	return []UnitOutput{{Ref: cloudCoreComposeOutputRef, Bytes: RenderCloudCoreComposeForDomain(domain)}}, nil
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
