package api

import (
	"context"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/appsetup"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

func (s *Server) runHomeAssistantOwnerBootstrap(ctx context.Context) (map[string]string, *skerrors.StackKitError) {
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupHomeAssistantURL, "http://home-assistant:8123"), "/")
	password := s.config.SetupAdminPassword
	if strings.TrimSpace(password) == "" {
		return nil, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Home Assistant owner bootstrap requires StackKit admin credentials",
			skerrors.WithSuggestion("Set STACKKIT_ADMIN_PASSWORD for stackkit-server"),
		)
	}
	client := appsetup.NewImmichHTTPClient()
	defer client.CloseIdleConnections()
	result, err := appsetup.BootstrapHomeAssistantOwner(ctx, client, baseURL, appsetup.HomeAssistantOwnerRequest{
		Username:        "homelab",
		Password:        password,
		DisplayName:     "Homelab",
		Language:        "en",
		ExpectedVersion: appsetup.HomeAssistantPinnedVersion,
	})
	if err != nil {
		return nil, skerrors.NewDependencyError("home_assistant_owner_bootstrap_failed", "failed to create the Homelab Home Assistant owner", skerrors.WithCause(err))
	}
	return map[string]string{
		"displayName":        "Homelab",
		"username":           "homelab",
		"userId":             result.UserID,
		"role":               "owner",
		"status":             "owner-verified",
		"version":            result.Version,
		"serverInitialized":  strconv.FormatBool(result.ServerInitialized),
		"ownerVerified":      strconv.FormatBool(result.UserIsOwner),
		"adminVerified":      strconv.FormatBool(result.UserIsAdmin),
		"onboardingComplete": strconv.FormatBool(result.OnboardingComplete),
	}, nil
}
