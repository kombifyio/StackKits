package architecturev2renderer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"gopkg.in/yaml.v3"
)

const (
	tinyAuthRouteACLInputRef     = "tinyauth-route-acls"
	tinyAuthRouteACLContractSalt = "|authorization:tinyauth-v5-static-exact-host-groups,unknown-host-deny,no-docker-provider,pocketid-provider-email-admission"
)

var tinyAuthRouteACLEnvironmentPattern = regexp.MustCompile(`^TINYAUTH_APPS_ROUTE([A-F0-9]{64})_(CONFIG_DOMAIN|OAUTH_WHITELIST|OAUTH_GROUPS)$`)

type coreRuntimeValues struct {
	BackupSource      localbackuppolicy.BackupSourceProjection `json:"backup-source"`
	TinyAuthRouteACLs []rawPublicServiceRouteV4                `json:"tinyauth-route-acls"`
}

func validateTinyAuthRouteACLBinding(raw []byte, includeBackup bool, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode core runtime input bindings", err)
	}
	want := 1
	if includeBackup {
		want++
	}
	if len(bindings) != want {
		return fail(ErrInvalidPlan, path, "requires the exact compiler-owned route ACL bindings")
	}
	seenBackup, seenACL := false, false
	for _, binding := range bindings {
		switch binding.TargetRef {
		case "backup-source":
			seenBackup = includeBackup && binding.SourceRef == "backup.localKopiaSource" &&
				binding.ValueType == "local-kopia-backup-source-v1" && binding.Cardinality == "single" &&
				binding.Required && len(binding.DefaultValue) == 0
		case tinyAuthRouteACLInputRef:
			seenACL = binding.SourceRef == "network.routes" &&
				binding.ValueType == "authority-bound-service-route-list-v4" && binding.Cardinality == "list" &&
				binding.Required && len(binding.DefaultValue) == 0
		default:
			return fail(ErrInvalidPlan, path, "contains an ungoverned core runtime input binding")
		}
	}
	if seenACL && seenBackup == includeBackup {
		return nil
	}
	return fail(ErrInvalidPlan, path, "does not match the governed route ACL bindings")
}

func tinyAuthRouteACLGroups(privilege string) (string, bool) {
	switch privilege {
	case "user":
		return "owners,admins,household", true
	case "admin", "identity", "secrets", "vault", "recovery":
		return "owners,admins", true
	default:
		return "", false
	}
}

func tinyAuthRouteACLs(unit RenderUnit) (map[string]string, error) {
	var values coreRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, wrap(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".values", "decode compiler-owned route ACLs", err)
	}
	groupsByHost := map[string]string{}
	var endpoints []rawModuleServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil {
		return nil, wrap(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".serviceEndpoints", "decode core route privileges", err)
	}
	domain, _ := unit.NetworkDomainBase()
	prefix, _ := unit.NetworkSubdomainPrefix()
	for _, endpoint := range endpoints {
		if endpoint.IngressAuth != "forward-auth" {
			continue
		}
		service := endpoint.ServiceRef
		if service == "basement-hub" {
			service = "base"
		}
		host := service + "." + domain
		if prefix != "" {
			host = prefix + "-" + service + "." + domain
		}
		groups, ok := tinyAuthRouteACLGroups(endpoint.RequiredPrivilege)
		if !ok || !basementDomainPattern.MatchString(host) {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".serviceEndpoints", "core route %q has no enforceable TinyAuth host/privilege policy", endpoint.ServiceRef)
		}
		groupsByHost[host] = groups
	}
	for _, route := range values.TinyAuthRouteACLs {
		if route.IngressAuth != "forward-auth" || (route.ModuleRef != unit.ModuleID() && route.CoreModuleRef != unit.ModuleID()) {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(route.Host))
		groups, ok := tinyAuthRouteACLGroups(route.Access.Privilege)
		if !ok || host == "" || !basementDomainPattern.MatchString(host) || host != route.Host {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".values."+tinyAuthRouteACLInputRef, "forward-auth route %q has no enforceable TinyAuth host/privilege policy", route.ID)
		}
		if existing, exists := groupsByHost[host]; exists && existing != groups {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".values."+tinyAuthRouteACLInputRef, "host %q has conflicting route privileges", host)
		}
		groupsByHost[host] = groups
	}
	if len(groupsByHost) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".values."+tinyAuthRouteACLInputRef, "contains no forward-auth route owned by this core")
	}
	return groupsByHost, nil
}

