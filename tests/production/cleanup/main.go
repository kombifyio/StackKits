//go:build production

// cleanup deletes stale test simulations from the Sim API.
// A simulation is stale if its name starts with a known test prefix and
// it was created more than 1 hour ago.
//
// Run: go run -tags production ./cleanup/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var staleTestPrefixes = []string{
	"prodtest-",
	"nodetest-",
	"install-test-",
	"init-test-",
	"base-install-test-",
}

func main() {
	cfg := loadConfig()
	client := &simCleanupClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}

	sims, err := client.listSimulations()
	if err != nil {
		log.Fatalf("list simulations: %v", err)
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	deleted, skipped := 0, 0

	for _, s := range sims {
		if !isTestSim(s.Name) {
			skipped++
			continue
		}
		age := time.Since(s.CreatedAt).Round(time.Minute)
		if s.CreatedAt.After(cutoff) {
			fmt.Printf("SKIP   %-40s %s old (< 1h)\n", s.Name, age)
			skipped++
			continue
		}
		fmt.Printf("DELETE %-40s %s old\n", s.Name, age)
		if err := client.deleteSimulation(s.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		} else {
			deleted++
		}
	}

	fmt.Printf("\nResult: deleted %d, kept %d (of %d total)\n", deleted, skipped, len(sims))
}

func isTestSim(name string) bool {
	for _, p := range staleTestPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ─── Config ────────────────────────────────────────────────────────────────

type config struct {
	APIURL string
	APIKey string
	JWT    string
}

func loadConfig() config {
	c := config{
		APIURL: os.Getenv("KOMBIFY_API_URL"),
		APIKey: os.Getenv("KOMBIFY_API_KEY"),
		JWT:    os.Getenv("KOMBIFY_JWT_TOKEN"),
	}
	if c.APIURL == "" {
		c.APIURL = "https://api.kombify.io"
	}
	if c.APIKey == "" && c.JWT == "" {
		log.Fatal("KOMBIFY_API_KEY or KOMBIFY_JWT_TOKEN must be set")
	}
	return c
}

// ─── Minimal Sim client ────────────────────────────────────────────────────

type simEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type simCleanupClient struct {
	cfg  config
	http *http.Client
}

func (c *simCleanupClient) request(method, path string, body any) (*http.Response, error) {
	url := c.cfg.APIURL + path
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("X-Api-Key", c.cfg.APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.cfg.JWT)
	}
	return c.http.Do(req)
}

func (c *simCleanupClient) listSimulations() ([]simEntry, error) {
	resp, err := c.request(http.MethodGet, "/v1/simulation/simulations", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Data []simEntry `json:"data"`
	}
	// Try wrapped format first ({"data": [...]}), fall back to plain array
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err == nil && result.Data != nil {
		return result.Data, nil
	}
	var plain []simEntry
	if err := json.Unmarshal(body, &plain); err != nil {
		return nil, fmt.Errorf("decode simulations: %w", err)
	}
	return plain, nil
}

func (c *simCleanupClient) deleteSimulation(id string) error {
	resp, err := c.request(http.MethodDelete, "/v1/simulation/simulations/"+id, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
