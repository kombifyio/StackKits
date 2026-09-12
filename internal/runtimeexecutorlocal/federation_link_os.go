package runtimeexecutorlocal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
)

type OSFederationLinkOperations struct {
	root string
	mu   sync.Mutex
}

func NewOSFederationLinkOperations(root string) *OSFederationLinkOperations {
	return &OSFederationLinkOperations{root: root}
}

// run is internal to this owner. Callers supply only typed policy/custody;
// the invocations below never read WireGuard private keys or execute a shell.
func (o *OSFederationLinkOperations) run(ctx context.Context, input string, name string, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("federation: external WireGuard activation requires Linux")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	command.Stdin = strings.NewReader(input)
	output := &cloudHostSecurityBoundedBuffer{remaining: 64 << 10}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("federation: %s operation failed: %w", name, err)
	}
	if output.exceeded {
		return nil, errors.New("federation: OS readback exceeded bound")
	}
	return output.Bytes(), nil
}

func (o *OSFederationLinkOperations) custody(p FederationLinkApplyPolicy) (WireGuardFabricCustody, error) {
	record, err := loadWireGuardCustody(o.root, p.Binding.FabricRef)
	if err != nil {
		return record.Fabric, err
	}
	owner, err := localevidence.LoadOwnerCustody(o.root)
	if err != nil {
		return record.Fabric, err
	}
	c := record.Fabric
	if owner.Binding != record.Binding || p.SiteRef != record.Binding.SiteRef || p.NodeRef != record.Binding.NodeRef ||
		p.ExecutionChannelRef != record.Binding.ChannelRef || p.SiteKind != c.SiteKind ||
		p.Binding.BindingHash != c.BindingHash || p.Binding.CustodyAttestationRef != c.CustodyAttestationRef ||
		p.Overlay.Implementation != "wireguard" || p.Overlay.Initiation != "local-outbound" ||
		p.Overlay.TrafficMode != "policy-scoped" || len(p.HomeSiteRefs) != 1 || len(p.CloudSiteRefs) != 1 ||
		p.Overlay.AdvertiseDefaultRoute || p.Overlay.AdvertisePrivateSubnets || p.Overlay.AllowBroadRoutes {
		return c, errors.New("federation: policy differs from local external fabric custody")
	}
	issued, err := exactFederationLinkTime(p.Binding.IssuedAt)
	if err != nil {
		return c, err
	}
	until, err := exactFederationLinkTime(p.Binding.ValidUntil)
	if err != nil || time.Now().Before(issued) || !time.Now().Before(until) || until.Sub(issued) > 24*time.Hour {
		return c, errors.New("federation: external binding is not currently valid")
	}
	return c, nil
}

