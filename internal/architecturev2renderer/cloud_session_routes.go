package architecturev2renderer

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"crypto/sha256"
	"encoding/hex"
	"github.com/kombifyio/stackkits/internal/addressplan"
	"gopkg.in/yaml.v3"
)

func cloudDefaultSessionTopology(domain, prefix string) addressplan.SessionTopology {
	host := func(service string) string {
		if prefix != "" {
			return prefix + "-" + service + "." + domain
		}
		return service + "." + domain
	}
	return addressplan.SessionTopology{PocketIDHost: host("id"), TinyAuthHost: host("auth"), HubHost: host("base")}
}

// CloudSessionRoutingOverlay contains only the public changes for the two
// session consumers. The caller authenticates the retained source separately;
// it cannot upgrade images, change volumes, or replace the PocketID service.
func CloudSessionRoutingOverlay(source []byte, before, after addressplan.SessionTopology) ([]byte, error) {
	return CloudSessionRoutingComponentOverlay(source, before, after, "")
}

// CloudSessionRoutingComponentOverlay admits only the current CUE-projected
// TinyAuth image. Storage rollback is authorized by the signed node journal.
func CloudSessionRoutingComponentOverlay(source []byte, before, after addressplan.SessionTopology, image string) ([]byte, error) {
	if image != "" {
		admitted := false
		for _, component := range CloudStandaloneCoreServiceContracts() {
			if component.Ref == "tinyauth" && image == component.ImageRef+"@"+component.ImageDigest {
				admitted = true
			}
		}
		if !admitted {
			return nil, fmt.Errorf("Cloud session component image is outside the CUE projection")
		}
	}
	if before.Validate() != nil || after.ValidateTransitionFrom(before) != nil {
		return nil, fmt.Errorf("Cloud session overlay has invalid route intent")
	}
	var document struct {
		Services map[string]struct {
			Labels      []string       `yaml:"labels"`
			Environment map[string]any `yaml:"environment"`
			Command     []string       `yaml:"command"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(source, &document) != nil {
		return nil, fmt.Errorf("Cloud session overlay requires a retained Compose graph")
	}
	services := map[string]any{}
	for _, service := range []string{"hub", "tinyauth"} {
		current, ok := document.Services[service]
		if !ok {
			return nil, fmt.Errorf("Cloud session overlay requires both session consumers")
		}
		oldHost, newHost, router := before.HubHost, after.HubHost, "hub"
		if service == "tinyauth" {
			oldHost, newHost, router = before.TinyAuthHost, after.TinyAuthHost, "tinyauth"
		}
		labels := map[string]string{}
		matched := false
		for _, label := range current.Labels {
			key, value, ok := strings.Cut(label, "=")
			if !ok {
				return nil, fmt.Errorf("Cloud session source labels are malformed")
			}
			if key == "traefik.http.routers."+router+".rule" {
				if value != "Host(`"+oldHost+"`)" {
					return nil, fmt.Errorf("Cloud session source route differs from retained intent")
				}
				value, matched = "Host(`"+newHost+"`)", true
			}
			labels[key] = value
		}
		if !matched {
			return nil, fmt.Errorf("Cloud session source route is missing")
		}
		// Only the old Hub gets a redirect. Old OAuth callbacks are left
		// unrouted because their transaction cookies belong to the old host.
		if service == "hub" {
			labels["traefik.http.routers.stackkit-session-hub-redirect.rule"] = "Host(`" + oldHost + "`)"
			labels["traefik.http.routers.stackkit-session-hub-redirect.entrypoints"] = labels["traefik.http.routers.hub.entrypoints"]
			labels["traefik.http.routers.stackkit-session-hub-redirect.tls"] = "true"
			labels["traefik.http.routers.stackkit-session-hub-redirect.middlewares"] = "stackkit-session-hub-redirect@docker"
			labels["traefik.http.routers.stackkit-session-hub-redirect.service"] = "hub"
			labels["traefik.http.middlewares.stackkit-session-hub-redirect.redirectregex.regex"] = "^https://[^/]+/(.*)"
			labels["traefik.http.middlewares.stackkit-session-hub-redirect.redirectregex.replacement"] = "https://" + newHost + "/$${1}"
			labels["traefik.http.middlewares.stackkit-session-hub-redirect.redirectregex.permanent"] = "true"
		}
		entry := map[string]any{"labels": labels}
		if service == "tinyauth" && image != "" {
			entry["image"] = image
		}
		if service == "hub" {
			if len(current.Command) != 3 || current.Command[0] != "/bin/sh" || current.Command[1] != "-ec" {
				return nil, fmt.Errorf("Cloud session source Hub command differs from the retained core")
			}
			command := append([]string(nil), current.Command...)
			command[2] = strings.NewReplacer(before.TinyAuthHost, after.TinyAuthHost, before.HubHost, after.HubHost).Replace(command[2])
			entry["command"] = command
		} else {
			environment := map[string]any{
				"TINYAUTH_APPURL": "https://" + after.TinyAuthHost,
				"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL": after.CallbackURL(),
				"TINYAUTH_AUTH_ACLS_POLICY":                     "deny",
				"TINYAUTH_LABELPROVIDER":                        "none",
				"TINYAUTH_OAUTH_PROVIDERS_POCKETID_WHITELIST":   "/.*/",
			}
			for key := range current.Environment {
				if tinyAuthRouteACLEnvironmentPattern.MatchString(key) {
					environment[key] = nil
				}
			}
			sum := sha256.Sum256([]byte(after.HubHost))
			key := "TINYAUTH_APPS_ROUTE" + strings.ToUpper(hex.EncodeToString(sum[:]))
			environment[key+"_CONFIG_DOMAIN"] = after.HubHost
			environment[key+"_OAUTH_WHITELIST"] = "/.*/"
			environment[key+"_OAUTH_GROUPS"] = "owners,admins"
			entry["environment"] = environment
		}
		services[service] = entry
	}
	return yaml.Marshal(map[string]any{"services": services})
}

func cloudSessionTopology(unit RenderUnit) (addressplan.SessionTopology, error) {
	domain, _ := unit.NetworkDomainBase()
	prefix, _ := unit.NetworkSubdomainPrefix()
	before := cloudDefaultSessionTopology(domain, prefix)
	topology := before
	var values coreRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return topology, err
	}
	seen := map[string]bool{}
	for _, route := range values.TinyAuthRouteACLs {
		if route.ModuleRef != unit.ModuleID() || route.Exposure != "public" {
			continue
		}
		var target *string
		switch route.ServiceRef {
		case "id":
			target = &topology.PocketIDHost
		case "auth":
			target = &topology.TinyAuthHost
		case "base":
			target = &topology.HubHost
		default:
			continue
		}
		if seen[route.ServiceRef] && *target != route.Host {
			return topology, fmt.Errorf("Cloud core service has conflicting public route hosts")
		}
		seen[route.ServiceRef], *target = true, route.Host
	}
	if topology == before {
		return topology, topology.Validate()
	}
	if unit.ModuleID() != cloudStandaloneCoreModuleID || domain != "kombify.me" || prefix == "" {
		return topology, fmt.Errorf("Cloud core route override requires the standalone session transition")
	}
	return topology, topology.ValidateTransitionFrom(before)
}

func renderCloudSessionTopology(content []byte, before, after addressplan.SessionTopology) []byte {
	if before == after {
		return content
	}
	content = []byte(strings.NewReplacer(before.TinyAuthHost, after.TinyAuthHost, before.HubHost, after.HubHost).Replace(string(content)))
	// Environment overrides follow both immutable custody env files. Only
	// public URLs move; client identity, secret and issuer stay in their seals.
	anchor := regexp.MustCompile(`(?m)^([ ]+)TINYAUTH_LABELPROVIDER: none\n`).FindSubmatchIndex(content)
	if len(anchor) != 4 {
		return nil
	}
	indent := string(content[anchor[2]:anchor[3]])
	lines := fmt.Sprintf("%sTINYAUTH_APPURL: %q\n%sTINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL: %q\n", indent, "https://"+after.TinyAuthHost, indent, after.CallbackURL())
	return bytes.Replace(content, content[anchor[0]:anchor[1]], append(append([]byte(nil), content[anchor[0]:anchor[1]]...), []byte(lines)...), 1)
}

func cloudArtifactSessionTopology(content []byte, before addressplan.SessionTopology) (addressplan.SessionTopology, bool) {
	topology := before
	for _, field := range []struct {
		router string
		target *string
	}{{"tinyauth", &topology.TinyAuthHost}, {"hub", &topology.HubHost}} {
		matches := regexp.MustCompile("routers[.]"+field.router+"[.]rule=Host\\(`([a-z0-9.-]+)`\\)").FindAllSubmatch(content, -1)
		if len(matches) != 1 {
			return topology, false
		}
		*field.target = string(matches[0][1])
	}
	return topology, topology == before || topology.ValidateTransitionFrom(before) == nil
}

// CloudComposeSessionTopology projects the three core routes without granting
// migration authority. The native lifecycle separately admits signed changes.
func CloudComposeSessionTopology(content []byte) (addressplan.SessionTopology, bool) {
	domain, prefix, ok := CloudComposeIdentityAddress(content)
	if !ok {
		return addressplan.SessionTopology{}, false
	}
	return cloudArtifactSessionTopology(content, cloudDefaultSessionTopology(domain, prefix))
}
