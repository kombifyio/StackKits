package hostpreflight

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
)

type socketIntent struct {
	ListenerRequirement
	Unit string
}

// Loaded systemd sockets include inactive activation intents. They are existing
// host ownership, not StackKits reservations and not evidence of a healthy app.
func observeSocketIntents(ctx context.Context, workspace string) ([]socketIntent, bool) {
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return nil, false
	}
	if strings.TrimSpace(string(pid1)) != "systemd" {
		return nil, true
	}
	raw, ok := boundedProbeOutput(ctx, workspace, "systemctl", "list-sockets", "--all", "--show-types", "--no-legend", "--no-pager")
	if !ok {
		return nil, false
	}
	intents := []socketIntent{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			if strings.TrimSpace(line) != "" {
				return nil, false
			}
			continue
		}
		if strings.HasPrefix(fields[0], "/") || strings.HasPrefix(fields[0], "@") {
			continue
		}
		transport := ""
		switch fields[1] {
		case "Stream":
			transport = "tcp"
		case "Datagram":
			transport = "udp"
		default:
			continue
		}
		address, portText, err := net.SplitHostPort(fields[0])
		if err != nil {
			address, portText = "::", fields[0]
		}
		if address == "*" || address == "" {
			address = "::"
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, false
		}
		listener := ListenerRequirement{Transport: transport, BindAddress: address, Port: port}
		if _, err := listenerAddress(listener); err != nil {
			return nil, false
		}
		intents = append(intents, socketIntent{ListenerRequirement: listener, Unit: fields[2]})
	}
	return intents, true
}

func socketIntentOverlaps(intent socketIntent, listener ListenerRequirement) bool {
	if intent.Transport != listener.Transport || intent.Port != listener.Port {
		return false
	}
	a, err := listenerAddress(intent.ListenerRequirement)
	if err != nil {
		return true
	}
	b, err := listenerAddress(listener)
	if err != nil {
		return true
	}
	return a == b || (a.Is4() == b.Is4() && (a.IsUnspecified() || b.IsUnspecified())) || (a.Is6() && a.IsUnspecified()) || (b.Is6() && b.IsUnspecified())
}
