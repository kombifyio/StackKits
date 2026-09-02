package architecturev2renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

const (
	basementCoreLiteModuleID = "stackkits-basement-core-lite-runtime"

	basementCoreLiteComposeTemplateRef   = "builtin://basement/core-lite/compose/v1.yaml"
	basementCoreLiteOpenTofuTemplateRef  = "builtin://basement/core-lite/opentofu/v1.tf"
	basementCoreLiteTerramateTemplateRef = "builtin://basement/core-lite/terramate/v1"

	basementCoreLiteComposeOutputRef           = "platform/basement-core-lite/compose.yaml"
	basementCoreLiteOpenTofuOutputRef          = "platform/basement-core-lite/main.tf"
	basementCoreLiteTerramateOpenTofuOutputRef = "platform/basement-core-lite/main.tf"
	basementCoreLiteTerramateRootOutputRef     = "platform/basement-core-lite/terramate.tm.hcl"
	basementCoreLiteTerramateStackOutputRef    = "platform/basement-core-lite/stack.tm.hcl"
)

const basementCoreLiteComposeSchema = `stackkit.basement-core-lite-compose/v1|artifact-revision:1|resolved-network-domain:required|runtime-listeners:catalog-bound|services:router,socket-proxy,pocketid,tinyauth,step-ca,kopia-agent,hub|networks:basement-core-host-reachable,basement-control-internal,basement-backup-internal-no-peer|kopia:idle-owner-command,deterministic-source-hostname,read-only-managed-volume-allowlist,owner-local-repository,isolated-restore-staging,internal-no-peer|hub-endpoints:healthz,verification|healthchecks:container-and-module|credentials:service-scoped-owner-signed-runtime-custody|step-ca:owner-rooted-online-intermediate|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`
const basementCoreLiteOpenTofuSchema = `stackkit.basement-core-lite-opentofu/v1|artifact-revision:1|resolved-network-domain:required|runtime-listeners:catalog-bound|local-file:compose|terraform-data:docker-compose-up-wait|networks:basement-core-host-reachable,basement-control-internal,basement-backup-internal-no-peer|kopia:idle-owner-command,deterministic-source-hostname,read-only-managed-volume-allowlist,owner-local-repository,isolated-restore-staging,internal-no-peer|healthchecks:docker-compose-wait|credentials:service-scoped-owner-signed-runtime-custody|step-ca:owner-rooted-online-intermediate|service-lifecycle:stackkits-local|server-provider-lifecycle:not-owned|mem-limit:catalog-resources`
const basementCoreLiteTerramateSchema = `stackkit.basement-core-lite-terramate/v1|artifact-revision:1|runtime-listeners:catalog-bound|engine:terramate|underlay:opentofu|outputs:main.tf,stack.tm.hcl,terramate.tm.hcl|execution-instance:node-local|credentials:none|cloud:none|coolify:omitted`

func basementCoreLiteComponentsJSON() string {
	return filterCoolifyJSONComponents(basementCoreComponentsJSON)
}

// BasementCoreLiteServiceContracts returns the exact service graph emitted by
// the Lite renderer. It is projected from the shared component authority so
// local Apply and Verify cannot grow a second service list.
func BasementCoreLiteServiceContracts() []BasementCoreServiceContract {
	services := BasementCoreServiceContracts()
	result := make([]BasementCoreServiceContract, 0, len(services))
	for _, service := range services {
		if strings.HasPrefix(service.Ref, "coolify") {
			continue
		}
		result = append(result, service)
	}
	return result
}

func basementClosedLocalCoreLiteProfile() closedLocalCoreProfile {
	return closedLocalCoreProfile{
		displayName: "Basement core lite", moduleID: basementCoreLiteModuleID, runtimeEngine: "docker",
		imageRef:       "docker.io/library/nginx:alpine",
		imageDigest:    "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752",
		entryComponent: "hub", componentsJSON: basementCoreLiteComponentsJSON(),
		serviceEndpoints: map[string]closedLocalCoreEndpoint{
			"basement-hub": {port: 80, healthRef: "basement-hub-http"},
			"id":           {port: 1411, healthRef: "pocketid-http"},
			"auth":         {port: 3000, healthRef: "tinyauth-http"},
		},
		renderCompose: func(domain string) []byte { return RenderBasementCoreLiteComposeForDomain(domain) },
	}
}

func BasementCoreLiteComposeRendererContract() RendererContract {
	return basementCoreContract("compose", basementCoreLiteComposeTemplateRef, basementCoreLiteComposeSchema)
}

func BasementCoreLiteOpenTofuRendererContract() RendererContract {
	return basementCoreContract("opentofu", basementCoreLiteOpenTofuTemplateRef, basementCoreLiteOpenTofuSchema)
}

func BasementCoreLiteTerramateRendererContract() RendererContract {
	return basementCoreContract("terramate", basementCoreLiteTerramateTemplateRef, basementCoreLiteTerramateSchema)
}

func RenderBasementCoreLiteComposeForDomain(domain string) []byte {
	standard := RenderBasementCoreComposeForDomain(domain)
	if standard == nil {
		return nil
	}
	return filterCoolifyComposeYAML(standard)
}

func newBasementCoreLiteComposeRenderer() basementCoreRenderer {
	return basementCoreRenderer{
		contract: BasementCoreLiteComposeRendererContract(), unitID: basementCoreComposeUnitID,
		outputRef: basementCoreLiteComposeOutputRef,
		render: func(unit RenderUnit) []byte {
			domain, _ := unit.NetworkDomainBase()
			return RenderBasementCoreLiteComposeForDomain(domain)
		},
	}
}

