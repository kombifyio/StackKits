package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
)

const (
	socketProxyModuleID       = "socket-proxy"
	socketProxyUnitID         = "compose"
	socketProxyRendererRef    = "stackkit"
	socketProxyTemplateRef    = "builtin://foundation/socket-proxy/compose.yaml"
	socketProxyVersion        = "1.0.0"
	socketProxyOutputRef      = "foundation/socket-proxy/compose.yaml"
	socketProxyProviderRef    = "stackkits-basement-compose"
	socketProxyDaemonRef      = "docker-default"
	socketProxyNetworkRef     = "docker-api-readonly"
	socketProxyInterfaceRef   = "docker-api-readonly"
	socketProxyEndpointRef    = "docker-api"
	socketProxyPolicyProfile  = "docker-readonly-baseline"
	socketProxyApprovalPolicy = "docker-provider-backing"
	socketProxyApprovalID     = "approve-socket-proxy-backing"
	socketProxyApprovalProof  = "socket-proxy-provider-backing-governance"
	socketProxyImageRef       = "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
	socketProxyImageDigest    = "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
	socketProxyImage          = socketProxyImageRef + "@" + socketProxyImageDigest
	socketProxySocketToken    = "@@DAEMON_SOCKET_PATH@@"
	socketProxyNetworkToken   = "@@NETWORK_INSTANCE_REF@@"
)

const socketProxyComposeTemplate = `services:
  socket-proxy:
    image: "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2@sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
    restart: unless-stopped
    environment:
      ALLOW_PAUSE: "0"
      ALLOW_RESTARTS: "0"
      ALLOW_START: "0"
      ALLOW_STOP: "0"
      ALLOW_UNPAUSE: "0"
      AUTH: "0"
      BUILD: "0"
      COMMIT: "0"
      CONFIGS: "0"
      CONTAINERS: "1"
      DISABLE_IPV6: "1"
      DISTRIBUTION: "0"
      EVENTS: "1"
      EXEC: "0"
      GRPC: "0"
      IMAGES: "0"
      INFO: "0"
      NETWORKS: "1"
      NODES: "0"
      PING: "1"
      PLUGINS: "0"
      POST: "0"
      SECRETS: "0"
      SERVICES: "0"
      SESSION: "0"
      SWARM: "0"
      SYSTEM: "0"
      TASKS: "0"
      VERSION: "1"
      VOLUMES: "0"
    expose:
      - "2375"
    volumes:
      - type: bind
        source: "@@DAEMON_SOCKET_PATH@@"
        target: /var/run/docker.sock
        read_only: true
    networks:
      socket-proxy:
        aliases:
          - socket-proxy
    read_only: true
    tmpfs:
      - /run
      - /tmp
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test:
        - CMD-SHELL
        - wget -q --spider http://127.0.0.1:2375/version || exit 1
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 5s

networks:
  socket-proxy:
    name: "@@NETWORK_INSTANCE_REF@@"
    internal: true
`

type socketProxyProvidedInterfaceContract struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
	Endpoint struct {
		Ref        string `json:"ref"`
		Visibility string `json:"visibility"`
		Transport  string `json:"transport"`
		NetworkRef string `json:"networkRef"`
		Address    string `json:"address"`
		Port       int    `json:"port"`
	} `json:"endpoint"`
	Scopes        []string `json:"scopes"`
	CoLocation    string   `json:"coLocation"`
	DaemonRef     string   `json:"daemonRef"`
	PolicyProfile string   `json:"policyProfile"`
}

type socketProxyRequiredInterfaceContract struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
	Endpoint struct {
		Visibility string `json:"visibility"`
		Transport  string `json:"transport"`
		PathSource string `json:"pathSource"`
	} `json:"endpoint"`
	Scopes        []string `json:"scopes"`
	CoLocation    string   `json:"coLocation"`
	DaemonRef     string   `json:"daemonRef"`
	PolicyProfile string   `json:"policyProfile"`
}

