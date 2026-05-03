package kitio

// Section-to-service-group mappings. These mirror the constants in the
// kit-import endpoint (kombify-Administration/.../kit-import/+server.ts:32-79)
// so that yaml -> KitDefinition -> POST body and POST body -> KitDefinition
// -> yaml stay symmetric.
//
// Forward maps: yaml section key -> sk_service_group.slug.
// Reverse maps: sk_service_group.slug -> yaml section key + section name.

// ApplicationToGroup maps stackkit.yaml `application.<key>` to sk_service_group.slug.
// Pre-2026-04 named UseCaseToGroup with key `useCases.<key>`. See migration 000084.
var ApplicationToGroup = map[string]string{
	"photos":     "photo-management",
	"media":      "media-streaming",
	"vault":      "password-vault",
	"smart-home": "smart-home",
	"files":      "file-sharing",
	"ai":         "ai-workloads",
	"dev":        "dev-platform",
	"mail":       "mail-server",
	"game":       "game-server",
	"remote":     "remote-desktop",
}

// FoundationToGroup maps stackkit.yaml `foundation.<key>` to sk_service_group.slug.
//
// Note (migration 000085): `login-gateway` was previously here. Per
// ARCHITECTURE_V6 §4, login-gateway (TinyAuth + PocketID) is L2 Platform,
// not L1 Foundation — the admin bootstrap step is Foundation, the identity
// service itself is Platform. login-gateway moved to PlatformToGroup.
var FoundationToGroup = map[string]string{
	"security-baseline": "security-baseline",
	"admin-bootstrap":   "admin-bootstrap",
}

// PlatformToGroup maps stackkit.yaml `platform.<key>` to sk_service_group.slug.
var PlatformToGroup = map[string]string{
	"traefik":       "reverse-proxy",
	"tinyauth":      "forward-auth",
	"login-gateway": "forward-auth",
	"pocketid":      "oidc-provider",
	"paas":          "paas",
	"monitoring":    "uptime-monitoring",
	"dashboard":     "dashboard",
	"dozzle":        "container-logs",
	"socket-proxy":  "docker-socket-proxy",
	"unbound":       "dns-resolver",
	"adguard-home":  "dns-filter",
	"crowdsec":      "intrusion-detection",
}

// SectionKind identifies which yaml section a service-group belongs to.
type SectionKind string

const (
	SectionApplication SectionKind = "application"
	SectionFoundation  SectionKind = "foundation"
	SectionPlatform    SectionKind = "platform"
)

// ReverseMapping records the original section + key that produced a given
// service-group slug. Used by export to put selections back into the right
// stackkit.yaml section.
type ReverseMapping struct {
	Section  SectionKind
	Key      string
}

// reverseMap is the inverse of the three forward maps. Built once at init.
var reverseMap map[string][]ReverseMapping

func init() {
	reverseMap = make(map[string][]ReverseMapping)
	for k, group := range FoundationToGroup {
		reverseMap[group] = append(reverseMap[group], ReverseMapping{Section: SectionFoundation, Key: k})
	}
	for k, group := range PlatformToGroup {
		reverseMap[group] = append(reverseMap[group], ReverseMapping{Section: SectionPlatform, Key: k})
	}
	for k, group := range ApplicationToGroup {
		reverseMap[group] = append(reverseMap[group], ReverseMapping{Section: SectionApplication, Key: k})
	}
}

// GroupReverseMappings returns the section/key pairs that map to a given
// service-group slug. Some groups (e.g. "forward-auth") have multiple sources
// (foundation.login-gateway + platform.tinyauth) — both are returned.
func GroupReverseMappings(groupSlug string) []ReverseMapping {
	if m, ok := reverseMap[groupSlug]; ok {
		out := make([]ReverseMapping, len(m))
		copy(out, m)
		return out
	}
	return nil
}

// PreferredSection picks the canonical section for a group when multiple
// sources exist. Rule: foundation > platform > application. Within the
// same section, we prefer the SHORTER key, then lexicographically earlier
// — this keeps `login-gateway` (the documented canonical) winning over
// `tinyauth` (the legacy alias) for forward-auth, deterministically.
//
// Migration 000086 (P1-2 fix): login-gateway moved from foundation to
// platform per ARCHITECTURE_V6 §4. Both login-gateway and tinyauth are
// now in platform; the deterministic-within-section rule keeps the
// canonical pair stable.
func PreferredSection(groupSlug string) (SectionKind, string, bool) {
	mappings := GroupReverseMappings(groupSlug)
	if len(mappings) == 0 {
		return "", "", false
	}
	// Priority order across sections
	for _, kind := range []SectionKind{SectionFoundation, SectionPlatform, SectionApplication} {
		var best *ReverseMapping
		for i := range mappings {
			m := mappings[i]
			if m.Section != kind {
				continue
			}
			if best == nil {
				best = &mappings[i]
				continue
			}
			// Prefer shorter key, then lexicographic. login-gateway (13 chars)
			// loses to tinyauth (8 chars) on length alone, so override:
			// `forward-auth` group canonically uses `login-gateway` per V6 docs.
			// Use the explicit canonical-preference list instead of relying
			// on length, since length is a fragile heuristic.
			if comparePreferred(m.Key, best.Key) < 0 {
				best = &mappings[i]
			}
		}
		if best != nil {
			return best.Section, best.Key, true
		}
	}
	return mappings[0].Section, mappings[0].Key, true
}

// comparePreferred returns <0 if a should win over b within the same section.
// Canonical names from ARCHITECTURE_V6 §4 always win; otherwise lex order.
var canonicalKeyPriority = map[string]int{
	"login-gateway": -100, // V6-canonical for forward-auth
	"traefik":       -90,  // V6-canonical for reverse-proxy
	"pocketid":      -80,  // V6-canonical for oidc-provider
}

func comparePreferred(a, b string) int {
	pa, ok := canonicalKeyPriority[a]
	if !ok {
		pa = 0
	}
	pb, ok := canonicalKeyPriority[b]
	if !ok {
		pb = 0
	}
	if pa != pb {
		return pa - pb
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