func (o *OSFederationLinkOperations) EstablishInterSiteLink(ctx context.Context, p FederationLinkApplyPolicy) (result FederationLinkObservation, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c, err := o.custody(p)
	if err != nil {
		return result, err
	}
	name := WireGuardFabricInterface(c.FabricRef)
	if err := o.verifyInterface(ctx, c, false, 0); err != nil {
		return result, err
	}
	if err := o.reconcileObsolete(ctx, p); err != nil {
		return result, err
	}
	if _, err := o.run(ctx, "", "ip", "link", "set", "dev", name, "down"); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_, stopErr := o.run(cleanup, "", "ip", "link", "set", "dev", name, "down")
			if stopErr == nil {
				stopErr = setWireGuardActivation(o.root, c.FabricRef, false)
			}
			err = errors.Join(err, stopErr)
		}
	}()
	if err = o.installGuard(ctx, c, p); err != nil {
		return result, err
	}
	if err = setWireGuardActivation(o.root, c.FabricRef, true); err != nil {
		return result, err
	}
	if _, err = o.run(ctx, "", "ip", "link", "set", "dev", name, "up"); err != nil {
		return result, err
	}
	// Linux removes unicast device routes when an interface goes down. Add only
	// the adopted peer /32; never replace a route owned by another interface.
	if _, err = o.run(ctx, "", "ip", "route", "add", c.PeerAddress+"/32", "dev", name); err != nil {
		return result, err
	}
	// Both Sites may apply concurrently. Wait only for the first authenticated
	// WireGuard handshake, with a bounded bootstrap window; no ready receipt is
	// emitted while the other Site is still down.
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		if err = o.verifyInterface(wait, c, true, time.Duration(p.Partition.MaxStaleVerificationSeconds)*time.Second); err == nil {
			break
		}
		select {
		case <-wait.Done():
			return result, fmt.Errorf("federation: initial peer handshake unavailable: %w", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	return o.observe(ctx, c, p, "established")
}

func (o *OSFederationLinkOperations) RemoveObsoleteInterSiteLink(ctx context.Context, p FederationLinkExpectation) (FederationLinkObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c, err := o.custody(p)
	if err != nil {
		return FederationLinkObservation{}, err
	}
	if err := o.reconcileObsolete(ctx, p); err != nil {
		return FederationLinkObservation{}, err
	}
	return o.observe(ctx, c, p, "obsolete-removed")
}

func (o *OSFederationLinkOperations) reconcileObsolete(ctx context.Context, p FederationLinkExpectation) error {
	previous, err := activatedWireGuardCustodies(o.root)
	if err != nil {
		return err
	}
	for _, record := range previous {
		// Only interfaces that this Owner actually activated are reconciled.
		// External keys/interfaces and merely imported custody remain untouched.
		if record.Fabric.FabricRef != p.Binding.FabricRef {
			if err := o.stopInterSiteLink(ctx, record.Fabric.FabricRef); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *OSFederationLinkOperations) VerifyInterSiteLink(ctx context.Context, p FederationLinkExpectation) (FederationLinkObservation, error) {
	return o.verify(ctx, p, "ready")
}

func (o *OSFederationLinkOperations) verify(ctx context.Context, p FederationLinkExpectation, status string) (FederationLinkObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c, err := o.custody(p)
	if err != nil {
		return FederationLinkObservation{}, err
	}
	return o.observe(ctx, c, p, status)
}

// StopInterSiteLink withdraws local activation without deleting the external
// interface, keys, routing setup or custody. Readback must prove it is down.
func (o *OSFederationLinkOperations) StopInterSiteLink(ctx context.Context, fabricRef string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopInterSiteLink(ctx, fabricRef)
}

func (o *OSFederationLinkOperations) stopInterSiteLink(ctx context.Context, fabricRef string) error {
	record, err := loadWireGuardCustody(o.root, fabricRef)
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(o.root)
	if err != nil || owner.Binding != record.Binding {
		return errors.New("federation: stop target differs from local Owner")
	}
	name := WireGuardFabricInterface(fabricRef)
	if _, err := o.run(ctx, "", "ip", "link", "set", "dev", name, "down"); err != nil {
		return err
	}
	raw, err := o.run(ctx, "", "ip", "-j", "link", "show", "dev", name)
	if err != nil {
		return err
	}
	var links []struct {
		Flags []string `json:"flags"`
	}
	if err := json.Unmarshal(raw, &links); err != nil {
		return err
	}
	if len(links) != 1 {
		return errors.New("federation: stopped interface missing")
	}
	for _, flag := range links[0].Flags {
		if flag == "UP" {
			return errors.New("federation: interface remained active")
		}
	}
	return setWireGuardActivation(o.root, fabricRef, false)
}

func (o *OSFederationLinkOperations) verifyInterface(ctx context.Context, c WireGuardFabricCustody, active bool, maxHandshakeAge time.Duration) error {
	name := WireGuardFabricInterface(c.FabricRef)
	key, err := base64.StdEncoding.DecodeString(c.PeerPublicKey)
	if err != nil || len(key) != 32 {
		return errors.New("federation: exact external WireGuard peer key required")
	}
	raw, err := o.run(ctx, "", "wg", "show", name, "allowed-ips")
	if err != nil {
		return err
	}
	if strings.Join(strings.Fields(string(raw)), " ") != c.PeerPublicKey+" "+c.PeerAddress+"/32" {
		return errors.New("federation: external interface has undeclared peers or broad routes")
	}
	raw, err = o.run(ctx, "", "wg", "show", name, "persistent-keepalive")
	if err != nil {
		return err
	}
	want := c.PeerPublicKey + " off"
	if c.SiteKind == "home" {
		want = c.PeerPublicKey + " 25"
	}
	if strings.Join(strings.Fields(string(raw)), " ") != want {
		return errors.New("federation: only Home may initiate periodic link traffic")
	}
	raw, err = o.run(ctx, "", "ip", "-j", "route", "show", "table", "all", "dev", name)
	if err != nil {
		return err
	}
	var routes []struct {
		Dst  string `json:"dst"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		return err
	}
	peerRoute := false
	for _, route := range routes {
		// Linux creates this interface-local multicast entry even with address
		// generation disabled. It cannot match the sole IPv4 WireGuard AllowedIP,
		// and the guard drops all IPv6 input/output/forwarding on the interface.
		if route.Type == "multicast" && route.Dst == "ff00::/8" {
			continue
		}
		if route.Type != "local" && route.Dst != c.PeerAddress && route.Dst != c.PeerAddress+"/32" {
			return errors.New("federation: interface advertises an undeclared route")
		}
		peerRoute = peerRoute || route.Dst == c.PeerAddress || route.Dst == c.PeerAddress+"/32"
	}
	if active {
		if !peerRoute {
			return errors.New("federation: adopted peer route is absent")
		}
		raw, err := o.run(ctx, "", "wg", "show", name, "latest-handshakes")
		if err != nil {
			return err
		}
		fields := strings.Fields(string(raw))
		if len(fields) != 2 || fields[0] != c.PeerPublicKey {
			return errors.New("federation: authenticated peer handshake absent")
		}
		stamp, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || stamp <= 0 || time.Since(time.Unix(stamp, 0)) > maxHandshakeAge || time.Unix(stamp, 0).After(time.Now()) {
			return errors.New("federation: authenticated peer handshake stale")
		}
	}
	return nil
}

func (o *OSFederationLinkOperations) observe(ctx context.Context, c WireGuardFabricCustody, p FederationLinkExpectation, status string) (FederationLinkObservation, error) {
	if err := o.verifyGuard(ctx, c, p); err != nil {
		return FederationLinkObservation{}, err
	}
	if c.SiteKind == "cloud" {
		status, err := localorigin.ProbePeerOrigin(ctx, o.root, c.PeerRef, c.OriginSocket, c.OriginServerName)
		if err != nil {
			return FederationLinkObservation{}, err
		}
		if status < 200 || status >= 400 {
			return FederationLinkObservation{}, errors.New("federation: Home mTLS origin denied workload request")
		}
	} else {
		if _, err := localorigin.Observe(ctx, o.root, c.OriginServerName); err != nil {
			return FederationLinkObservation{}, err
		}
	}
	if err := o.verifyInterface(ctx, c, true, time.Duration(p.Partition.MaxStaleVerificationSeconds)*time.Second); err != nil {
		return FederationLinkObservation{}, err
	}
	if _, err := o.custody(p); err != nil {
		return FederationLinkObservation{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return FederationLinkObservation{
		PolicyDigest: p.PolicyDigest, Status: status, EvaluatedAt: p.EvaluatedAt, ObservedAt: now, ConfigurationObservedAt: now,
		StackID: p.StackID, SiteRef: p.SiteRef, NodeRef: p.NodeRef, SiteKind: p.SiteKind, ExecutionChannelRef: p.ExecutionChannelRef,
		BindingRef: p.Binding.BindingRef, FabricRef: p.Binding.FabricRef, CustodyAttestationRef: p.Binding.CustodyAttestationRef,
		RequirementsHash: p.Binding.RequirementsHash, BindingHash: p.Binding.BindingHash, BridgeContractHash: p.Binding.BridgeContractHash,
		BindingIssuedAt: p.Binding.IssuedAt, BindingValidUntil: p.Binding.ValidUntil,
		HomeSiteRefs: p.HomeSiteRefs, CloudSiteRefs: p.CloudSiteRefs, PeerSiteRefs: p.Overlay.PeerSiteRefs,
		OverlayContractRef: p.Overlay.ContractRef, Implementation: p.Overlay.Implementation, Initiation: p.Overlay.Initiation, TrafficMode: p.Overlay.TrafficMode,
		OnCloudLoss: p.Partition.OnCloudLoss, OnLinkLoss: p.Partition.OnLinkLoss, CloudEdge: p.Partition.CloudEdge,
		MaxStaleVerificationSeconds: p.Partition.MaxStaleVerificationSeconds, LocalIdentityAuthorityAvailable: true, DenyNewCrossSiteSessions: true,
		LocalAgentConfigured: true, PeerAuthenticated: true, CustodyVerified: true, InitiatesLink: c.SiteKind == "home", AcceptsOnlyAuthenticatedPeers: true,
		OutboundEstablished: true, DeclaredFlowsOnly: true, DefaultDeny: true, LocalAuthorityContinues: true, NewCrossSiteSessionsFailClosed: true,
	}, nil
}

// The guard is independent of the external fabric's routing implementation.
// It admits only one service over one authenticated /32 peer, never forwarded
// LAN traffic. The peer set expires in-kernel with the opaque binding even if
// no StackKits process remains running to perform cleanup.
func wireGuardGuard(c WireGuardFabricCustody, ttl time.Duration) []map[string]any {
	name := WireGuardFabricInterface(c.FabricRef)
	object := func(fields map[string]any) map[string]any {
		fields["family"] = "inet"
		fields["table"] = name
		return fields
	}
	match := func(left any, right any) any {
		return map[string]any{"match": map[string]any{"op": "==", "left": left, "right": right}}
	}
	meta := func(key string) any { return map[string]any{"meta": map[string]any{"key": key}} }
	payload := func(protocol, field string) any {
		return map[string]any{"payload": map[string]any{"protocol": protocol, "field": field}}
	}
	commands := []map[string]any{{"add": map[string]any{"table": map[string]any{"family": "inet", "name": name}}}, {"add": map[string]any{"set": object(map[string]any{"name": "peer", "type": "ipv4_addr", "flags": []string{"timeout"}, "timeout": int64(ttl.Seconds()), "elem": []any{c.PeerAddress}})}}}
	for _, chain := range []string{"input", "output", "forward"} {
		commands = append(commands, map[string]any{"add": map[string]any{"chain": object(map[string]any{"name": chain, "type": "filter", "hook": chain, "prio": 0, "policy": "accept"})}})
		if chain != "forward" {
			device, address, port := "iifname", "saddr", "dport"
			if chain == "output" {
				device, address, port = "oifname", "daddr", "sport"
			}
			if c.SiteKind == "cloud" {
				if port == "dport" {
					port = "sport"
				} else {
					port = "dport"
				}
			}
			expr := []any{match(meta(device), name), match(payload("ip", address), "@peer"), match(payload("tcp", port), int(c.ServicePort))}
			if port == "sport" {
				// A source service port alone is not response authority: otherwise
				// Home could originate a new connection to arbitrary Cloud ports.
				expr = append(expr, match(map[string]any{"ct": map[string]any{"key": "state"}}, "established"))
			}
			expr = append(expr, map[string]any{"accept": nil})
			commands = append(commands, map[string]any{"add": map[string]any{"rule": object(map[string]any{"chain": chain, "expr": expr})}})
		}
		devices := []string{"iifname"}
		if chain == "output" {
			devices = []string{"oifname"}
		}
		if chain == "forward" {
			devices = append(devices, "oifname")
		}
		for _, device := range devices {
			commands = append(commands, map[string]any{"add": map[string]any{"rule": object(map[string]any{"chain": chain, "expr": []any{match(meta(device), name), map[string]any{"drop": nil}}})}})
		}
	}
	return commands
}

func (o *OSFederationLinkOperations) installGuard(ctx context.Context, c WireGuardFabricCustody, p FederationLinkApplyPolicy) error {
	until, _ := exactFederationLinkTime(p.Binding.ValidUntil)
	ttl := time.Until(until)
	if ttl < time.Second {
		return errors.New("federation: binding expires before bounded kernel activation")
	}
	commands := wireGuardGuard(c, ttl)
	name := WireGuardFabricInterface(c.FabricRef)
	if _, err := o.run(ctx, "", "nft", "list", "table", "inet", name); err == nil {
		commands = append([]map[string]any{{"delete": map[string]any{"table": map[string]any{"family": "inet", "name": name}}}}, commands...)
	}
	raw, err := json.Marshal(map[string]any{"nftables": commands})
	if err != nil {
		return err
	}
	_, err = o.run(ctx, string(raw), "nft", "-j", "-f", "-")
	return err
}

func (o *OSFederationLinkOperations) verifyGuard(ctx context.Context, c WireGuardFabricCustody, p FederationLinkApplyPolicy) error {
	raw, err := o.run(ctx, "", "nft", "-j", "list", "table", "inet", WireGuardFabricInterface(c.FabricRef))
	if err != nil {
		return err
	}
	var live struct {
		Objects []map[string]map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal(raw, &live); err != nil {
		return err
	}
	var actualRules, actualChains []any
	peerActive := false
	until, _ := exactFederationLinkTime(p.Binding.ValidUntil)
	for _, entry := range live.Objects {
		if rule := entry["rule"]; rule != nil {
			actualRules = append(actualRules, []any{rule["chain"], rule["expr"]})
		}
		if chain := entry["chain"]; chain != nil {
			actualChains = append(actualChains, []any{chain["name"], chain["hook"], chain["type"], chain["prio"], chain["policy"]})
		}
		if set := entry["set"]; set != nil && set["name"] == "peer" && set["type"] == "ipv4_addr" {
			elements, _ := set["elem"].([]any)
			if len(elements) == 1 {
				wrapped, _ := elements[0].(map[string]any)
				element, _ := wrapped["elem"].(map[string]any)
				expires, _ := element["expires"].(float64)
				peerActive = element["val"] == c.PeerAddress && expires > 0 && expires <= time.Until(until).Seconds()+1
			}
		}
	}
	var expectedRules, expectedChains []any
	// Normalize numbers through the same JSON representation as nft readback.
	normalized, _ := json.Marshal(wireGuardGuard(c, time.Minute))
	var desired []map[string]map[string]map[string]any
	_ = json.Unmarshal(normalized, &desired)
	for _, entry := range desired {
		if rule := entry["add"]["rule"]; rule != nil {
			expectedRules = append(expectedRules, []any{rule["chain"], rule["expr"]})
		}
		if chain := entry["add"]["chain"]; chain != nil {
			expectedChains = append(expectedChains, []any{chain["name"], chain["hook"], chain["type"], chain["prio"], chain["policy"]})
		}
	}
	if !peerActive || !reflect.DeepEqual(actualRules, expectedRules) || !reflect.DeepEqual(actualChains, expectedChains) {
		return errors.New("federation: live default-deny guard or binding expiry differs from authority")
	}
	return nil
}
