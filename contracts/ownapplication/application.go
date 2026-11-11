// Package ownapplication defines Techstack-owned desired state consumed by the
// node's StackKits renderer. It grants no host, daemon or provider authority.
package ownapplication

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"
	"regexp"

	"golang.org/x/net/publicsuffix"
	"strings"
)

const APIVersion = "techstack.own-application/v1"
const BundleVersion = "stackkit.own-application-bundle/v1"
const ModuleRef = "stackkits-own-application-runtime"

// Application is deliberately closed: users supply an image, never Compose,
// commands, capabilities, host mounts, devices or network configuration.
type Application struct {
	APIVersion  string            `json:"apiVersion"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	HTTPPort    int               `json:"httpPort"`
	Hostname    string            `json:"hostname"`
	SignIn      *bool             `json:"signIn,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	SecretRefs  map[string]string `json:"secretRefs,omitempty"`
	Volume      *Volume           `json:"volume,omitempty"`
	Resources   Resources         `json:"resources,omitempty"`
}
type Volume struct {
	MountPath string `json:"mountPath"`
}
type Resources struct {
	MemoryMiB int     `json:"memoryMiB,omitempty"`
	CPUs      float64 `json:"cpus,omitempty"`
}

// Installation is supplied by verified applied state, never by a live write's
// caller. Offline rendering accepts an explicit installation for inspection.
type Installation struct {
	Domain        string `json:"domain"`
	SiteRef       string `json:"siteRef"`
	NodeRef       string `json:"nodeRef"`
	CoreModuleRef string `json:"coreModuleRef"`
	TLSIssuerRef  string `json:"tlsIssuerRef"`
	TLSProfileRef string `json:"tlsProfileRef"`
	Exposure      string `json:"exposure"`
}

// Bundle adapts desired state into the common application delivery graph.
// ResolvedImage is registry-observed before approval; execution uses that digest.
type Bundle struct {
	APIVersion    string       `json:"apiVersion"`
	Application   Application  `json:"application"`
	Installation  Installation `json:"installation"`
	ResolvedImage string       `json:"resolvedImage,omitempty"`
	// ImageID is derived from the verified manifest/config by the node, never desired state.
	ImageID string `json:"imageID,omitempty"`
}

var label = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var variable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var repository = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]{1,5})?/[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var tag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var secretRef = regexp.MustCompile(`^secret://[a-zA-Z0-9][a-zA-Z0-9/_-]{0,199}$`)

func Decode(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return errors.New("own application document must be 1..65536 bytes")
	}
	if err := uniqueKeys(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("closed own application contract: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("own application document must contain one JSON value")
	}
	return nil
}
func Parse(raw []byte) (Application, error) {
	var a Application
	if err := Decode(raw, &a); err != nil {
		return a, err
	}
	return a, a.Validate()
}
func (a Application) Protected() bool     { return a.SignIn == nil || *a.SignIn }
func (a Application) WorkloadRef() string { return "own-" + a.Name }
func (a Application) Limits() Resources {
	r := a.Resources
	if r.MemoryMiB == 0 {
		r.MemoryMiB = 512
	}
	if r.CPUs == 0 {
		r.CPUs = 1
	}
	return r
}
func ValidName(name string) bool { return len(name) >= 1 && len(name) <= 40 && label.MatchString(name) }

// ValidateRegistry is the single image-host admission rule. It performs no DNS
// lookup; transport additionally rejects non-public resolved addresses.
func ValidateRegistry(registry string) error {
	host, port, explicit := strings.Cut(registry, ":")
	if explicit && port != "443" {
		return errors.New("own image registry permits only HTTPS port 443")
	}
	if len(host) > 253 || !strings.Contains(host, ".") {
		return errors.New("own image registry requires a public DNS name over HTTPS")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return errors.New("IP-literal image registries are forbidden")
	}
	for _, part := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(part) {
			return errors.New("image registry must use canonical public DNS labels")
		}
	}
	suffix, _ := publicsuffix.PublicSuffix(host)
	_, icann := publicsuffix.PublicSuffix(host[strings.LastIndex(host, ".")+1:])
	if !icann || suffix == host {
		return errors.New("private, reserved and unknown registry DNS suffixes are forbidden")
	}
	// Special-use names nested beneath an otherwise public suffix are private too.
	for _, suffix := range []string{"localhost", "local", "localdomain", "lan", "home", "internal", "test", "invalid", "example", "onion", "alt", "arpa", "home.arpa"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return errors.New("private registry DNS suffix is forbidden")
		}
	}
	return nil
}

