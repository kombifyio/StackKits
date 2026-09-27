package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

// hostPublicRootBundles are the Linux system CA bundle locations Go itself
// consults; the first readable one keeps public TLS working next to the home
// CA inside the container.
var hostPublicRootBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// homeIdentityCABundle concatenates the host's public roots with the home CA
// root (Basement). A Cloud node has no home CA; its identity route uses public
// TLS, so the public roots alone suffice.
func homeIdentityCABundle(workspace string) ([]byte, error) {
	var bundle bytes.Buffer
	for _, path := range hostPublicRootBundles {
		raw, err := os.ReadFile(path) // #nosec G304 -- fixed system CA bundle locations.
		if err == nil && len(raw) > 0 {
			bundle.Write(raw)
			if !bytes.HasSuffix(raw, []byte("\n")) {
				bundle.WriteByte('\n')
			}
			break
		}
	}
	root, _, err := localevidence.BasementStepCARootCAPEM(workspace)
	switch {
	case err == nil:
		bundle.Write(root)
	case errors.Is(err, localevidence.ErrBasementRuntimeCustodyMissing):
	default:
		return nil, fmt.Errorf("read the home CA root for application trust: %w", err)
	}
	if bundle.Len() == 0 {
		return nil, errors.New("no public or home CA roots are available for application trust")
	}
	return bundle.Bytes(), nil
}

// applyHomeIdentityAccess mounts the CA bundle and preserves the canonical
// Pocket ID issuer. Cloud resolves that host to the router's network alias;
// Basement retains the host gateway path to its published router.
func applyHomeIdentityAccess(
	workspace string,
	coreModuleRef string,
	routingNetwork standaloneComposeNetwork,
	access *architecturev2renderer.ApplicationDeliveryHomeIdentityAccess,
	service *standaloneComposeService,
	configFiles map[string][]byte,
) error {
	if access == nil {
		return nil
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return fmt.Errorf("resolve the home identity provider host: %w", err)
	}
	bundle, err := homeIdentityCABundle(workspace)
	if err != nil {
		return err
	}
	switch coreModuleRef {
	case "stackkits-cloud-core-runtime", "stackkits-cloud-core-standalone-runtime":
		// The caller selects this external network from the plan-bound core
		// owner. Never shadow its canonical issuer alias with host-gateway:
		// that would send same-node identity traffic through host ingress.
		expected, err := standaloneComposeCoreNetwork(coreModuleRef)
		if err != nil || !routingNetwork.External || routingNetwork.Name != expected || !slices.Contains(service.Networks, "stackkit-routing") {
			return errors.New("Cloud identity access requires the plan-bound core routing network")
		}
	default:
		service.ExtraHosts = append(service.ExtraHosts, address.ServiceHost("id")+":host-gateway")
	}
	// The bundle persists beside the other owner-only config files.
	rel := architecturev2renderer.StandaloneComposeConfigRelPath(access.CABundleTarget)
	configFiles[rel] = bundle
	service.Volumes = append(service.Volumes, "./"+rel+":"+access.CABundleTarget+":ro")
	for _, name := range access.CABundleEnvironment {
		service.Environment[name] = access.CABundleTarget
	}
	return nil
}