func renderTinyAuthRouteACLs(unit RenderUnit, compose []byte) ([]byte, error) {
	groupsByHost, err := tinyAuthRouteACLs(unit)
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(groupsByHost))
	for host := range groupsByHost {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	var lines []string
	for _, host := range hosts {
		sum := sha256.Sum256([]byte(host))
		// The app name is longer than a legal DNS label. TinyAuth's secondary
		// app-name prefix lookup therefore cannot admit any host; only the exact
		// configured domain can select this ACL.
		key := "TINYAUTH_APPS_ROUTE" + strings.ToUpper(hex.EncodeToString(sum[:]))
		lines = append(lines,
			fmt.Sprintf("      %s_CONFIG_DOMAIN: %q", key, host),
			fmt.Sprintf("      %s_OAUTH_WHITELIST: %q", key, "/.*/"),
			fmt.Sprintf("      %s_OAUTH_GROUPS: %q", key, groupsByHost[host]),
		)
	}
	marker := []byte("      # stackkit-tinyauth-route-acls\n")
	if bytes.Count(compose, marker) == 1 {
		return bytes.Replace(compose, marker, append(marker, []byte(strings.Join(lines, "\n")+"\n")...), 1), nil
	}
	// The standalone Cloud graph is derived through YAML decoding, which
	// intentionally drops comments. Its closed label-provider line remains an
	// exact, unique insertion point.
	anchors := regexp.MustCompile(`(?m)^([ ]+)TINYAUTH_LABELPROVIDER: none\n`).FindAllSubmatchIndex(compose, -1)
	if len(anchors) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.tinyauth-route-acls", "Compose template lacks the exact TinyAuth ACL insertion point")
	}
	match := anchors[0]
	indent := string(compose[match[2]:match[3]])
	for index, line := range lines {
		lines[index] = indent + strings.TrimPrefix(line, "      ")
	}
	anchor := compose[match[0]:match[1]]
	insertion := append(append([]byte(nil), anchor...), []byte(strings.Join(lines, "\n")+"\n")...)
	return bytes.Replace(compose, anchor, insertion, 1), nil
}

func stripTinyAuthRouteACLs(content []byte) ([]byte, bool) {
	var document struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(content, &document) != nil {
		return nil, false
	}
	environment := document.Services["tinyauth"].Environment
	seen := map[string]map[string]string{}
	for key, value := range environment {
		match := tinyAuthRouteACLEnvironmentPattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		if seen[match[1]] == nil {
			seen[match[1]] = map[string]string{}
		}
		seen[match[1]][match[2]] = value
	}
	for hash, fields := range seen {
		host, ok := fields["CONFIG_DOMAIN"]
		if !ok || len(fields) != 3 || !basementDomainPattern.MatchString(host) || host != strings.ToLower(host) ||
			fields["OAUTH_WHITELIST"] != "/.*/" || (fields["OAUTH_GROUPS"] != "owners,admins" && fields["OAUTH_GROUPS"] != "owners,admins,household") {
			return nil, false
		}
		sum := sha256.Sum256([]byte(host))
		if hash != strings.ToUpper(hex.EncodeToString(sum[:])) {
			return nil, false
		}
	}
	lines := bytes.Split(content, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "TINYAUTH_APPS_ROUTE") {
			key := strings.SplitN(trimmed, ":", 2)[0]
			if tinyAuthRouteACLEnvironmentPattern.MatchString(key) {
				continue
			}
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n")), true
}
