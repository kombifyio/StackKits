package lancert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare answers DNS-01 for an own domain in the owner's Cloudflare zone
// with the owner's API token (a token limited to that zone with Zone:Read,
// which zoneOf needs to look the zone up by name, and DNS:Edit). It
// publishes only the challenge TXT record under _acme-challenge.<name> and
// removes exactly the record it created; it never touches an address record,
// and the token is never logged or returned in an error.
type Cloudflare struct {
	Token string
	// API overrides the Cloudflare API base (tests).
	API    string
	Client *http.Client

	mu      sync.Mutex
	zones   map[string]string
	records map[string]string
}

func (c *Cloudflare) base() string {
	if c.API != "" {
		return c.API
	}
	return cloudflareAPI
}

func (c *Cloudflare) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("cloudflare: the API is unreachable")
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &envelope) != nil || response.StatusCode >= 300 || !envelope.Success {
		message := "the request failed"
		if len(envelope.Errors) > 0 {
			message = envelope.Errors[0].Message
		}
		return fmt.Errorf("cloudflare: %s (HTTP %d)", message, response.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

// zoneOf finds the zone that holds name: the longest suffix of name the token
// can see as a zone.
func (c *Cloudflare) zoneOf(ctx context.Context, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(name), "."), ".")
	for start := 0; start+1 < len(labels); start++ {
		candidate := strings.Join(labels[start:], ".")
		if id, ok := c.zones[candidate]; ok {
			return id, nil
		}
		var zones []struct {
			ID string `json:"id"`
		}
		if err := c.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil, &zones); err != nil {
			return "", err
		}
		if len(zones) == 1 {
			if c.zones == nil {
				c.zones = map[string]string{}
			}
			c.zones[candidate] = zones[0].ID
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare: no zone of this token holds %s (the token needs Zone:Read and DNS:Edit on the zone)", name)
}

// SetChallenge publishes one DNS-01 value as a TXT record.
func (c *Cloudflare) SetChallenge(ctx context.Context, identifier, value string) error {
	name := "_acme-challenge." + identifier
	zone, err := c.zoneOf(ctx, name)
	if err != nil {
		return err
	}
	var record struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", map[string]any{
		"type": "TXT", "name": name, "content": value, "ttl": 60, "comment": "stackkit ACME DNS-01",
	}, &record); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.records == nil {
		c.records = map[string]string{}
	}
	c.records[name+"\x00"+value] = zone + "/" + record.ID
	return nil
}

// RemoveChallenge deletes the record SetChallenge created for the value.
func (c *Cloudflare) RemoveChallenge(ctx context.Context, identifier, value string) error {
	name := "_acme-challenge." + identifier
	c.mu.Lock()
	ref, ok := c.records[name+"\x00"+value]
	delete(c.records, name+"\x00"+value)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	zone, id, _ := strings.Cut(ref, "/")
	return c.call(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+id, nil, nil)
}

// SetLANAddress is not offered: an own domain's address records are the
// owner's.
func (c *Cloudflare) SetLANAddress(context.Context, string, netip.Addr) error {
	return errors.New("cloudflare: address records of an own domain are not managed by StackKits")
}
