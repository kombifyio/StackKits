package nativehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
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

// applyHomeIdentityAccess makes the Pocket ID host resolve to the node's
// router (published on every host address) and mounts the CA bundle.
func applyHomeIdentityAccess(
	workspace string,
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
	service.ExtraHosts = append(service.ExtraHosts, address.ServiceHost("id")+":host-gateway")
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
) error {
	client := component.PocketIDClient
	if client == nil {
		return nil
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return fmt.Errorf("resolve the home identity provider issuer: %w", err)
	}
	secret, err := localevidence.ResolveLocalSecretMaterial(workspace, localowner.ApplicationOIDCClientSecretRef(bundle.WorkloadRef))
	if err != nil {
		return fmt.Errorf("resolve the application's Pocket ID client secret: %w", err)
	}
	defer clear(secret)
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
		if err := ensure(ctx, o.workspaceRoot, localowner.ApplicationOIDCClientRequest{
			ClientID:    localowner.ApplicationOIDCClientID(bundle.WorkloadRef),
			Name:        "StackKit " + bundle.WorkloadRef,
			CallbackURL: pocketIDRouteOrigin(bundle.Route) + component.PocketIDClient.CallbackPath,
			SecretRef:   localowner.ApplicationOIDCClientSecretRef(bundle.WorkloadRef),
		}); err != nil {
			return fmt.Errorf("register the %s Pocket ID client: %w", bundle.WorkloadRef, err)
		}
	}
	return nil
}

func ensureApplicationOIDCClientWithLocalOwner(ctx context.Context, workspace string, request localowner.ApplicationOIDCClientRequest) error {
	service, err := localowner.NewService(workspace)
	if err != nil {
		return err
	}
	_, err = service.EnsureApplicationOIDCClient(ctx, request)
	return err
}
