package coolify

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Exact pinned Coolify 4.7.5+ runtime and sidecar image coordinates matching
// foundation/architecture_v2_marketplace.cue and core_stackkit_server.go.
const (
	CoolifyPinnedVersion          = "4.7.5"
	CoolifyPinnedImageRef         = "coollabsio/coolify:4.7.5"
	CoolifyPinnedImageDigest      = "sha256:ac2259a438f0221e73eca2838c0c929ff2fd304f8345daabc85c910b6c83d6ee"
	CoolifyPostgresImageRef       = "postgres:15-alpine"
	CoolifyPostgresImageDigest    = "sha256:f7d23353e1b15400d22ebe31189f4d314b87a4c129cc400c8c2d8d4ca127bf81"
	CoolifyRedisImageRef          = "redis:7-alpine"
	CoolifyRedisImageDigest       = "sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499"
	CookieScrubberBaseImageRef    = "docker.io/library/alpine:3.24"
	CookieScrubberBaseImageDigest = "sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6"
	CoolifyProxyImageRef          = "traefik:v3.6.13"

	NetworkBasementCore           = "basement-core"
	NetworkCoolifyEdge            = "coolify-edge"
	CookieScrubberServiceName     = "coolify-cookie-scrubber"
	CookieScrubberDefaultListen   = ":8085"
	CookieScrubberDefaultUpstream = "http://coolify-proxy:80"
)

// ProxyValidationOptions allows an operator/caller to explicitly mark
// independently verified external env_file paths or config/volume mount source
// names/paths.
// Zero-value ProxyValidationOptions fails closed on every non-empty env_file,
// secrets/configs declaration, and Traefik/ACME volume mount.
type ProxyValidationOptions struct {
	VerifiedEnvFiles     map[string]bool
	VerifiedConfigMounts map[string]bool
}

// ValidateProxyCompose parses Coolify's rendered proxy Docker Compose YAML and
// fails closed if ANY service:
//   - binds host port 80 or 443 (short syntax, port ranges, or long syntax),
//   - uses network_mode: host,
//   - enables ACME / certificatesresolvers / Let's Encrypt or external Traefik
//     configfile/file-provider flags, environment variables, or labels, or
//   - references unverified external config/secret sources (env_file, secrets,
//     configs, extends, top-level include, or mounted traefik.yml/traefik.toml/
//     acme/letsencrypt files) unless explicitly empty or verified.
//
// Diagnostic errors NEVER include raw environment variable values or full
// rejected tokens — only service name, field name, and setting/key name.
//
// Kit-Traefik + Access Gate is the sole owner of host ports 80/443 and TLS
// certificates (ADR-0054 §3).
func ValidateProxyCompose(yamlBytes []byte) error {
	return ValidateProxyComposeWithOptions(yamlBytes, ProxyValidationOptions{})
}

