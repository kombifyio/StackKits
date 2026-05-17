// Package cluster contains local cluster bootstrap primitives.
package cluster

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const JoinTokenVersion = "stackkit.cluster/v1"

type MainNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

type JoinToken struct {
	Version      string    `json:"version"`
	HomelabID    string    `json:"homelabId"`
	MainNode     MainNode  `json:"mainNode"`
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expiresAt"`
	AllowedRoles []string  `json:"allowedRoles"`
}

func GenerateJoinToken(homelabID string, main MainNode, ttl time.Duration) (*JoinToken, error) {
	homelabID = strings.TrimSpace(homelabID)
	if homelabID == "" {
		return nil, fmt.Errorf("homelab id is required")
	}
	main.ID = strings.TrimSpace(main.ID)
	main.Name = strings.TrimSpace(main.Name)
	main.Endpoint = strings.TrimSpace(main.Endpoint)
	if main.ID == "" {
		return nil, fmt.Errorf("main node id is required")
	}
	if main.Name == "" {
		main.Name = main.ID
	}
	if main.Endpoint == "" {
		return nil, fmt.Errorf("main node endpoint is required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate join token: %w", err)
	}

	return &JoinToken{
		Version:      JoinTokenVersion,
		HomelabID:    homelabID,
		MainNode:     main,
		Token:        "skj_" + base64.RawURLEncoding.EncodeToString(raw),
		ExpiresAt:    time.Now().UTC().Add(ttl),
		AllowedRoles: []string{"worker", "storage"},
	}, nil
}
