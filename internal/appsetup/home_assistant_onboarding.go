package appsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Complete only the pinned HA onboarding markers after authenticated owner
// verification. These endpoints keep the current core settings and analytics
// preferences; they do not choose a location, configure devices or opt in to
// telemetry. See core 2026.7.2 components/onboarding/views.py.
func completeHomeAssistantOnboarding(ctx context.Context, client *http.Client, baseURL, token string) error {
	for _, step := range []string{"core_config", "analytics", "integration"} {
		current, err := readHomeAssistantOnboarding(ctx, client, baseURL, token)
		if err != nil {
			return err
		}
		if current.notFound || current.complete {
			return nil
		} // Caller verifies final state against running config.
		pending := false
		for _, observed := range current.steps {
			if observed.Step == step {
				pending = !observed.Done
			}
		}
		if !pending {
			continue
		}
		body := map[string]string{}
		if step == "integration" {
			body = map[string]string{"client_id": homeAssistantClientID, "redirect_uri": homeAssistantRedirectURI}
		}
		// The integration response contains a one-use authorization code. Discard it;
		// our authenticated setup session is already registered for revocation.
		err = homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/onboarding/"+step, body, token, nil)
		if err != nil {
			var statusErr *homeAssistantHTTPError
			if !errors.As(err, &statusErr) || statusErr.status != http.StatusForbidden {
				return fmt.Errorf("Home Assistant %s onboarding failed: %w", step, err)
			}
		}
		readback, readErr := readHomeAssistantOnboarding(ctx, client, baseURL, token)
		if readErr != nil {
			return readErr
		}
		if readback.notFound {
			continue
		} // May disappear when final step finishes; final authenticated config is required.
		completed := false
		for _, observed := range readback.steps {
			if observed.Step == step {
				completed = observed.Done
			}
		}
		if !completed {
			return fmt.Errorf("Home Assistant %s onboarding was not completed", step)
		}
	}
	return nil
}