// SocketProxyRendererContract returns the exact built-in product renderer
// identity. The hash covers the complete immutable Compose policy, including
// the pinned image, deny-list, mount target, security posture, and the two
// authority-owned instance placeholders.
func SocketProxyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(socketProxyComposeTemplate))
	return RendererContract{
		Kind: "compose", RendererRef: socketProxyRendererRef, TemplateRef: socketProxyTemplateRef,
		Version: socketProxyVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type socketProxyComposeRenderer struct {
	template []byte
	contract RendererContract
}

func newSocketProxyComposeRenderer() socketProxyComposeRenderer {
	return socketProxyComposeRenderer{
		template: []byte(socketProxyComposeTemplate),
		contract: SocketProxyRendererContract(),
	}
}

func (r socketProxyComposeRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	socketPath, networkInstanceRef, err := validateSocketProxyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if socketProxyTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(socketProxySocketToken)) != 1 || bytes.Count(r.template, []byte(socketProxyNetworkToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.socket-proxy.template", "embedded Compose policy does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(socketProxySocketToken), []byte(socketPath), 1)
	output = bytes.Replace(output, []byte(socketProxyNetworkToken), []byte(networkInstanceRef), 1)
	if bytes.Contains(output, []byte("@@")) {
		return nil, fail(ErrOutputChanged, "renderer.socket-proxy.template", "unresolved template authority token")
	}
	return []UnitOutput{{Ref: socketProxyOutputRef, Bytes: output}}, nil
}

func socketProxyTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

//nolint:gocyclo // Exact socket, interface, network, approval, and image authority is intentionally audited in one linear boundary.
func validateSocketProxyUnit(unit RenderUnit, contract RendererContract) (string, string, error) {
	unitPath := "resolvedPlan.modules." + socketProxyModuleID + ".renderUnits." + socketProxyUnitID
	if unit.ModuleID() != socketProxyModuleID || unit.ID() != socketProxyUnitID {
		return "", "", fail(ErrInvalidPlan, unitPath, "renderer accepts only %s/%s", socketProxyModuleID, socketProxyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return "", "", fail(ErrOutputChanged, unitPath, "render-unit implementation identity differs from the registered socket-proxy contract")
	}
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "stackkit" {
		return "", "", fail(ErrInvalidPlan, unitPath+".runtime", "socket proxy requires exact container/stackkit runtime ownership")
	}
	engine, present := unit.RuntimeEngine()
	if !present || engine != "docker" {
		return "", "", fail(ErrInvalidPlan, unitPath+".runtime.engine", "socket proxy requires the Docker engine")
	}
	imageRef, refPresent := unit.ContainerImageRef()
	imageDigest, digestPresent := unit.ContainerImageDigest()
	if !refPresent || !digestPresent || imageRef != socketProxyImageRef || imageDigest != socketProxyImageDigest {
		return "", "", fail(ErrOutputChanged, unitPath+".runtime.image", "socket proxy image must match exact ref and OCI digest %s", socketProxyImage)
	}
	if unit.InstanceScope() != "node-local" {
		return "", "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID()+".scope", "socket proxy must be node-local")
	}
	siteRef, sitePresent := unit.SiteRef()
	nodeRef, nodePresent := unit.NodeRef()
	daemonRef, daemonPresent := unit.DaemonRef()
	daemonInstanceRef, daemonInstancePresent := unit.DaemonInstanceRef()
	daemonEngine, daemonEnginePresent := unit.DaemonEngine()
	socketPath, socketPresent := unit.DaemonSocketPath()
	if !sitePresent || !nodePresent || !daemonPresent || !daemonInstancePresent || !daemonEnginePresent || !socketPresent {
		return "", "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID(), "exact site/node/daemon/socket binding is required")
	}
	if daemonRef != socketProxyDaemonRef || daemonEngine != "docker" || validateDockerSocketPath(socketPath) != nil {
		return "", "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID(), "daemon binding must be exact docker-default Docker with a canonical Unix socket")
	}
	wantInstanceID := fmt.Sprintf("%s-node-%s-daemon-%s", socketProxyUnitID, nodeRef, daemonInstanceRef)
	if unit.InstanceID() != wantInstanceID {
		return "", "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID()+".id", "must be exact daemon-bound instance %q", wantInstanceID)
	}
	if !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !stringListContains(unit.LogicalNodeRefs(), nodeRef) {
		return "", "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID(), "instance must remain inside the governed logical placement")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONArray(unit.ServiceEndpointsJSON()) {
		return "", "", fail(ErrInvalidPlan, unitPath+".inputs", "socket-proxy v1.0.0 has no free inputs, secrets, or routable service endpoints")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
		DaemonRef   string `json:"daemonRef"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-daemon" || placement.DaemonRef != socketProxyDaemonRef {
		return "", "", fail(ErrInvalidPlan, unitPath+".placement", "socket proxy requires exact node-local/one-per-daemon docker-default placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != socketProxyOutputRef {
		return "", "", fail(ErrInvalidPlan, unitPath+".outputs", "socket proxy requires exactly output %q", socketProxyOutputRef)
	}
	if err := validateSocketProxyInterfaces(unit, unitPath); err != nil {
		return "", "", err
	}
	networkInstanceRef, err := validateSocketProxyNetworkBinding(unit, siteRef, nodeRef, daemonInstanceRef, unitPath)
	if err != nil {
		return "", "", err
	}
	if err := validateSocketProxyApproval(unit, siteRef, nodeRef, unitPath); err != nil {
		return "", "", err
	}
	return socketPath, networkInstanceRef, nil
}

func validateSocketProxyInterfaces(unit RenderUnit, unitPath string) error {
	var provided []socketProxyProvidedInterfaceContract
	if err := decodeStrict(unit.ProvidedInterfacesJSON(), &provided); err != nil || len(provided) != 1 {
		return wrap(ErrInvalidPlan, unitPath+".providesInterfaces", "decode exact provider interface", errOrCount(err, len(provided), 1))
	}
	if err := validateSocketProxyProvidedInterface(provided[0], unitPath); err != nil {
		return err
	}

	var required []socketProxyRequiredInterfaceContract
	if err := decodeStrict(unit.RequiredInterfacesJSON(), &required); err != nil || len(required) != 1 {
		return wrap(ErrInvalidPlan, unitPath+".requiresInterfaces", "decode exact direct backing interface", errOrCount(err, len(required), 1))
	}
	return validateSocketProxyRequiredInterface(required[0], unitPath)
}

func validateSocketProxyProvidedInterface(p socketProxyProvidedInterfaceContract, unitPath string) error {
	if p.ID != socketProxyInterfaceRef || p.Kind != "docker-http-readonly-v1" || p.Protocol != "docker-http" || p.Version != "v1" ||
		p.Endpoint.Ref != socketProxyEndpointRef || p.Endpoint.Visibility != "node-local" || p.Endpoint.Transport != "tcp" ||
		p.Endpoint.NetworkRef != socketProxyNetworkRef || p.Endpoint.Address != "socket-proxy" || p.Endpoint.Port != 2375 ||
		!exactStringList(p.Scopes, []string{"CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"}) ||
		p.CoLocation != "same-node-and-network" || p.DaemonRef != socketProxyDaemonRef || p.PolicyProfile != socketProxyPolicyProfile {
		return fail(ErrInvalidPlan, unitPath+".providesInterfaces", "provided Docker API interface widens or drifts from the readonly baseline")
	}
	return nil
}

func validateSocketProxyRequiredInterface(r socketProxyRequiredInterfaceContract, unitPath string) error {
	if r.ID != "docker-provider-backing" || r.Kind != dockerSocketDirectInterfaceKind || r.Protocol != "docker-engine" || r.Version != "v1" ||
		r.Endpoint.Visibility != "node-local" || r.Endpoint.Transport != "unix-socket" || r.Endpoint.PathSource != dockerSocketPathSourceDaemonBinding ||
		!exactStringList(r.Scopes, []string{"docker-api:full"}) || r.CoLocation != "same-node" || r.DaemonRef != socketProxyDaemonRef || r.PolicyProfile != socketProxyApprovalPolicy {
		return fail(ErrInvalidPlan, unitPath+".requiresInterfaces", "direct Docker backing interface widens or drifts from its central approval subject")
	}
	return nil
}

func validateSocketProxyNetworkBinding(unit RenderUnit, siteRef, nodeRef, daemonInstanceRef, unitPath string) (string, error) {
	var bindings []rawRuntimeNetworkBinding
	if err := decodeStrict(unit.RuntimeNetworkBindingsJSON(), &bindings); err != nil || len(bindings) != 1 {
		return "", wrap(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID()+".networkBindings", "decode exact provider network binding", errOrCount(err, len(bindings), 1))
	}
	binding := bindings[0]
	wantNetworkID := unit.InstanceID() + "-network-" + socketProxyNetworkRef + "-interface-" + socketProxyInterfaceRef
	if binding.NetworkInstanceRef != wantNetworkID || binding.NetworkRef != socketProxyNetworkRef || binding.Role != "provider" || binding.InterfaceRef != socketProxyInterfaceRef ||
		binding.SiteRef != siteRef || binding.NodeRef != nodeRef || binding.DaemonRef != socketProxyDaemonRef || binding.DaemonInstanceRef != daemonInstanceRef ||
		binding.Owner.ModuleRef != socketProxyModuleID || binding.Owner.UnitRef != socketProxyUnitID || binding.Owner.InstanceRef != unit.InstanceID() || binding.Owner.InterfaceRef != socketProxyInterfaceRef {
		return "", fail(ErrInvalidPlan, unitPath+".instances."+unit.InstanceID()+".networkBindings", "runtime network must be the exact instance-owned internal provider network %q", wantNetworkID)
	}
	return binding.NetworkInstanceRef, nil
}

func validateSocketProxyApproval(unit RenderUnit, siteRef, nodeRef, unitPath string) error {
	var approvals []rawPrivilegedInterfaceApproval
	if err := decodeStrict(unit.PrivilegedInterfaceApprovalsJSON(), &approvals); err != nil || len(approvals) != 1 {
		return wrap(ErrInvalidPlan, unitPath+".privilegedInterfaceApprovals", "decode exact central backing approval", errOrCount(err, len(approvals), 1))
	}
	approval := approvals[0]
	if approval.ID != socketProxyApprovalID || approval.Kind != dockerSocketDirectInterfaceKind || approval.ModuleRef != socketProxyModuleID || approval.UnitRef != socketProxyUnitID ||
		approval.ProviderRef != socketProxyProviderRef || approval.DaemonRef != socketProxyDaemonRef || approval.PolicyProfile != socketProxyApprovalPolicy ||
		approval.ReasonCode != "provider-backing" || approval.EvidenceRef != socketProxyApprovalProof || approval.EvidenceGateRef == "" ||
		!exactStringList(approval.SiteRefs, unit.LogicalSiteRefs()) || !exactStringList(approval.NodeRefs, unit.LogicalNodeRefs()) ||
		!stringListContains(approval.SiteRefs, siteRef) || !stringListContains(approval.NodeRefs, nodeRef) {
		return fail(ErrInvalidPlan, unitPath+".privilegedInterfaceApprovals", "central direct-socket approval does not exactly cover this module, provider, daemon, policy, and placement")
	}
	return nil
}

func exactStringList(left, right []string) bool {
	return slices.Equal(left, right)
}

func stringListContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func errOrCount(err error, got, want int) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("contains %d entries, want %d", got, want)
}
