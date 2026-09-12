package hostpreflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ListenerRequirement is a projection of a verified compiler listener. Container
// target ports, URLs and observed sockets never become requirements.
type ListenerRequirement struct {
	ID          string `json:"id"`
	NodeRef     string `json:"nodeRef"`
	Transport   string `json:"transport"`
	BindAddress string `json:"bindAddress"`
	Port        int    `json:"port"`
}

func ListenersFromPlan(plan resolvedplan.ResolvedPlan, nodeRef string) ([]ListenerRequirement, error) {
	if nodeRef == "" {
		return nil, errors.New("host baseline requires an exact local node")
	}
	nodesJSON, err := json.Marshal(plan["nodes"])
	if err != nil {
		return nil, err
	}
	var nodes []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(nodesJSON, &nodes); err != nil {
		return nil, err
	}
	found := false
	for _, node := range nodes {
		if node.ID == nodeRef && node.Enabled {
			found = true
		}
	}
	if !found {
		return nil, errors.New("host baseline node is not enabled in the verified plan")
	}
	raw, err := json.Marshal(plan["network"])
	if err != nil {
		return nil, err
	}
	var network struct {
		RuntimeListeners []ListenerRequirement `json:"runtimeListeners"`
	}
	if err := json.Unmarshal(raw, &network); err != nil {
		return nil, err
	}
	if network.RuntimeListeners == nil {
		return nil, errors.New("verified plan has no runtime listener authority")
	}
	result := make([]ListenerRequirement, 0)
	for _, listener := range network.RuntimeListeners {
		if listener.NodeRef != nodeRef {
			continue
		}
		if _, err := listenerAddress(listener); err != nil {
			return nil, err
		}
		result = append(result, listener)
	}
	return result, nil
}

func listenerAddress(listener ListenerRequirement) (netip.Addr, error) {
	address, err := netip.ParseAddr(listener.BindAddress)
	if err != nil || listener.Port < 1 || listener.Port > 65535 || (listener.Transport != "tcp" && listener.Transport != "udp") {
		return netip.Addr{}, fmt.Errorf("invalid compiler listener %q", listener.ID)
	}
	return address.Unmap(), nil
}

// ObserveListeners measures only the exact host bindings being changed. A bind
// probe is immediately closed, not a reservation. Apply must still handle a
// foreign process winning the race afterwards without stopping that process.
func ObserveListeners(ctx context.Context, workspace string, listeners []ListenerRequirement) []PortFact {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	facts := make([]PortFact, 0, len(listeners))
	if len(listeners) == 0 {
		return facts
	}
	containers, containersObserved := inspectHostContainers(ctx, workspace)
	intents, intentsObserved := observeSocketIntents(ctx, workspace)
	for _, listener := range listeners {
		fact := PortFact{ListenerID: listener.ID, Transport: listener.Transport, BindAddress: listener.BindAddress, Port: listener.Port, Namespace: "host"}
		address, err := listenerAddress(listener)
		if err == nil {
			network := listener.Transport + "4"
			if address.Is6() {
				network = listener.Transport + "6"
				if address.IsUnspecified() {
					network = listener.Transport
				}
			}
			endpoint := net.JoinHostPort(address.String(), strconv.Itoa(listener.Port))
			var config net.ListenConfig
			if listener.Transport == "tcp" {
				var socket net.Listener
				socket, err = config.Listen(ctx, network, endpoint)
				if err == nil {
					err = socket.Close()
				}
			} else {
				var socket net.PacketConn
				socket, err = config.ListenPacket(ctx, network, endpoint)
				if err == nil {
					err = socket.Close()
				}
			}
		}
		fact.Observed = err == nil || errors.Is(err, syscall.EADDRINUSE)
		fact.InUse = errors.Is(err, syscall.EADDRINUSE)
		if err != nil {
			fact.Detail = boundedDiagnostic(err.Error())
		}
		matching := 0
		for _, container := range containers {
			if containerBindingOverlaps(container, listener) {
				matching++
				fact.InUse = true
				fact.Detail = "Existing container binding or stopped-container intent: " + container.ID
			}
		}
		// Docker can publish through NAT without any userspace listening socket.
		// Missing daemon access therefore cannot prove a binding is free.
		if !containersObserved {
			fact.Observed = false
			fact.Detail = "Container binding inventory is unavailable or incomplete"
		}
		if fact.InUse && containersObserved && matching == 1 {
			fact.OwnedByCurrentRuntime = currentWorkspaceOwnsListener(ctx, workspace, listener)
		}
		for _, intent := range intents {
			if socketIntentOverlaps(intent, listener) {
				fact.InUse, fact.OwnedByCurrentRuntime = true, false
				fact.Detail = "Existing systemd socket intent: " + intent.Unit
			}
		}
		if !intentsObserved {
			fact.Observed = false
			fact.Detail = "System socket intent inventory is unavailable or incomplete"
		}
		facts = append(facts, fact)
	}
	return facts
}

func observePorts(ctx context.Context, workspace string, ports []int) []PortFact {
	listeners := make([]ListenerRequirement, 0, len(ports))
	for _, port := range ports {
		listeners = append(listeners, ListenerRequirement{Transport: "tcp", BindAddress: "0.0.0.0", Port: port})
	}
	return ObserveListeners(ctx, workspace, listeners)
}

// EvaluateListenerAdmission is the mandatory collision check when optional
// resource diagnostics are skipped. It cannot turn missing binding evidence
// into permission to mutate the host.
func EvaluateListenerAdmission(ctx context.Context, request ObserveRequest, kitSlug string) Report {
	facts := Facts{Ports: ObserveListeners(ctx, request.WorkspacePath, request.RequiredListeners), Baseline: observeBaseline(ctx, request)}
	check := checkPorts(facts)
	return Report{SchemaVersion: SchemaVersion, Policy: PolicySkip, KitSlug: kitSlug, Facts: facts, Checks: []Check{check}, Status: check.Status, Admitted: check.Status != StatusBlocked}
}
