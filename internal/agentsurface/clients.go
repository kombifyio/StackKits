package agentsurface

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AgentDirRelPath = ".stackkit/agent"

func WriteProductMCPClients(root string, spec map[string]any, doc Document) error {
	domain := specDomainBase(spec)
	dir := filepath.Join(root, filepath.FromSlash(AgentDirRelPath))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create agent directory: %w", err)
	}
	for _, surface := range doc.UseCases {
		for _, mcp := range surface.ProductMCPs {
			if !mcp.GenerateClientConfig {
				continue
			}
			url := productMCPURL(mcp.ID, mcp.Endpoint, domain)
			body := map[string]any{
				"name":      mcp.ID,
				"url":       url,
				"transport": mcp.Transport,
				"auth":      "oauth",
				"oauth": map[string]any{
					"clientIdNote": "Home Assistant IndieAuth uses the MCP client base URL as client_id. StackKits never writes an access token here.",
				},
			}
			data, err := json.MarshalIndent(body, "", "  ")
			if err != nil {
				return err
			}
			path := filepath.Join(dir, mcp.ID+".mcp.json")
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				return fmt.Errorf("write %s MCP client config: %w", mcp.ID, err)
			}
		}
		if surface.Ref == "smart-home" {
			owner := map[string]any{
				"displayName": "Homelab",
				"username":    "homelab",
				"role":        "owner",
				"bootstrap":   "home-assistant-onboarding",
				"api":         "/api/onboarding/users",
				"url":         productUIURL("home-assistant", domain),
			}
			data, err := json.MarshalIndent(owner, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "home-assistant-owner.json"), append(data, '\n'), 0o600); err != nil {
				return fmt.Errorf("write Home Assistant owner intent: %w", err)
			}
		}
	}
	return nil
}

func specDomainBase(spec map[string]any) string {
	network, _ := spec["network"].(map[string]any)
	domain, _ := network["domain"].(map[string]any)
	base, _ := domain["base"].(string)
	return strings.TrimSpace(base)
}

func productMCPURL(id, endpoint, domain string) string {
	path := strings.TrimSpace(endpoint)
	if path == "" {
		path = "/api/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return productUIURL(id, domain) + path
}

func productUIURL(id, domain string) string {
	if domain == "" {
		domain = "home.test"
	}
	host := id
	if id == "home-assistant" {
		host = "smart-home"
	}
	return "https://" + host + "." + domain
}
