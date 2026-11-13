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
	"sort"

	"golang.org/x/net/publicsuffix"
	"strings"
)

const APIVersion = "techstack.own-application/v1"
const APIVersionV2 = "techstack.own-application/v2"
const APIVersionV3 = "techstack.own-application/v3"

// APIVersionV4 is one container StackKits rebuilt from an observed container
// it did not install ("Stack it"). It is the v2 single-container form plus the
// adoption source and the host ports the original published.
const APIVersionV4 = "techstack.own-application/v4"
const BundleVersion = "stackkit.own-application-bundle/v1"
const ModuleRef = "stackkits-own-application-runtime"

// Application is deliberately closed: users supply an image, never Compose,
// commands, capabilities, host mounts, devices or network configuration.
//
// A techstack.own-application/v3 document declares a Compose project instead of
// a single container: it lists its services and carries no image or httpPort of
// its own.
type Application struct {
	OutboundInternet *bool             `json:"outboundInternet,omitempty"`
	APIVersion       string            `json:"apiVersion"`
	Name             string            `json:"name"`
	Image            string            `json:"image,omitempty"`
	HTTPPort         int               `json:"httpPort,omitempty"`
	Hostname         string            `json:"hostname"`
	SignIn           *bool             `json:"signIn,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	SecretRefs       map[string]string `json:"secretRefs,omitempty"`
	Volume           *Volume           `json:"volume,omitempty"`
	Resources        Resources         `json:"resources,omitempty"`
	// Services declares the containers of a techstack.own-application/v3
	// project. The one-container forms keep their image and httpPort here.
	Services []Service `json:"services,omitempty"`
	// AdoptedFrom and PublishedPorts belong to techstack.own-application/v4
	// only: the observed source of an adopted container and the host ports it
	// published, kept so LAN host:port clients keep working.
	AdoptedFrom    *AdoptedFrom    `json:"adoptedFrom,omitempty"`
	PublishedPorts []PublishedPort `json:"publishedPorts,omitempty"`
}

// AdoptedFrom records the observed container an adopted application was
// rebuilt from. It is evidence of origin, never runtime authority.
type AdoptedFrom struct {
	ContainerID string `json:"containerId"`
	// ImageDigest is the registry manifest digest the original ran.
	ImageDigest string `json:"imageDigest"`
	// Volumes names the data source copied into the named volume:
	// volume:<name> or bind:<absolute host path>. At most one.
	Volumes []string `json:"volumes,omitempty"`
	// OriginalConfigRef names the configuration the original came from, for
	// example compose:<project>/<service> or docker:<container name>.
	OriginalConfigRef string `json:"originalConfigRef"`
}

// PublishedPort is one host port the adopted container keeps publishing.
type PublishedPort struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

// Service is one container of an own Compose project. Exactly one service
// declares httpPort and is routed on the project hostname; every other service
// stays on the project's private network.
type Service struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	HTTPPort    int               `json:"httpPort,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	SecretRefs  map[string]string `json:"secretRefs,omitempty"`
	Volume      *Volume           `json:"volume,omitempty"`
	Resources   Resources         `json:"resources,omitempty"`
	DependsOn   []string          `json:"dependsOn,omitempty"`
}

