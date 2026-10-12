package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/kombifynotify"
)

// standaloneComposeHostFacts are the node facts a render needs beyond the
// bundle. They are observed or read by prepare, never carried in a plan.
type standaloneComposeHostFacts struct {
	dockerRoot string
	notify     *homeAssistantNotifyFacts
}

// homeAssistantNotifyFacts is the container-network gateway the agent binds
// and the node-held bearer token Home Assistant presents.
type homeAssistantNotifyFacts struct {
	gateway netip.Addr
	token   string
}

const standaloneComposeNetworkGatewayFormat = "{{range .IPAM.Config}}{{.Gateway}}{{println}}{{end}}"

// standaloneComposeNetworkGatewayArgs is the second read-only non-Compose query
// the runner admits: the gateway of one core routing network.
func standaloneComposeNetworkGatewayArgs(network string) []string {
	return []string{"network", "inspect", "--format", standaloneComposeNetworkGatewayFormat, network}
}

func validNetworkGatewayQuery(args []string) bool {
	if len(args) != 5 || args[0] != "network" || args[1] != "inspect" || args[2] != "--format" || args[3] != standaloneComposeNetworkGatewayFormat {
		return false
	}
	return slices.Contains([]string{"stackkit-basement-core", "stackkit-cloud-core", "stackkit-cloud-core-standalone"}, args[4])
}

func homeAssistantNotifyComponent(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) (architecturev2renderer.ApplicationDeliveryComponentDescriptor, bool) {
	for _, component := range bundle.Components {
		if component.HomeAssistantNotify != nil {
			return component, true
		}
	}
	return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, false
}

// resolveHomeAssistantNotify derives the facts of the opt-in kombify notify
// right. The agent can reach Home Assistant only through the gateway address
// of the core routing network the container joins, so that observed address
// is the one the agent binds. Apply creates the token custody once and
// publishes the listen address; inspection only reads existing custody.
func (o *osStandaloneComposeWorkloadOperations) resolveHomeAssistantNotify(
	ctx context.Context,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	ensure bool,
) (*homeAssistantNotifyFacts, error) {
	if _, enabled := homeAssistantNotifyComponent(bundle); !enabled {
		return nil, nil
	}
	if bundle.Route.ID == "" {
		return nil, errors.New("kombify notify needs the routed Smart Home: its container reaches the node agent through the core routing network")
	}
	network, err := standaloneComposeCoreNetwork(bundle.Route.CoreModuleRef)
	if err != nil {
		return nil, err
	}
	raw, err := o.runner.Run(ctx, standaloneComposeNetworkGatewayArgs(network), o.workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("observe the %s network gateway for kombify notify: %w", network, err)
	}
	gateway, err := firstIPv4Gateway(raw)
	if err != nil {
		return nil, fmt.Errorf("kombify notify on network %s: %w", network, err)
	}
	var token string
	if ensure {
		token, err = kombifynotify.Ensure(o.workspaceRoot, gateway)
	} else {
		token, err = kombifynotify.Load(o.workspaceRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("kombify notify custody: %w", err)
	}
	return &homeAssistantNotifyFacts{gateway: gateway, token: token}, nil
}

func firstIPv4Gateway(raw []byte) (netip.Addr, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		address, err := netip.ParseAddr(strings.TrimSpace(line))
		if err == nil && address.Is4() {
			if err := kombifynotify.ValidateGateway(address); err != nil {
				return netip.Addr{}, err
			}
			return address, nil
		}
	}
	return netip.Addr{}, errors.New("the container network has no private IPv4 gateway")
}

// applyHomeAssistantNotify mounts the generated notify platform and its
// secrets file below the descriptor's target. The token lives only in that
// owner-only secrets file; Home Assistant resolves the nearest secrets.yaml
// from the including file's directory, so the owner's /config/secrets.yaml is
// neither read nor shadowed.
func applyHomeAssistantNotify(
	descriptor *architecturev2renderer.HomeAssistantNotifyDescriptor,
	facts *homeAssistantNotifyFacts,
	service *standaloneComposeService,
	configFiles map[string][]byte,
) error {
	if descriptor == nil {
		return nil
	}
	if facts == nil {
		return errors.New("kombify notify requires the observed gateway and its token custody")
	}
	platform, err := kombifynotify.HomeAssistantPlatformYAML(facts.gateway)
	if err != nil {
		return err
	}
	files := map[string]string{
		descriptor.Target + "/notify.yaml":  platform,
		descriptor.Target + "/secrets.yaml": kombifynotify.HomeAssistantSecretsYAML(facts.token),
	}
	digest := sha256.New()
	for _, target := range []string{descriptor.Target + "/notify.yaml", descriptor.Target + "/secrets.yaml"} {
		rel := architecturev2renderer.StandaloneComposeConfigRelPath(target)
		if _, exists := configFiles[rel]; exists {
			return errors.New("kombify notify shadows a governed runtime file")
		}
		configFiles[rel] = []byte(files[target])
		service.Volumes = append(service.Volumes, "./"+rel+":"+target+":ro")
		// The secrets file's digest is a one-way hash of 256 random bits: it
		// recreates the container after a deliberate token rotation without
		// exposing the token.
		fmt.Fprintf(digest, "%s\x00%d\x00%s", target, len(files[target]), files[target])
	}
	label := "sha256:" + hex.EncodeToString(digest.Sum(nil))
	if existing := service.Labels["io.stackkit.config-digest"]; existing != "" {
		combined := sha256.Sum256([]byte(existing + "\x00" + label))
		label = "sha256:" + hex.EncodeToString(combined[:])
	}
	service.Labels["io.stackkit.config-digest"] = label
	return nil
}
