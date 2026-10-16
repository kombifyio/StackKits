package addressplan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var sessionHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// SessionTopology is a projection of canonical public routes. PocketID can
// retain its enrolled WebAuthn origin when the session consumers move together.
type SessionTopology struct {
	PocketIDHost string `json:"pocketIdHost"`
	TinyAuthHost string `json:"tinyAuthHost"`
	HubHost      string `json:"hubHost"`
}

func (t SessionTopology) Validate() error {
	seen := map[string]bool{}
	for _, host := range []string{t.PocketIDHost, t.TinyAuthHost, t.HubHost} {
		if len(host) > 253 || !sessionHostPattern.MatchString(host) || seen[host] {
			return fmt.Errorf("session routes require distinct canonical DNS hosts")
		}
		seen[host] = true
	}
	return nil
}

func (t SessionTopology) CallbackURL() string {
	return "https://" + t.TinyAuthHost + "/api/oauth/callback/pocketid"
}

// ValidateTransitionFrom admits only a sibling session pair in one private
// kombify.me installation zone, with the exact enrolled issuer retained.
func (t SessionTopology) ValidateTransitionFrom(before SessionTopology) error {
	if err := t.Validate(); err != nil {
		return err
	}
	zone := strings.TrimPrefix(t.TinyAuthHost, "auth.")
	label := strings.TrimSuffix(zone, ".kombify.me")
	if t.PocketIDHost != before.PocketIDHost || zone == t.TinyAuthHost ||
		t.HubHost != "base."+zone || zone != label+".kombify.me" ||
		!prefixPattern.MatchString(label) || len(label) > 63 {
		return fmt.Errorf("session routes must retain PocketID and share one private kombify.me installation zone")
	}
	return nil
}

func SessionTopologyFromCanonical(canonical []byte) (SessionTopology, error) {
	plan, err := FromCanonicalStackSpec(canonical)
	if err != nil {
		return SessionTopology{}, err
	}
	var topology SessionTopology
	for _, service := range plan.Services {
		switch service.ServiceKey {
		case "id":
			topology.PocketIDHost = service.Host
		case "auth":
			topology.TinyAuthHost = service.Host
		case "base":
			topology.HubHost = service.Host
		}
	}
	return topology, topology.Validate()
}

// BindSessionZone changes only the two session consumers of an installed
// core-only Cloud kit. It never changes the issuer, network domain or prefix.
// As with BindZone, the caller must CUE-validate before persisting the candidate.
func BindSessionZone(canonical []byte, zone string) ([]byte, error) {
	zone = strings.ToLower(strings.TrimSpace(zone))
	if !prefixPattern.MatchString(zone) || len(zone) > 63 {
		return nil, fmt.Errorf("cloud_session_transition_invalid: session zone must be one canonical DNS label")
	}
	before, err := SessionTopologyFromCanonical(canonical)
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return nil, err
	}
	kit, _ := spec["kit"].(map[string]any)
	install, _ := spec["install"].(map[string]any)
	platform, _ := install["platform"].(map[string]any)
	workloads, _ := spec["workloads"].(map[string]any)
	core, _ := workloads["cloud-core"].(map[string]any)
	if kit["slug"] != "cloud-kit" || platform["management"] != "standalone" || len(workloads) != 1 || core["alternative"] != "standalone-compose" {
		return nil, fmt.Errorf("cloud_session_transition_unsupported: session route transition requires an installed standalone core-only Cloud kit")
	}
	network, _ := spec["network"].(map[string]any)
	domain, _ := network["domain"].(map[string]any)
	if domain["base"] != "kombify.me" || domain["subdomainPrefix"] == nil {
		return nil, fmt.Errorf("cloud_session_transition_unsupported: session route transition requires the retained flat kombify.me layout")
	}
	zoneDomain := zone + ".kombify.me"
	routes, _ := spec["routes"].(map[string]any)
	if len(routes) != 3 {
		return nil, fmt.Errorf("cloud_session_transition_unsupported: session route transition requires exactly the three core public routes")
	}
	for _, raw := range routes {
		route, _ := raw.(map[string]any)
		if route["exposure"] != "public" || route["moduleRef"] != "stackkits-cloud-core-standalone-runtime" {
			return nil, fmt.Errorf("cloud_session_transition_unsupported: session route transition refuses non-core routes")
		}
		switch route["serviceRef"] {
		case "auth":
			route["host"] = "auth." + zoneDomain
		case "base":
			route["host"] = "base." + zoneDomain
		case "id":
		default:
			return nil, fmt.Errorf("cloud_session_transition_unsupported: session route transition refuses non-core services")
		}
	}
	candidate, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	after, err := SessionTopologyFromCanonical(candidate)
	if err != nil || after.PocketIDHost != before.PocketIDHost {
		return nil, fmt.Errorf("session route transition must preserve the enrolled PocketID origin")
	}
	return candidate, nil
}
