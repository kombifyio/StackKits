package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

func (s *Server) runHomeAssistantOwnerBootstrap(ctx context.Context) (map[string]string, *skerrors.StackKitError) {
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupHomeAssistantURL, "http://home-assistant:8123"), "/")
	password := strings.TrimSpace(s.config.SetupAdminPassword)
	if password == "" {
		return nil, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Home Assistant owner bootstrap requires StackKit admin credentials",
			skerrors.WithSuggestion("Set STACKKIT_ADMIN_PASSWORD for stackkit-server"),
		)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	evidence, err := bootstrapHomeAssistantOwner(ctx, client, baseURL, "Homelab", "homelab", password)
	if err != nil {
		return nil, skerrors.NewDependencyError("home_assistant_owner_bootstrap_failed", "failed to create the Homelab Home Assistant owner", skerrors.WithCause(err))
	}
	return evidence, nil
}

func bootstrapHomeAssistantOwner(ctx context.Context, client *http.Client, baseURL, name, username, password string) (map[string]string, error) {
	onboarding, err := homeAssistantGetOnboarding(ctx, client, baseURL)
	if err != nil {
		return nil, err
	}
	if onboarding.UserDone {
		return map[string]string{
			"displayName": name,
			"username":    username,
			"role":        "owner",
			"status":      "already-complete",
			"api":         "/api/onboarding/users",
		}, nil
	}
	payload, err := json.Marshal(map[string]string{
		"client_id": "http://stackkit",
		"name":      name,
		"username":  username,
		"password":  password,
		"language":  "en",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/onboarding/users", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("home assistant onboarding users returned %d", resp.StatusCode)
	}
	_ = body
	return map[string]string{
		"displayName": name,
		"username":    username,
		"role":        "owner",
		"status":      "created",
		"api":         "/api/onboarding/users",
	}, nil
}

type homeAssistantOnboarding struct {
	UserDone bool
}

func homeAssistantGetOnboarding(ctx context.Context, client *http.Client, baseURL string) (homeAssistantOnboarding, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/onboarding", nil)
	if err != nil {
		return homeAssistantOnboarding{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return homeAssistantOnboarding{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return homeAssistantOnboarding{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return homeAssistantOnboarding{UserDone: true}, nil
	}
	if resp.StatusCode >= 300 {
		return homeAssistantOnboarding{}, fmt.Errorf("home assistant onboarding status returned %d", resp.StatusCode)
	}
	var steps []struct {
		Step string `json:"step"`
		Done bool   `json:"done"`
	}
	if err := json.Unmarshal(body, &steps); err != nil {
		return homeAssistantOnboarding{}, err
	}
	for _, step := range steps {
		if step.Step == "user" && step.Done {
			return homeAssistantOnboarding{UserDone: true}, nil
		}
	}
	return homeAssistantOnboarding{}, nil
}
