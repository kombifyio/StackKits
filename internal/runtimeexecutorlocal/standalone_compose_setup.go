package runtimeexecutorlocal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// WithStandaloneComposeHTTP extends the existing application adapter with a
// bounded local API session. The caller supplies its already admitted
// deployment, never a URL. Every request rechecks the same Compose container,
// image, persisted configuration, and loopback binding before sending secrets.
func WithStandaloneComposeHTTP(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, run func(*http.Client, string) error) error {
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	o := operations.(*osStandaloneComposeWorkloadOperations)
	return o.withHTTP(ctx, deployment, run)
}

// ObserveStandaloneComposeContainerCustody reads the exact Compose container
// identity for every component in an already admitted deployment. It checks
// the owner-controlled Compose files before and after the daemon readback, and
// accepts stopped and one-shot containers because this is an identity check,
// not a health observation.
func ObserveStandaloneComposeContainerCustody(
	ctx context.Context,
	workspace string,
	deployment SelectedPaaSWorkloadDeployment,
) (map[string]string, error) {
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	o, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return nil, errors.New("standalone Compose operations have an unexpected implementation")
	}
	return o.observeContainerCustody(ctx, deployment)
}

func (o *osStandaloneComposeWorkloadOperations) observeContainerCustody(
	ctx context.Context,
	deployment SelectedPaaSWorkloadDeployment,
) (map[string]string, error) {
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return nil, err
	}
	if err := o.verifyPersisted(project); err != nil {
		return nil, err
	}
	raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return nil, fmt.Errorf("standalone Docker Compose custody observation failed: %w", err)
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return nil, err
	}
	if err := o.verifyPersisted(project); err != nil {
		return nil, err
	}
	return standaloneComposeContainerIdentities(project.bundle.Components, statuses, true)
}

func (o *osStandaloneComposeWorkloadOperations) withHTTP(ctx context.Context, deployment SelectedPaaSWorkloadDeployment, run func(*http.Client, string) error) error {
	if run == nil {
		return errors.New("application setup requires a bounded API action")
	}
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return err
	}
	inspect := func(ctx context.Context) (string, string, error) {
		if err := o.verifyPersisted(project); err != nil {
			return "", "", err
		}
		raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
		if err != nil {
			return "", "", err
		}
		statuses, err := parseStandaloneComposeStatuses(raw)
		if err != nil {
			return "", "", err
		}
		if _, err := observeStandaloneComposeComponentsWithIdentity(project.bundle.Components, statuses, true); err != nil {
			return "", "", err
		}
		entry := statuses[project.bundle.EntryComponent]
		if entry.ID == "" {
			return "", "", errors.New("application setup requires an exact live container identity")
		}
		if err := validateStandaloneComposeRouteReadback(entry, project.bundle.Route); err != nil {
			return "", "", err
		}
		port, err := o.runner.Run(ctx, standaloneComposeArgs(project, "port"), project.directory)
		if err != nil {
			return "", "", err
		}
		address, err := standaloneComposeLoopbackAddress(port)
		return entry.ID, address, err
	}
	identity, address, err := inspect(ctx)
	if err != nil {
		return fmt.Errorf("admit application setup endpoint: %w", err)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("application setup refuses HTTP redirects")
		},
		Transport: setupRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.URL.Scheme != "http" || request.URL.Host != address || request.URL.User != nil {
				return nil, errors.New("application setup request left its admitted loopback endpoint")
			}
			currentID, currentAddress, err := inspect(request.Context())
			if err != nil {
				return nil, err
			}
			if currentID != identity || currentAddress != address {
				return nil, errors.New("application setup container or loopback endpoint changed")
			}
			return transport.RoundTrip(request)
		}),
	}
	if err := run(client, "http://"+address); err != nil {
		return err
	}
	currentID, currentAddress, err := inspect(ctx)
	if err != nil {
		return err
	}
	if currentID != identity || currentAddress != address {
		return errors.New("application setup target changed before result verification")
	}
	return nil
}

type setupRoundTripper func(*http.Request) (*http.Response, error)

func (r setupRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return r(request)
}