// ValidateProxyComposeWithOptions validates a Compose YAML document with
// optional explicitly verified external env_file/config mount allowlists.
func ValidateProxyComposeWithOptions(yamlBytes []byte, opts ProxyValidationOptions) error {
	if len(strings.TrimSpace(string(yamlBytes))) == 0 {
		return errors.New("coolify proxy guard: compose document is empty")
	}
	var doc struct {
		Include  any `yaml:"include"`
		Services map[string]struct {
			NetworkMode string `yaml:"network_mode"`
			Ports       []any  `yaml:"ports"`
			Command     any    `yaml:"command"`
			Entrypoint  any    `yaml:"entrypoint"`
			Environment any    `yaml:"environment"`
			Labels      any    `yaml:"labels"`
			EnvFile     any    `yaml:"env_file"`
			Volumes     []any  `yaml:"volumes"`
			Configs     []any  `yaml:"configs"`
			Secrets     []any  `yaml:"secrets"`
			Extends     any    `yaml:"extends"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return fmt.Errorf("coolify proxy guard: failed to parse proxy compose YAML: %w", err)
	}
	if hasNonEmptyValue(doc.Include) {
		return errors.New("coolify proxy guard: top-level field \"include\" declares unverified external compose source")
	}
	if len(doc.Services) == 0 {
		return errors.New("coolify proxy guard: compose document defines no services")
	}

	serviceNames := make([]string, 0, len(doc.Services))
	for svcName := range doc.Services {
		serviceNames = append(serviceNames, svcName)
	}
	sort.Strings(serviceNames)

	for _, svcName := range serviceNames {
		svc := doc.Services[svcName]
		if hasNonEmptyValue(svc.Extends) {
			return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external compose inheritance", svcName, "extends")
		}
		mode := strings.ToLower(strings.TrimSpace(svc.NetworkMode))
		if mode == "host" || strings.HasPrefix(mode, "host:") {
			return fmt.Errorf("coolify proxy guard: service %q field %q uses forbidden host network mode", svcName, "network_mode")
		}
		for _, portEntry := range svc.Ports {
			if err := validatePortEntry(svcName, portEntry); err != nil {
				return err
			}
		}
		if err := validateEnvFileField(svcName, svc.EnvFile, opts.VerifiedEnvFiles); err != nil {
			return err
		}
		if len(svc.Secrets) > 0 {
			return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external secret source", svcName, "secrets")
		}
		for _, cfgEntry := range svc.Configs {
			if err := validateConfigEntry(svcName, cfgEntry, opts.VerifiedConfigMounts); err != nil {
				return err
			}
		}
		for _, volEntry := range svc.Volumes {
			if err := validateVolumeEntry(svcName, volEntry, opts.VerifiedConfigMounts); err != nil {
				return err
			}
		}
		if err := validateCommandField(svcName, "command", svc.Command); err != nil {
			return err
		}
		if err := validateCommandField(svcName, "entrypoint", svc.Entrypoint); err != nil {
			return err
		}
		if err := validateKeyValueField(svcName, "environment", svc.Environment); err != nil {
			return err
		}
		if err := validateKeyValueField(svcName, "labels", svc.Labels); err != nil {
			return err
		}
	}
	return nil
}

// ValidateEffectiveProxyDeployment merges baseYAML with one or more Compose
// overlay YAML documents using Docker Compose merge semantics (additive lists
// such as ports, volumes, env_file, configs, secrets unless explicitly reset;
// map merges for environment/labels; inherited scalar/command/entrypoint
// fields unless overridden) and validates the resulting effective deployment
// fail-closed against ValidateProxyCompose.
func ValidateEffectiveProxyDeployment(baseYAML []byte, overlays ...[]byte) error {
	return ValidateEffectiveProxyDeploymentWithOptions(ProxyValidationOptions{}, baseYAML, overlays...)
}

// ValidateEffectiveProxyDeploymentWithOptions merges baseYAML with overlays
// and validates the effective deployment with opts.
func ValidateEffectiveProxyDeploymentWithOptions(opts ProxyValidationOptions, baseYAML []byte, overlays ...[]byte) error {
	merged, err := MergeComposeDocuments(baseYAML, overlays...)
	if err != nil {
		return err
	}
	return ValidateProxyComposeWithOptions(merged, opts)
}

// MergeComposeDocuments merges baseYAML with zero or more overlay YAML
// documents following Docker Compose merge semantics and returns the effective
// merged Compose YAML.
func MergeComposeDocuments(baseYAML []byte, overlays ...[]byte) ([]byte, error) {
	if len(bytes.TrimSpace(baseYAML)) == 0 {
		return nil, errors.New("coolify proxy guard: base compose document is empty")
	}
	effective, err := parseComposeMappingNode(baseYAML)
	if err != nil {
		return nil, fmt.Errorf("coolify proxy guard: failed to parse base compose YAML: %w", err)
	}
	for i, overlay := range overlays {
		if len(bytes.TrimSpace(overlay)) == 0 {
			continue
		}
		ovNode, err := parseComposeMappingNode(overlay)
		if err != nil {
			return nil, fmt.Errorf("coolify proxy guard: failed to parse overlay compose YAML #%d: %w", i+1, err)
		}
		var mergeErr error
		effective, mergeErr = mergeTopLevelCompose(effective, ovNode)
		if mergeErr != nil {
			return nil, mergeErr
		}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(effective); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("coolify proxy guard: failed to encode merged compose YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func parseComposeMappingNode(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, errors.New("compose document is empty")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("compose document root must be a YAML mapping")
	}
	return cloneYAMLNode(root), nil
}

func cloneYAMLNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = cloneYAMLNode(c)
		}
	}
	return &cp
}

func isResetOrOverrideNode(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	return n.Tag == "!reset" || n.Tag == "!override"
}

func clearComposeResetTag(n *yaml.Node) *yaml.Node {
	cp := cloneYAMLNode(n)
	if cp.Tag == "!reset" || cp.Tag == "!override" {
		cp.Tag = ""
	}
	return cp
}

func mappingGet(m *yaml.Node, key string) (*yaml.Node, int) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

func mappingSet(m *yaml.Node, key string, val *yaml.Node) {
	if _, idx := mappingGet(m, key); idx >= 0 {
		m.Content[idx+1] = val
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

func mergeTopLevelCompose(base, overlay *yaml.Node) (*yaml.Node, error) {
	merged := cloneYAMLNode(base)
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		key := overlay.Content[i].Value
		ovVal := overlay.Content[i+1]
		baseVal, _ := mappingGet(merged, key)
		switch key {
		case "services":
			if baseVal != nil && baseVal.Kind == yaml.MappingNode && ovVal.Kind == yaml.MappingNode && !isResetOrOverrideNode(ovVal) {
				mergedServices, err := mergeServicesMap(baseVal, ovVal)
				if err != nil {
					return nil, err
				}
				mappingSet(merged, key, mergedServices)
			} else {
				mappingSet(merged, key, clearComposeResetTag(ovVal))
			}
		case "networks":
			if baseVal != nil && baseVal.Kind == yaml.MappingNode && ovVal.Kind == yaml.MappingNode && !isResetOrOverrideNode(ovVal) {
				mappingSet(merged, key, mergeGenericMapping(baseVal, ovVal))
			} else {
				mappingSet(merged, key, clearComposeResetTag(ovVal))
			}
		case "include":
			if baseVal != nil && !isResetOrOverrideNode(ovVal) {
				mappingSet(merged, key, mergeAdditiveSequence(baseVal, ovVal))
			} else {
				mappingSet(merged, key, clearComposeResetTag(ovVal))
			}
		default:
			mappingSet(merged, key, clearComposeResetTag(ovVal))
		}
	}
	return merged, nil
}

func mergeServicesMap(base, overlay *yaml.Node) (*yaml.Node, error) {
	merged := cloneYAMLNode(base)
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		svcName := overlay.Content[i].Value
		ovSvc := overlay.Content[i+1]
		baseSvc, _ := mappingGet(merged, svcName)
		if baseSvc != nil && baseSvc.Kind == yaml.MappingNode && ovSvc.Kind == yaml.MappingNode && !isResetOrOverrideNode(ovSvc) {
			mergedSvc, err := mergeSingleService(svcName, baseSvc, ovSvc)
			if err != nil {
				return nil, err
			}
			mappingSet(merged, svcName, mergedSvc)
		} else {
			mappingSet(merged, svcName, clearComposeResetTag(ovSvc))
		}
	}
	return merged, nil
}

func mergeSingleService(svcName string, base, overlay *yaml.Node) (*yaml.Node, error) {
	merged := cloneYAMLNode(base)
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		field := overlay.Content[i].Value
		ovVal := overlay.Content[i+1]
		baseVal, _ := mappingGet(merged, field)

		if isResetOrOverrideNode(ovVal) {
			mappingSet(merged, field, clearComposeResetTag(ovVal))
			continue
		}

		switch field {
		case "ports", "volumes", "env_file", "configs", "secrets", "cap_drop", "security_opt", "expose":
			if baseVal != nil {
				mappingSet(merged, field, mergeAdditiveSequence(baseVal, ovVal))
			} else {
				mappingSet(merged, field, cloneYAMLNode(ovVal))
			}
		case "environment", "labels":
			if baseVal != nil {
				mergedKV, err := mergeKeyValueNodes(svcName, field, baseVal, ovVal)
				if err != nil {
					return nil, err
				}
				mappingSet(merged, field, mergedKV)
			} else {
				mappingSet(merged, field, cloneYAMLNode(ovVal))
			}
		case "networks":
			if baseVal != nil {
				if baseVal.Kind == yaml.MappingNode && ovVal.Kind == yaml.MappingNode {
					mappingSet(merged, field, mergeGenericMapping(baseVal, ovVal))
				} else {
					mappingSet(merged, field, mergeAdditiveSequence(baseVal, ovVal))
				}
			} else {
				mappingSet(merged, field, cloneYAMLNode(ovVal))
			}
		default:
			mappingSet(merged, field, cloneYAMLNode(ovVal))
		}
	}
	return merged, nil
}

func mergeGenericMapping(base, overlay *yaml.Node) *yaml.Node {
	merged := cloneYAMLNode(base)
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		mappingSet(merged, overlay.Content[i].Value, clearComposeResetTag(overlay.Content[i+1]))
	}
	return merged
}

func mergeAdditiveSequence(base, overlay *yaml.Node) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	appendNodeItems := func(n *yaml.Node) {
		if n == nil || n.Tag == "!!null" {
			return
		}
		if n.Kind == yaml.SequenceNode {
			for _, item := range n.Content {
				seq.Content = append(seq.Content, cloneYAMLNode(item))
			}
			return
		}
		seq.Content = append(seq.Content, cloneYAMLNode(n))
	}
	appendNodeItems(base)
	appendNodeItems(overlay)
	return seq
}

func mergeKeyValueNodes(service, field string, base, overlay *yaml.Node) (*yaml.Node, error) {
	orderedKeys := make([]string, 0)
	kv := make(map[string]*yaml.Node)
	ingest := func(n *yaml.Node) error {
		if n == nil || n.Tag == "!!null" {
			return nil
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i].Value
				if _, exists := kv[k]; !exists {
					orderedKeys = append(orderedKeys, k)
				}
				kv[k] = cloneYAMLNode(n.Content[i+1])
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					return fmt.Errorf("coolify proxy guard: service %q %s: non-string entry is not allowed", service, field)
				}
				k, v, hasEq := strings.Cut(item.Value, "=")
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				if _, exists := kv[k]; !exists {
					orderedKeys = append(orderedKeys, k)
				}
				if hasEq {
					kv[k] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
				} else {
					kv[k] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
				}
			}
		default:
			return fmt.Errorf("coolify proxy guard: service %q %s: unsupported value", service, field)
		}
		return nil
	}
	if err := ingest(base); err != nil {
		return nil, err
	}
	if err := ingest(overlay); err != nil {
		return nil, err
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range orderedKeys {
		out.Content = append(out.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			kv[k],
		)
	}
	return out, nil
}

func hasNonEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func validateEnvFileField(service string, raw any, verified map[string]bool) error {
	switch v := raw.(type) {
	case nil:
		return nil
	case string:
		p := strings.TrimSpace(v)
		if p == "" || verified[p] {
			return nil
		}
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external environment file", service, "env_file")
	case []any:
		for _, item := range v {
			if err := validateEnvFileItem(service, item, verified); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		return validateEnvFileItem(service, v, verified)
	default:
		return fmt.Errorf("coolify proxy guard: service %q field %q has unsupported env_file declaration", service, "env_file")
	}
}

func validateEnvFileItem(service string, item any, verified map[string]bool) error {
	switch e := item.(type) {
	case string:
		p := strings.TrimSpace(e)
		if p == "" || verified[p] {
			return nil
		}
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external environment file", service, "env_file")
	case map[string]any:
		rawPath, _ := e["path"].(string)
		p := strings.TrimSpace(rawPath)
		if p != "" && verified[p] {
			return nil
		}
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external environment file", service, "env_file")
	default:
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external environment file", service, "env_file")
	}
}

func validateConfigEntry(service string, entry any, verified map[string]bool) error {
	switch c := entry.(type) {
	case string:
		name := strings.TrimSpace(c)
		if name != "" && verified[name] {
			return nil
		}
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external config source", service, "configs")
	case map[string]any:
		src, _ := c["source"].(string)
		src = strings.TrimSpace(src)
		if src != "" && verified[src] {
			return nil
		}
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external config source", service, "configs")
	default:
		return fmt.Errorf("coolify proxy guard: service %q field %q declares unverified external config source", service, "configs")
	}
}

func validateVolumeEntry(service string, entry any, verified map[string]bool) error {
	var source, target string
	switch v := entry.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		parts := strings.Split(trimmed, ":")
		switch len(parts) {
		case 1:
			target = strings.TrimSpace(parts[0])
		default:
			source = strings.TrimSpace(parts[0])
			target = strings.TrimSpace(parts[1])
		}
	case map[string]any:
		if s, ok := v["source"].(string); ok {
			source = strings.TrimSpace(s)
		}
		if t, ok := v["target"].(string); ok {
			target = strings.TrimSpace(t)
		}
	default:
		return fmt.Errorf("coolify proxy guard: service %q field %q has unsupported volume entry", service, "volumes")
	}
	if source != "" && verified[source] {
		return nil
	}
	if isForbiddenProxyConfigMount(source) || isForbiddenProxyConfigMount(target) {
		return fmt.Errorf("coolify proxy guard: service %q field %q mounts unverified Traefik/ACME config or state", service, "volumes")
	}
	return nil
}

func isForbiddenProxyConfigMount(rawPath string) bool {
	p := strings.ToLower(strings.TrimSpace(rawPath))
	if p == "" {
		return false
	}
	cleaned := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	base := path.Base(cleaned)
	switch base {
	case "traefik.yml", "traefik.yaml", "traefik.toml", "traefik.json",
		"acme.json", "dynamic.yml", "dynamic.yaml", "dynamic.toml":
		return true
	}
	if cleaned == "/etc/traefik" || strings.HasPrefix(cleaned, "/etc/traefik/") ||
		cleaned == "etc/traefik" || strings.HasPrefix(cleaned, "etc/traefik/") {
		return true
	}
	for _, needle := range []string{"traefik.yml", "traefik.yaml", "traefik.toml", "acme", "letsencrypt", "certresolver"} {
		if strings.Contains(cleaned, needle) {
			return true
		}
	}
	return false
}

func validatePortEntry(service string, entry any) error {
	switch v := entry.(type) {
	case int:
		if v == 80 || v == 443 {
			return fmt.Errorf("coolify proxy guard: service %q binds forbidden host port %d", service, v)
		}
		return nil
	case uint64:
		if v == 80 || v == 443 {
			return fmt.Errorf("coolify proxy guard: service %q binds forbidden host port %d", service, v)
		}
		return nil
	case string:
		return validateShortPortSpec(service, v)
	case map[string]any:
		published, hasPublished := v["published"]
		if !hasPublished || published == nil {
			return nil
		}
		switch p := published.(type) {
		case int:
			if p == 80 || p == 443 {
				return fmt.Errorf("coolify proxy guard: service %q binds forbidden host port %d", service, p)
			}
			return nil
		case string:
			return validatePortRange(service, p)
		default:
			return fmt.Errorf("coolify proxy guard: service %q has unsupported published port type %T", service, published)
		}
	default:
		return fmt.Errorf("coolify proxy guard: service %q has unsupported port entry type %T", service, entry)
	}
}

func validateShortPortSpec(service, spec string) error {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil
	}
	// Strip protocol suffix (e.g. /tcp, /udp).
	if slash := strings.IndexByte(trimmed, '/'); slash >= 0 {
		trimmed = trimmed[:slash]
	}
	// Handle bracketed IPv6 host binding like [::]:443:443 or [fd00::1]:80:80.
	if strings.HasPrefix(trimmed, "[") {
		end := strings.IndexByte(trimmed, ']')
		if end < 0 {
			return fmt.Errorf("coolify proxy guard: service %q has malformed IPv6 port binding", service)
		}
		rest := strings.TrimPrefix(trimmed[end+1:], ":")
		parts := strings.Split(rest, ":")
		if len(parts) == 0 {
			return nil
		}
		return validatePortRange(service, parts[0])
	}
	parts := strings.Split(trimmed, ":")
	switch len(parts) {
	case 1:
		// Single port e.g. "80" or "80-82" publishes the same host port or
		// allocates; treat explicit 80/443 as forbidden fail-closed.
		return validatePortRange(service, parts[0])
	case 2:
		// host:container
		return validatePortRange(service, parts[0])
	case 3:
		// ip:host:container
		return validatePortRange(service, parts[1])
	default:
		return fmt.Errorf("coolify proxy guard: service %q has malformed port mapping", service)
	}
}

func validatePortRange(service, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	startStr, endStr, isRange := strings.Cut(trimmed, "-")
	startPort, err := strconv.Atoi(strings.TrimSpace(startStr))
	if err != nil {
		return fmt.Errorf("coolify proxy guard: service %q has non-numeric host port", service)
	}
	endPort := startPort
	if isRange {
		endPort, err = strconv.Atoi(strings.TrimSpace(endStr))
		if err != nil {
			return fmt.Errorf("coolify proxy guard: service %q has non-numeric host port range", service)
		}
	}
	if startPort > endPort {
		startPort, endPort = endPort, startPort
	}
	if (80 >= startPort && 80 <= endPort) || (443 >= startPort && 443 <= endPort) {
		return fmt.Errorf("coolify proxy guard: service %q binds forbidden host port range %q (Kit-Traefik owns 80/443)", service, raw)
	}
	return nil
}

var forbiddenACMESubstrings = []string{
	"certificatesresolvers",
	"certresolver",
	"letsencrypt",
	".acme.",
	"_acme_",
	"--acme",
	"acme.email",
	"acme.storage",
	"acme.httpchallenge",
	"acme.tlschallenge",
	"acme.dnschallenge",
	"acme.caserver",
	"configfile",
	"providers.file",
	"providers_file",
}

func containsForbiddenACMESubstring(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return false
	}
	for _, needle := range forbiddenACMESubstrings {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func formatForbiddenSettingError(service, field, settingName string) error {
	safeKey := sanitizeSettingName(field, settingName)
	return fmt.Errorf("coolify proxy guard: service %q field %q setting %q enables forbidden ACME/TLS or external proxy configuration", service, field, safeKey)
}

func sanitizeSettingName(field, rawKey string) string {
	k := strings.TrimSpace(rawKey)
	if idx := strings.IndexByte(k, '='); idx >= 0 {
		k = strings.TrimSpace(k[:idx])
	}
	if k == "" {
		return "<unnamed>"
	}
	// Never include URLs, credentials, or whitespace-bearing values in diagnostics.
	if strings.Contains(k, "://") || strings.Contains(k, "@") || strings.ContainsAny(k, " \t\r\n") {
		return "<redacted>"
	}
	if (field == "command" || field == "entrypoint") && !strings.HasPrefix(k, "-") {
		return "<arg>"
	}
	return k
}

func validateCommandField(service, field string, value any) error {
	tokens := extractCommandTokens(value)
	for _, tok := range tokens {
		keyPart, valPart, hasEq := strings.Cut(tok, "=")
		if containsForbiddenACMESubstring(keyPart) || (hasEq && containsForbiddenACMESubstring(valPart)) {
			return formatForbiddenSettingError(service, field, keyPart)
		}
	}
	return nil
}

func extractCommandTokens(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return strings.Fields(v)
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, extractCommandTokens(item)...)
		}
		return out
	default:
		return []string{fmt.Sprint(v)}
	}
}

func validateKeyValueField(service, field string, value any) error {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if containsForbiddenACMESubstring(k) || valueContainsForbiddenACME(v[k]) {
				return formatForbiddenSettingError(service, field, k)
			}
		}
		return nil
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("coolify proxy guard: service %q %s: non-string entry is not allowed", service, field)
			}
			k, val, hasEq := strings.Cut(s, "=")
			if containsForbiddenACMESubstring(k) || (hasEq && containsForbiddenACMESubstring(val)) {
				return formatForbiddenSettingError(service, field, k)
			}
		}
		return nil
	case string:
		for _, tok := range strings.Fields(v) {
			k, val, hasEq := strings.Cut(tok, "=")
			if containsForbiddenACMESubstring(k) || (hasEq && containsForbiddenACMESubstring(val)) {
				return formatForbiddenSettingError(service, field, k)
			}
		}
		return nil
	default:
		if containsForbiddenACMESubstring(fmt.Sprint(v)) {
			return formatForbiddenSettingError(service, field, "<unnamed>")
		}
		return nil
	}
}

func valueContainsForbiddenACME(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return containsForbiddenACMESubstring(v)
	case []any:
		return slices.ContainsFunc(v, valueContainsForbiddenACME)
	case map[string]any:
		for k, val := range v {
			if containsForbiddenACMESubstring(k) || valueContainsForbiddenACME(val) {
				return true
			}
		}
		return false
	default:
		return containsForbiddenACMESubstring(fmt.Sprint(v))
	}
}

// ProxyOverlayConfig configures the hardened Option-A HTTP-only internal proxy
// overlay and optional zero-custody cookie-scrubber sidecar.
type ProxyOverlayConfig struct {
	TrustBoundary       TrustBoundaryConfig
	ProxyImage          string
	ScrubberListen      string
	TinyAuthCookieNames []string
	StagedBinaryPath    string

	// BaseComposeYAML, when provided, is merged with the generated overlay and
	// validated via ValidateEffectiveProxyDeployment so inherited base hazards
	// (host ports 80/443, network_mode: host, ACME flags, unverified env_file
	// or mounted Traefik configs) fail closed before deployment.
	BaseComposeYAML []byte
}

// CookieScrubberSidecarConfig configures the isolated zero-custody
// cookie-scrubber container definition.
type CookieScrubberSidecarConfig struct {
	Image               string
	StagedBinaryPath    string
	ListenAddr          string
	UpstreamURL         string
	TinyAuthCookieNames []string
}

// RenderCookieScrubberSidecar returns the hardened zero-custody Compose
// service definition for stackkit-server --mode=cookie-scrubber:
//   - pinned alpine base image
//   - non-root, read-only rootfs, cap_drop ALL, no-new-privileges
//   - zero custody mounts, zero signer socket, zero Docker socket, zero MCP token
//   - joined strictly to [basement-core, coolify-edge]
func RenderCookieScrubberSidecar(cfg CookieScrubberSidecarConfig) (map[string]any, error) {
	image := strings.TrimSpace(cfg.Image)
	if image == "" {
		image = CookieScrubberBaseImageRef + "@" + CookieScrubberBaseImageDigest
	}
	binaryPath := strings.TrimSpace(cfg.StagedBinaryPath)
	if binaryPath == "" {
		binaryPath = "./stackkit-server"
	}
	listen := strings.TrimSpace(cfg.ListenAddr)
	if listen == "" {
		listen = CookieScrubberDefaultListen
	}
	upstream := strings.TrimSpace(cfg.UpstreamURL)
	if upstream == "" {
		upstream = CookieScrubberDefaultUpstream
	}
	// Validate scrubber config fail-closed without importing server-side internal/auth packages.
	if err := validateCookieScrubberUpstreamURL(upstream); err != nil {
		return nil, err
	}

	tinyAuthNames := dedupeCookieScrubberNames(append([]string{"tinyauth", "tinyauth_session"}, cfg.TinyAuthCookieNames...))

	command := []string{
		"/usr/local/bin/stackkit-server",
		"--mode=cookie-scrubber",
		"--scrubber-listen=" + listen,
		"--scrubber-upstream=" + upstream,
		"--scrubber-tinyauth-cookies=" + strings.Join(tinyAuthNames, ","),
	}

	service := map[string]any{
		"image":        image,
		"restart":      "unless-stopped",
		"user":         "65534:65534",
		"read_only":    true,
		"cap_drop":     []string{"ALL"},
		"security_opt": []string{"no-new-privileges:true"},
		"mem_limit":    "64m",
		"command":      command,
		"volumes": []map[string]any{
			{
				"type":      "bind",
				"source":    binaryPath,
				"target":    "/usr/local/bin/stackkit-server",
				"read_only": true,
			},
		},
		"networks": []string{NetworkBasementCore, NetworkCoolifyEdge},
	}
	return service, nil
}

// RenderHardenedProxyOverlay generates the Option-A HTTP-only Coolify internal
// proxy overlay together with the zero-custody cookie-scrubber sidecar, and
// verifies the effective deployment (merged with BaseComposeYAML when supplied)
// against ValidateEffectiveProxyDeployment before returning.
func RenderHardenedProxyOverlay(cfg ProxyOverlayConfig) ([]byte, error) {
	posture, err := EvaluateTrustBoundary(cfg.TrustBoundary)
	if err != nil {
		return nil, err
	}
	proxyImage := strings.TrimSpace(cfg.ProxyImage)
	if proxyImage == "" {
		proxyImage = CoolifyProxyImageRef
	}

	proxyService := map[string]any{
		"image":        proxyImage,
		"restart":      "unless-stopped",
		"cap_drop":     []string{"ALL"},
		"security_opt": []string{"no-new-privileges:true"},
		"command": []string{
			"--providers.docker=true",
			"--providers.docker.exposedbydefault=false",
			"--entrypoints.http.address=:80",
			"--api.dashboard=false",
		},
		"networks": []string{NetworkCoolifyEdge},
	}

	upstreamURL := CookieScrubberDefaultUpstream
	if posture.Mode == TrustModeIsolatedExtensionVM {
		addrPort, parseErr := netip.ParseAddrPort(posture.PrivateListenerAddr)
		if parseErr != nil {
			return nil, parseErr
		}
		proxyService["ports"] = []string{fmt.Sprintf("%s:80", addrPort.String())}
		upstreamURL = fmt.Sprintf("http://%s", addrPort.String())
	}

	scrubberService, err := RenderCookieScrubberSidecar(CookieScrubberSidecarConfig{
		StagedBinaryPath:    cfg.StagedBinaryPath,
		ListenAddr:          cfg.ScrubberListen,
		UpstreamURL:         upstreamURL,
		TinyAuthCookieNames: cfg.TinyAuthCookieNames,
	})
	if err != nil {
		return nil, err
	}

	doc := map[string]any{
		"services": map[string]any{
			"coolify-proxy":           proxyService,
			CookieScrubberServiceName: scrubberService,
		},
		"networks": map[string]any{
			NetworkBasementCore: map[string]any{"external": true},
			NetworkCoolifyEdge:  map[string]any{"internal": posture.Mode == TrustModeSingleNodeTrustedOwner},
		},
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(cfg.BaseComposeYAML)) > 0 {
		if err := ValidateEffectiveProxyDeployment(cfg.BaseComposeYAML, raw); err != nil {
			return nil, fmt.Errorf("coolify effective proxy deployment failed validation: %w", err)
		}
	} else {
		if err := ValidateEffectiveProxyDeployment(raw); err != nil {
			return nil, fmt.Errorf("coolify proxy overlay failed self-validation: %w", err)
		}
	}
	return raw, nil
}

func validateCookieScrubberUpstreamURL(raw string) error {
	upstreamRaw := strings.TrimSpace(raw)
	if upstreamRaw == "" {
		return errors.New("cookiescrubber: upstream URL is required")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return errors.New("cookiescrubber: invalid upstream URL")
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" {
		return errors.New("cookiescrubber: upstream URL must use http or https with a host")
	}
	if upstream.User != nil || upstream.RawQuery != "" || upstream.ForceQuery || upstream.Fragment != "" {
		return errors.New("cookiescrubber: upstream URL must not carry credentials, query string, or fragment")
	}
	return nil
}

func dedupeCookieScrubberNames(inputs []string) []string {
	seen := make(map[string]struct{}, len(inputs))
	out := make([]string, 0, len(inputs))
	for _, entry := range inputs {
		for _, part := range strings.Split(entry, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}