// applyPocketIDClientEnvironment renders the component's sign-in settings
// from its templates. Values holding the client secret go to the owner-only
// environment file; the others stay in the Compose file.
func applyPocketIDClientEnvironment(
	workspace string,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	component architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	service *standaloneComposeService,
	secretValues map[string]string,
	configFiles map[string][]byte,
) error {
	client := component.PocketIDClient
	if client == nil {
		return nil
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return fmt.Errorf("resolve the home identity provider issuer: %w", err)
	}
	var secret []byte
	if !client.Public {
		secret, err = localevidence.ResolveLocalSecretMaterial(workspace, localowner.ApplicationOIDCClientSecretRef(bundle.WorkloadRef))
		if err != nil {
			return fmt.Errorf("resolve the application's Pocket ID client secret: %w", err)
		}
		defer clear(secret)
	}
	replacer := strings.NewReplacer(
		"{{issuer}}", address.PocketIDOrigin(),
		"{{clientId}}", localowner.ApplicationOIDCClientID(bundle.WorkloadRef),
		"{{clientSecret}}", string(secret),
		"{{origin}}", pocketIDRouteOrigin(bundle.Route),
	)
	for name, template := range client.Environment {
		value := replacer.Replace(template)
		if strings.Contains(template, "{{clientSecret}}") {
			variable := standaloneComposeSecretVariable(component.ID, name)
			secretValues[variable] = value
			service.Environment[name] = "${" + variable + ":?required}"
			continue
		}
		service.Environment[name] = strings.ReplaceAll(value, "$", "$$")
	}
	if configuration := client.Configuration; configuration != nil {
		value := replacer.Replace(configuration.Body)
		rel := architecturev2renderer.StandaloneComposeConfigRelPath(configuration.Target)
		if _, exists := configFiles[rel]; exists {
			return errors.New("Pocket ID client configuration shadows another runtime file")
		}
		configFiles[rel] = []byte(value)
		service.Volumes = append(service.Volumes, "./"+rel+":"+configuration.Target+":ro")
		digest := sha256.Sum256([]byte(configuration.Target + "\x00" + value))
		label := "sha256:" + hex.EncodeToString(digest[:])
		if existing := service.Labels["io.stackkit.config-digest"]; existing != "" {
			combined := sha256.Sum256([]byte(existing + "\x00" + label))
			label = "sha256:" + hex.EncodeToString(combined[:])
		}
		service.Labels["io.stackkit.config-digest"] = label
	}
	return nil
}

func pocketIDRouteOrigin(route architecturev2renderer.ApplicationDeliveryRouteDescriptor) string {
	host := route.Host
	if route.Port != 0 && route.Port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(route.Port))
	}
	return "https://" + host
}

// ensurePocketIDClients registers or converges the Pocket ID client of every
// component that declares one, before its environment is rendered.
func (o *osStandaloneComposeWorkloadOperations) ensurePocketIDClients(ctx context.Context, bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) error {
	for _, component := range bundle.Components {
		if component.PocketIDClient == nil {
			continue
		}
		ensure := o.ensureOIDCClient
		if ensure == nil {
			ensure = ensureApplicationOIDCClientWithLocalOwner
		}
		secretRef := localowner.ApplicationOIDCClientSecretRef(bundle.WorkloadRef)
		if component.PocketIDClient.Public {
			secretRef = ""
		}
		if err := ensure(ctx, o.workspaceRoot, localowner.ApplicationOIDCClientRequest{
			Public:            component.PocketIDClient.Public,
			ClientID:          localowner.ApplicationOIDCClientID(bundle.WorkloadRef),
			RequiredPrivilege: bundle.RequiredPrivilege,
			Name:              "StackKit " + bundle.WorkloadRef,
			CallbackURLs:      renderPocketIDCallbackURLs(component.PocketIDClient.CallbackURLs, pocketIDRouteOrigin(bundle.Route)),
			SecretRef:         secretRef,
		}); err != nil {
			return fmt.Errorf("register the %s Pocket ID client: %w", bundle.WorkloadRef, err)
		}
	}
	return nil
}

func renderPocketIDCallbackURLs(templates []string, origin string) []string {
	callbacks := make([]string, len(templates))
	for index, template := range templates {
		callbacks[index] = strings.ReplaceAll(template, "{{origin}}", origin)
	}
	return callbacks
}

func ensureApplicationOIDCClientWithLocalOwner(ctx context.Context, workspace string, request localowner.ApplicationOIDCClientRequest) error {
	service, err := localowner.NewService(workspace)
	if err != nil {
		return err
	}
	_, err = service.EnsureApplicationOIDCClient(ctx, request)
	if err == nil && request.ClientID == localowner.ApplicationOIDCClientID("photos") {
		return service.EnsureImmichUserClaims(ctx)
	}
	return err
}