// UnmarshalJSON keeps the v1 wire shape closed even when a caller decodes a
// bundle directly rather than calling Parse. An explicit null is not a bool.
func (a *Application) UnmarshalJSON(raw []byte) error {
	type wire Application
	var value wire
	if err := Decode(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if flag, present := fields["outboundInternet"]; present {
		if (value.APIVersion != APIVersionV2 && value.APIVersion != APIVersionV4) || bytes.Equal(bytes.TrimSpace(flag), []byte("null")) {
			return errors.New("outboundInternet is a boolean field of techstack.own-application/v2 and v4 only")
		}
	}
	for _, field := range []string{"adoptedFrom", "publishedPorts"} {
		if raw, present := fields[field]; present {
			if value.APIVersion != APIVersionV4 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("%s is a field of techstack.own-application/v4 only", field)
			}
		}
	}
	*a = Application(value)
	return nil
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
	// ResolvedImages and ImageIDs carry the image custody of every service of a
	// techstack.own-application/v3 project, keyed by service name. A project
	// never carries the single-container custody above.
	ResolvedImages map[string]string `json:"resolvedImages,omitempty"`
	ImageIDs       map[string]string `json:"imageIDs,omitempty"`
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
func (a Application) OutboundEnabled() bool {
	return (a.APIVersion == APIVersionV2 || a.APIVersion == APIVersionV4) && a.OutboundInternet != nil && *a.OutboundInternet
}

// Firewalled reports whether the application runs on its managed host
// firewall bridge instead of an internal-only network: it has outbound
// Internet, published host ports, or both. Docker publishes no port of a
// container attached only to an internal network, so published ports without
// outbound use the same bridge with an ingress-only ruleset.
func (a Application) Firewalled() bool {
	return a.OutboundEnabled() || len(a.PublishedPorts) != 0
}

// IsAdopted reports whether StackKits rebuilt the application from an
// observed container it did not install.
func (a Application) IsAdopted() bool     { return a.APIVersion == APIVersionV4 }
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

// ValidServiceName reports whether name is a Compose service identity this
// contract admits: a lowercase DNS label of at most 40 characters.
func ValidServiceName(name string) bool {
	return len(name) >= 1 && len(name) <= 40 && label.MatchString(name)
}

// IsProject reports whether the application declares a Compose project rather
// than a single container.
func (a Application) IsProject() bool { return a.APIVersion == APIVersionV3 }

// Service returns the declared project service with the given name.
func (a Application) Service(name string) (Service, bool) {
	for _, s := range a.Services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}

// RoutedService returns the single project service that serves the routed
// host. A project always declares exactly one.
func (a Application) RoutedService() (Service, bool) {
	for _, s := range a.Services {
		if s.HTTPPort != 0 {
			return s, true
		}
	}
	return Service{}, false
}

// HasVolume reports whether any declared container keeps a named volume.
func (a Application) HasVolume() bool {
	if a.Volume != nil {
		return true
	}
	for _, s := range a.Services {
		if s.Volume != nil {
			return true
		}
	}
	return false
}

// SecretSlotKey is the flat input slot of one project secret. It keeps the
// private secrets file a flat object while the custody reference stays
// namespaced by the service that holds the variable.
func SecretSlotKey(service, key string) string { return service + "." + key }

// SecretSlots maps every declared secret to the flat input slot that carries
// its private material and to the custody reference that holds it.
func (a Application) SecretSlots() map[string]string {
	slots := map[string]string{}
	if a.IsProject() {
		for _, s := range a.Services {
			for key, ref := range s.SecretRefs {
				slots[SecretSlotKey(s.Name, key)] = ref
			}
		}
		return slots
	}
	for key, ref := range a.SecretRefs {
		slots[key] = ref
	}
	return slots
}

// SecretRefList returns every custody reference of the application in a
// stable order.
func (a Application) SecretRefList() []string {
	slots := a.SecretSlots()
	keys := make([]string, 0, len(slots))
	for slot := range slots {
		keys = append(keys, slot)
	}
	sort.Strings(keys)
	refs := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, slot := range keys {
		ref := slots[slot]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// ExpectedSecretRef returns the custody reference a declared secret slot must
// carry. It returns the empty string for a slot the application does not
// declare, so a caller can refuse foreign custody.
func (a Application) ExpectedSecretRef(slot string) string {
	ref, ok := a.SecretSlots()[slot]
	if !ok {
		return ""
	}
	if a.IsProject() {
		service, key, found := strings.Cut(slot, ".")
		if !found || ref != "secret://"+a.WorkloadRef()+"/"+service+"/"+key {
			return ""
		}
		return ref
	}
	if ref != "secret://"+a.WorkloadRef()+"/"+slot {
		return ""
	}
	return ref
}

// ServiceLimits returns the admitted limits of one project service.
func (s Service) Limits() Resources {
	r := s.Resources
	if r.MemoryMiB == 0 {
		r.MemoryMiB = 512
	}
	if r.CPUs == 0 {
		r.CPUs = 1
	}
	return r
}

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
	if a.APIVersion != APIVersion && a.APIVersion != APIVersionV2 && a.APIVersion != APIVersionV3 && a.APIVersion != APIVersionV4 {
		return fmt.Errorf("unsupported own application apiVersion: %s", a.APIVersion)
	}
	if a.APIVersion == APIVersion && a.OutboundInternet != nil {
		return errors.New("outboundInternet requires techstack.own-application/v2; v1 always has no outbound Internet")
	}
	if err := a.validateAdoption(); err != nil {
		return err
	}
	if !ValidName(a.Name) || strings.HasPrefix(a.Name, "kombify") || strings.HasPrefix(a.Name, "stackkit") {
		return errors.New("name must be 1..40 lowercase DNS characters and must not start with kombify or stackkit")
	}
	if a.APIVersion == APIVersionV3 {
		if err := a.validateProject(); err != nil {
			return err
		}
	} else {
		if len(a.Services) != 0 {
			return errors.New("services belong to techstack.own-application/v3 only")
		}
		if _, _, err := SplitImage(a.Image); err != nil {
			return err
		}
		if a.HTTPPort < 1 || a.HTTPPort > 65535 {
			return errors.New("httpPort must be 1..65535")
		}
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
	if a.APIVersion == APIVersionV3 {
		return nil
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
		if err := validateVolumePath(a.Volume.MountPath); err != nil {
			return err
		}
	}
	r := a.Limits()
	if r.MemoryMiB < 64 || r.MemoryMiB > 8192 || r.CPUs < 0.1 || r.CPUs > 4 {
		return errors.New("resources must be 64..8192 MiB and 0.1..4 CPUs; defaults are 512 MiB and 1 CPU")
	}
	return nil
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var adoptedVolume = regexp.MustCompile(`^(?:volume:[A-Za-z0-9][A-Za-z0-9_.-]{0,254}|bind:/[^\x00\r\n:]{0,511})$`)
var configRef = regexp.MustCompile(`^(?:compose|docker):[A-Za-z0-9][A-Za-z0-9_./-]{0,255}$`)

// AdoptedDataRoots are the host directories an adopted bind-mounted data
// source may live below. System paths, the Docker socket and / never are.
var AdoptedDataRoots = []string{"/srv/", "/opt/", "/data/", "/home/", "/mnt/", "/media/"}

// AdoptableBindSource reports whether a host path is a clean path strictly
// below one of the admitted data roots.
func AdoptableBindSource(source string) bool {
	if path.Clean(source) != source {
		return false
	}
	for _, root := range AdoptedDataRoots {
		if strings.HasPrefix(source, root) && len(source) > len(root) {
			return true
		}
	}
	return false
}

// validateAdoption admits the v4 adoption source and the host ports the
// adopted container keeps. Every other version carries neither.
func (a Application) validateAdoption() error {
	if a.APIVersion != APIVersionV4 {
		if a.AdoptedFrom != nil || len(a.PublishedPorts) != 0 {
			return errors.New("adoptedFrom and publishedPorts belong to techstack.own-application/v4 only")
		}
		return nil
	}
	from := a.AdoptedFrom
	if from == nil {
		return errors.New("techstack.own-application/v4 requires adoptedFrom")
	}
	if !containerID.MatchString(from.ContainerID) || !digest.MatchString(from.ImageDigest) || !configRef.MatchString(from.OriginalConfigRef) {
		return errors.New("adoptedFrom requires the 64-hex container ID, the sha256 image digest and a compose: or docker: configuration reference")
	}
	if len(from.Volumes) > 1 {
		return errors.New("an adopted container brings at most one data source")
	}
	for _, source := range from.Volumes {
		if !adoptedVolume.MatchString(source) || (strings.HasPrefix(source, "bind:") && !AdoptableBindSource(source[5:])) {
			return errors.New("adoptedFrom volumes name volume:<name> or a clean bind:<path> below " + strings.Join(AdoptedDataRoots, ", "))
		}
	}
	if len(a.PublishedPorts) > 16 {
		return errors.New("an adopted container keeps at most 16 published ports")
	}
	for i, p := range a.PublishedPorts {
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			return errors.New("published port protocol must be tcp or udp")
		}
		if p.HostPort < 1 || p.HostPort > 65535 || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return errors.New("published ports must be 1..65535")
		}
		if p.Protocol == "tcp" && (p.HostPort == 80 || p.HostPort == 443) {
			return errors.New("host ports 80 and 443 belong to the installation router")
		}
		if p.HostIP != "" {
			ip, err := netip.ParseAddr(p.HostIP)
			if err != nil || !ip.Is4() || ip.String() != p.HostIP {
				return errors.New("published port hostIP must be an IPv4 address or empty for every address")
			}
		}
		// Two bindings collide on the same port and protocol when either binds
		// every address or both bind the same one.
		for _, other := range a.PublishedPorts[:i] {
			if other.HostPort == p.HostPort && other.Protocol == p.Protocol && (other.HostIP == "" || p.HostIP == "" || other.HostIP == "0.0.0.0" || p.HostIP == "0.0.0.0" || other.HostIP == p.HostIP) {
				return errors.New("published host ports must be unique")
			}
		}
	}
	return nil
}

// validateProject admits a Compose project: a closed set of containers that
// share the project's private network, exactly one of which serves the routed
// host. The same refusals as for one container apply to every service.
func (a Application) validateProject() error {
	if a.Image != "" || a.HTTPPort != 0 {
		return errors.New("techstack.own-application/v3 declares its containers in services; image and httpPort belong to v1 and v2")
	}
	if len(a.Environment) != 0 || len(a.SecretRefs) != 0 || a.Volume != nil {
		return errors.New("techstack.own-application/v3 declares environment, secrets and volumes per service")
	}
	if a.Resources != (Resources{}) {
		return errors.New("techstack.own-application/v3 declares resources per service")
	}
	if len(a.Services) == 0 || len(a.Services) > 8 {
		return errors.New("an own application project declares 1..8 services")
	}
	names := map[string]bool{}
	routed := 0
	for _, s := range a.Services {
		if !ValidServiceName(s.Name) {
			return errors.New("service name must be 1..40 lowercase DNS characters")
		}
		if names[s.Name] {
			return fmt.Errorf("service %s is declared twice", s.Name)
		}
		names[s.Name] = true
		if _, _, err := SplitImage(s.Image); err != nil {
			return err
		}
		if s.HTTPPort < 0 || s.HTTPPort > 65535 {
			return errors.New("httpPort must be 1..65535 when declared")
		}
		if s.HTTPPort != 0 {
			routed++
		}
		if len(s.Environment) > 64 || len(s.SecretRefs) > 64 {
			return fmt.Errorf("service %s declares at most 64 environment variables and 64 secret references", s.Name)
		}
		for key, value := range s.Environment {
			if !variable.MatchString(key) || len(key) > 128 || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
				return fmt.Errorf("service %s has an invalid or unbounded environment entry", s.Name)
			}
			if _, exists := s.SecretRefs[key]; exists {
				return fmt.Errorf("service %s names %s as both an environment variable and a secret", s.Name, key)
			}
		}
		for key, ref := range s.SecretRefs {
			if !variable.MatchString(key) || len(key) > 128 || !secretRef.MatchString(ref) || ref != "secret://"+a.WorkloadRef()+"/"+s.Name+"/"+key {
				return fmt.Errorf("service %s must map each secret to secret://own-<name>/<service>/<same-variable>; foreign custody is forbidden", s.Name)
			}
		}
		if s.Volume != nil {
			if err := validateVolumePath(s.Volume.MountPath); err != nil {
				return fmt.Errorf("service %s: %w", s.Name, err)
			}
		}
		r := s.Limits()
		if r.MemoryMiB < 64 || r.MemoryMiB > 8192 || r.CPUs < 0.1 || r.CPUs > 4 {
			return fmt.Errorf("service %s resources must be 64..8192 MiB and 0.1..4 CPUs; defaults are 512 MiB and 1 CPU", s.Name)
		}
	}
	if routed != 1 {
		return errors.New("exactly one service must declare httpPort; it is the routed service of the project")
	}
	for _, s := range a.Services {
		for _, dependency := range s.DependsOn {
			if dependency == s.Name {
				return fmt.Errorf("service %s depends on itself", s.Name)
			}
			if !names[dependency] {
				return fmt.Errorf("service %s depends on unknown service %s", s.Name, dependency)
			}
		}
	}
	return validateProjectOrder(a.Services)
}

// validateVolumePath admits the container mount path of a named volume. It
// refuses host paths, system paths and the Docker socket.
func validateVolumePath(p string) error {
	if len(p) > 200 || !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.ContainsAny(p, ":\\$\x00\r\n") {
		return errors.New("volume mountPath must be a clean absolute container path")
	}
	for _, forbidden := range []string{"/", "/var", "/run", "/var/run", "/proc", "/sys", "/dev", "/etc"} {
		if p == forbidden || (forbidden != "/" && forbidden != "/var" && strings.HasPrefix(p, forbidden+"/")) {
			return errors.New("volume mountPath must not cover a system path or Docker socket")
		}
	}
	return nil
}

// validateProjectOrder refuses a dependency cycle before Compose sees it.
func validateProjectOrder(services []Service) error {
	const (
		visiting = 1
		visited  = 2
	)
	state := map[string]int{}
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("service %s takes part in a dependency cycle", name)
		case visited:
			return nil
		}
		state[name] = visiting
		for _, s := range services {
			if s.Name != name {
				continue
			}
			for _, dependency := range s.DependsOn {
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		state[name] = visited
		return nil
	}
	for _, s := range services {
		if err := visit(s.Name); err != nil {
			return err
		}
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
	if b.Application.IsProject() {
		if b.ResolvedImage != "" || b.ImageID != "" {
			return errors.New("a project carries the image custody of every service, not of a single container")
		}
		return b.validateProjectImages()
	}
	if b.ResolvedImage != "" {
		if err := validateResolvedImage(b.Application.Image, b.ResolvedImage); err != nil {
			return err
		}
	}
	return nil
}

// validateProjectImages admits the node-observed image custody of every
// service of a project. An unresolved project is still a valid desired state.
func (b Bundle) validateProjectImages() error {
	if len(b.ResolvedImages) == 0 && len(b.ImageIDs) == 0 {
		return nil
	}
	if len(b.ResolvedImages) != len(b.Application.Services) || len(b.ImageIDs) != len(b.Application.Services) {
		return errors.New("a resolved project carries the image custody of every service")
	}
	for _, s := range b.Application.Services {
		resolved, ok := b.ResolvedImages[s.Name]
		if !ok {
			return fmt.Errorf("service %s has no resolved image", s.Name)
		}
		if err := validateResolvedImage(s.Image, resolved); err != nil {
			return fmt.Errorf("service %s: %w", s.Name, err)
		}
		id, ok := b.ImageIDs[s.Name]
		if !ok || !digest.MatchString(id) {
			return fmt.Errorf("service %s has an invalid node-derived image identity", s.Name)
		}
	}
	return nil
}

// ImagesResolved reports whether every declared container of the bundle
// carries its node-observed image custody.
func (b Bundle) ImagesResolved() bool {
	if b.Application.IsProject() {
		if len(b.ResolvedImages) != len(b.Application.Services) || len(b.ImageIDs) != len(b.Application.Services) {
			return false
		}
		for _, s := range b.Application.Services {
			if b.ResolvedImages[s.Name] == "" || b.ImageIDs[s.Name] == "" {
				return false
			}
		}
		return true
	}
	return b.ResolvedImage != ""
}

// ProjectRelease is the release identity of a project: the resolved image of
// every service in declared order. It names the whole project, not one
// container.
func (b Bundle) ProjectRelease() string {
	parts := make([]string, 0, len(b.Application.Services))
	for _, s := range b.Application.Services {
		image := s.Image
		if pinned := b.ResolvedImages[s.Name]; pinned != "" {
			image = pinned
		}
		parts = append(parts, s.Name+"="+image)
	}
	return strings.Join(parts, ",")
}

// validateResolvedImage admits a node-observed image against the desired
// reference: the resolved image must be pinned by digest and must name the
// same repository as the desired image.
func validateResolvedImage(desired, resolved string) error {
	repo, d, err := SplitImage(resolved)
	if err != nil || d == "" {
		return errors.New("resolved image must be pinned by digest")
	}
	want, _, _ := SplitImage(desired)
	if colon := strings.LastIndex(want, ":"); colon > strings.LastIndex(want, "/") {
		want = want[:colon]
	}
	_, desiredDigest, _ := SplitImage(desired)
	if desiredDigest != "" && desiredDigest != d {
		return errors.New("resolved image differs from desired digest")
	}
	if repo != want {
		return errors.New("resolved image repository differs from desired reference")
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
	mountPath := ""
	if a.Volume != nil {
		mountPath = a.Volume.MountPath
	}
	return admitImageConfiguration(raw, mountPath)
}

// AdmitImageConfiguration closes the authority inherited from the image of
// one project service rather than from the user contract.
func (s Service) AdmitImageConfiguration(raw []byte) error {
	mountPath := ""
	if s.Volume != nil {
		mountPath = s.Volume.MountPath
	}
	return admitImageConfiguration(raw, mountPath)
}

// admitImageConfiguration refuses an image that declares a volume the
// contract did not admit, and an image that carries routing labels.
func admitImageConfiguration(raw []byte, mountPath string) error {
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
		if mountPath == "" || mount != mountPath {
			return errors.New("image declares an unmanaged volume; it must match the one admitted named volume")
		}
	}
	return nil
}
