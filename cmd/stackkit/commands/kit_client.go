package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/kitio"
)

// loadAdminClient builds a kitio.AdminClient from CLI args + env fallbacks.
//
// Auth-mode resolution:
//
//  1. SERVICE_AUTH_SECRET (env or secret-store injected) → HS256 service-auth.
//     Preferred mode; works with admin's requireServiceKeyOrAdmin.
//  2. legacyToken (--token flag) or STACKKIT_ADMIN_TOKEN env or
//     KOMBIFY_ADMIN_API_KEY env → Authorization: Bearer.
//     Only works when admin has ALLOW_LEGACY_SERVICE_KEYS=true.
//  3. Neither → unauthenticated (admin will 401).
//
// Endpoint resolution (in order):
//   - explicit `endpoint` arg
//   - $STACKKIT_ADMIN_ENDPOINT
//   - $ADMIN_PUBLIC_API_URL  (shared admin deployment convention; bare host)
//   - $ADMIN_API_URL         (legacy; may already include /api/v1 — stripped)
//
// Returns a configured client + the resolved endpoint, or an error if no
// endpoint could be resolved.
func loadAdminClient(endpoint, legacyToken string) (*kitio.AdminClient, string, error) {
	if endpoint == "" {
		endpoint = os.Getenv("STACKKIT_ADMIN_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = os.Getenv("ADMIN_PUBLIC_API_URL")
	}
	if endpoint == "" {
		endpoint = os.Getenv("ADMIN_API_URL")
	}
	if endpoint == "" {
		return nil, "", fmt.Errorf("admin endpoint required: pass --endpoint, set STACKKIT_ADMIN_ENDPOINT, or set ADMIN_PUBLIC_API_URL")
	}
	// Strip trailing /api/v1 — our routes already include it
	endpoint = strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/api/v1")

	if legacyToken == "" {
		legacyToken = os.Getenv("STACKKIT_ADMIN_TOKEN")
	}
	if legacyToken == "" {
		legacyToken = os.Getenv("KOMBIFY_ADMIN_API_KEY")
	}

	client := kitio.NewAdminClient(endpoint, legacyToken)
	if secret := os.Getenv("SERVICE_AUTH_SECRET"); secret != "" {
		client = client.WithServiceAuth(secret)
	}
	return client, endpoint, nil
}