func SplitImage(image string) (string, string, error) {
	registry, _, _ := strings.Cut(image, "/")
	if err := ValidateRegistry(registry); err != nil {
		return "", "", err
	}
	if image != strings.TrimSpace(image) || len(image) > 512 {
		return "", "", errors.New("image reference is not canonical")
	}
	if repo, d, ok := strings.Cut(image, "@"); ok {
		if repository.MatchString(repo) && digest.MatchString(d) {
			return repo, d, nil
		}
	} else if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") && i > 0 {
		if repository.MatchString(image[:i]) && tag.MatchString(image[i+1:]) {
			return image, "", nil
		}
	}
	return "", "", errors.New("image must be registry/repository:tag or registry/repository@sha256:<64 lowercase hex>; schemes, implicit tags and builds are forbidden")
}
func (a Application) Validate() error {
	if a.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %s", APIVersion)
	}
	if !ValidName(a.Name) || strings.HasPrefix(a.Name, "kombify") || strings.HasPrefix(a.Name, "stackkit") {
		return errors.New("name must be 1..40 lowercase DNS characters and must not start with kombify or stackkit")
	}
	if _, _, err := SplitImage(a.Image); err != nil {
		return err
	}
	if a.HTTPPort < 1 || a.HTTPPort > 65535 {
		return errors.New("httpPort must be 1..65535")
	}
	host := strings.Split(a.Hostname, ".")
	if len(host) < 2 || len(a.Hostname) > 253 || host[0] != a.WorkloadRef() {
		return errors.New("hostname must be own-<name>.<installation-domain>")
	}
	for _, part := range host {
		if len(part) > 63 || !dnsLabel.MatchString(part) {
			return errors.New("hostname must use lowercase DNS labels")
		}
	}
	if len(a.Environment) > 64 || len(a.SecretRefs) > 64 {
		return errors.New("at most 64 environment variables and 64 secret references are allowed")
	}
	for key, value := range a.Environment {
		if !variable.MatchString(key) || len(key) > 128 || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("environment names or values are invalid or unbounded")
		}
		if _, exists := a.SecretRefs[key]; exists {
			return fmt.Errorf("environment %s also names a secret", key)
		}
	}
	for key, ref := range a.SecretRefs {
		if !variable.MatchString(key) || len(key) > 128 || !secretRef.MatchString(ref) || ref != "secret://"+a.WorkloadRef()+"/"+key {
			return errors.New("secrets must map each variable to secret://own-<name>/<same-variable>; foreign custody is forbidden")
		}
	}
	if a.Volume != nil {
		p := a.Volume.MountPath
		if len(p) > 200 || !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.ContainsAny(p, ":\\$\x00\r\n") {
			return errors.New("volume mountPath must be a clean absolute container path")
		}
		for _, forbidden := range []string{"/", "/var", "/run", "/var/run", "/proc", "/sys", "/dev", "/etc"} {
			if p == forbidden || (forbidden != "/" && forbidden != "/var" && strings.HasPrefix(p, forbidden+"/")) {
				return errors.New("volume mountPath must not cover a system path or Docker socket")
			}
		}
	}
	r := a.Limits()
	if r.MemoryMiB < 64 || r.MemoryMiB > 8192 || r.CPUs < 0.1 || r.CPUs > 4 {
		return errors.New("resources must be 64..8192 MiB and 0.1..4 CPUs; defaults are 512 MiB and 1 CPU")
	}
	return nil
}
func (b Bundle) Validate() error {
	if b.ImageID != "" && (!digest.MatchString(b.ImageID) || b.ResolvedImage == "") {
		return errors.New("invalid node-derived image identity")
	}
	if b.APIVersion != BundleVersion {
		return errors.New("own application bundle version is invalid")
	}
	if err := b.Application.Validate(); err != nil {
		return err
	}
	i := b.Installation
	if b.Application.Hostname != b.Application.WorkloadRef()+"."+i.Domain || !label.MatchString(i.SiteRef) || !label.MatchString(i.NodeRef) {
		return errors.New("own application must bind the installation domain and exact local node")
	}
	if i.CoreModuleRef != "stackkits-basement-core-runtime" && i.CoreModuleRef != "stackkits-basement-core-lite-runtime" {
		return errors.New("own applications require an installed Direct Compose Home core")
	}
	if i.TLSIssuerRef == "" || i.TLSProfileRef == "" || (i.Exposure != "local-only" && i.Exposure != "remote-private" && i.Exposure != "public") {
		return errors.New("own application requires existing routing, TLS and exposure authority")
	}
	if b.ResolvedImage != "" {
		repo, d, err := SplitImage(b.ResolvedImage)
		if err != nil || d == "" {
			return errors.New("resolved image must be pinned by digest")
		}
		want, _, _ := SplitImage(b.Application.Image)
		if colon := strings.LastIndex(want, ":"); colon > strings.LastIndex(want, "/") {
			want = want[:colon]
		}
		_, desiredDigest, _ := SplitImage(b.Application.Image)
		if desiredDigest != "" && desiredDigest != d {
			return errors.New("resolved image differs from desired digest")
		}
		if repo != want {
			return errors.New("resolved image repository differs from desired reference")
		}
	}
	return nil
}

func uniqueKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if seen[key] {
				return errors.New("own application JSON repeats an object key")
			}
			seen[key] = true
			if err = uniqueKeys(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err = uniqueKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON container")
	}
	_, err = decoder.Token()
	return err
}

// AdmitImageConfiguration closes authority inherited from an image rather
// than the desired document. In particular Dockerfile routing labels must not
// create a second ingress path outside the reviewed router and Access Gate.
func (a Application) AdmitImageConfiguration(raw []byte) error {
	if len(raw) == 0 || len(raw) > 256<<10 {
		return errors.New("image configuration is unavailable or exceeds its bound")
	}
	var config struct {
		Labels  map[string]string          `json:"Labels"`
		Volumes map[string]json.RawMessage `json:"Volumes"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("image configuration is malformed")
	}
	for label := range config.Labels {
		if strings.HasPrefix(strings.ToLower(label), "traefik.") {
			return errors.New("image inherits routing labels; use an image without traefik.* labels")
		}
	}
	for mount := range config.Volumes {
		if a.Volume == nil || mount != a.Volume.MountPath {
			return errors.New("image declares an unmanaged volume; it must match the one admitted named volume")
		}
	}
	return nil
}