func newBasementCoreLiteOpenTofuRenderer() basementCoreRenderer {
	return basementCoreRenderer{
		contract: BasementCoreLiteOpenTofuRendererContract(), unitID: basementCoreOpenTofuUnitID,
		outputRef: basementCoreLiteOpenTofuOutputRef, render: func(unit RenderUnit) []byte {
			domain, _ := unit.NetworkDomainBase()
			return renderBasementCoreLiteOpenTofu(domain)
		},
	}
}

type basementCoreLiteTerramateRenderer struct {
	contract RendererContract
}

func newBasementCoreLiteTerramateRenderer() basementCoreLiteTerramateRenderer {
	return basementCoreLiteTerramateRenderer{contract: BasementCoreLiteTerramateRendererContract()}
}

func (r basementCoreLiteTerramateRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateClosedLocalCoreUnitOutputs(unit, r.contract, basementCoreTerramateUnitID, []string{
		basementCoreLiteTerramateOpenTofuOutputRef,
		basementCoreLiteTerramateStackOutputRef,
		basementCoreLiteTerramateRootOutputRef,
	}, basementClosedLocalCoreLiteProfile()); err != nil {
		return nil, err
	}
	domain, _ := unit.NetworkDomainBase()
	return []UnitOutput{
		{Ref: basementCoreLiteTerramateOpenTofuOutputRef, Bytes: renderBasementCoreLiteOpenTofu(domain)},
		{Ref: basementCoreLiteTerramateRootOutputRef, Bytes: []byte(basementCoreTerramateRoot)},
		{Ref: basementCoreLiteTerramateStackOutputRef, Bytes: []byte(strings.ReplaceAll(basementCoreTerramateStack, "Basement Core", "Basement Core Lite"))},
	}, nil
}

func (r basementCoreRenderer) renderLiteUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateClosedLocalCoreUnitOutputs(unit, r.contract, r.unitID, []string{r.outputRef}, basementClosedLocalCoreLiteProfile()); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: r.outputRef, Bytes: r.render(unit)}}, nil
}

func newBasementCoreLiteComposeBoundRenderer() liteBoundRenderer {
	inner := newBasementCoreLiteComposeRenderer()
	return liteBoundRenderer{inner: inner}
}

func newBasementCoreLiteOpenTofuBoundRenderer() liteBoundRenderer {
	inner := newBasementCoreLiteOpenTofuRenderer()
	return liteBoundRenderer{inner: inner}
}

type liteBoundRenderer struct {
	inner basementCoreRenderer
}

func (r liteBoundRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	return r.inner.renderLiteUnit(ctx, unit)
}

func renderBasementCoreLiteOpenTofu(domains ...string) []byte {
	domain := "home.test"
	if len(domains) == 1 {
		domain = domains[0]
	}
	return renderBasementCoreOpenTofuFromCompose(RenderBasementCoreLiteComposeForDomain(domain))
}

func filterCoolifyJSONComponents(raw string) string {
	var components []map[string]any
	if err := json.Unmarshal([]byte(raw), &components); err != nil {
		panic("invalid built-in Basement core component contract: " + err.Error())
	}
	filtered := make([]map[string]any, 0, len(components))
	for _, component := range components {
		id, _ := component["id"].(string)
		if strings.HasPrefix(id, "coolify") {
			continue
		}
		filtered = append(filtered, component)
	}
	out, err := json.Marshal(filtered)
	if err != nil {
		panic("canonicalize Basement core lite components: " + err.Error())
	}
	return string(out)
}

func filterCoolifyComposeYAML(src []byte) []byte {
	lines := bytes.Split(src, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	skipping := false
	inVolumes := false
	for _, line := range lines {
		trimmed := strings.TrimRight(string(line), "\r")
		if trimmed == "volumes:" {
			inVolumes = true
			skipping = false
			out = append(out, line)
			continue
		}
		if inVolumes {
			name := volumeKey(trimmed)
			if strings.HasPrefix(name, "coolify") {
				continue
			}
			out = append(out, line)
			continue
		}
		if serviceKey, ok := composeServiceKey(trimmed); ok {
			skipping = strings.HasPrefix(serviceKey, "coolify")
			if skipping {
				continue
			}
			out = append(out, line)
			continue
		}
		if skipping {
			continue
		}
		text := string(line)
		if strings.Contains(text, "/var/lib/docker/volumes/stackkit-basement-core_coolify") {
			continue
		}
		text = coolifyHubLink.ReplaceAllString(text, "")
		out = append(out, []byte(text))
	}
	return bytes.Join(out, []byte("\n"))
}

func composeServiceKey(line string) (string, bool) {
	if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
		return "", false
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, ":") || strings.Contains(trimmed, " ") {
		return "", false
	}
	name := strings.TrimSuffix(trimmed, ":")
	switch name {
	case "services", "networks", "volumes", "name":
		return "", false
	}
	return name, true
}

var coolifyHubLink = regexp.MustCompile(`<li><a href="http://coolify\.[^"]+">Coolify</a></li>`)

func volumeKey(line string) string {
	if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
		return ""
	}
	trimmed := strings.TrimSpace(line)
	if i := strings.Index(trimmed, ":"); i > 0 {
		return strings.TrimSpace(trimmed[:i])
	}
	return ""
}

var _ UnitRenderer = liteBoundRenderer{}
var _ UnitRenderer = basementCoreLiteTerramateRenderer{}
