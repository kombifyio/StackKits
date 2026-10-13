package appsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const AdguardHomePolicyAction = "adguard-home-policy-check"

// VerifyAdguardHomePolicy observes the installed declarative policy through
// the admitted application API. check_host forces filtering on internally,
// so its decisions alone cannot prove the live server/client configuration.
// This action neither changes DNS policy nor proves client DNS connectivity.
func VerifyAdguardHomePolicy(ctx context.Context, client *http.Client, baseURL string, installed architecturev2renderer.AdguardHomeWorkloadBundleDescriptor, password []byte) error {
	base, err := url.Parse(baseURL)
	if ctx == nil || client == nil || client.Transport == nil || err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return errors.New("Network policy check requires the admitted application endpoint and transport")
	}
	if len(password) == 0 || len(password) > 4096 || strings.ContainsAny(string(password), "\x00\r\n") || len(installed.DNSPolicy.SelectedClients) == 0 || len(installed.DNSPolicy.BlockedDomains) == 0 || installed.LocalDomain == "" || installed.Release == "" {
		return errors.New("Network policy check requires installed intent and existing owner custody")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session := &http.Client{Transport: client.Transport, Timeout: client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, destination any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base.String(), "/")+"/control/"+path, nil)
		if err != nil {
			return errors.New("cannot construct Network policy observation")
		}
		req.SetBasicAuth("owner", string(password))
		req.Header.Set("Accept", "application/json")
		resp, err := session.Do(req)
		if err != nil {
			return errors.New("Network policy observation failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("Network policy observation refused (HTTP %d)", resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		defer clear(raw)
		if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, destination) != nil {
			return errors.New("Network policy observation is invalid or exceeds its response boundary")
		}
		return nil
	}
	var status struct {
		Running    *bool  `json:"running"`
		Protection *bool  `json:"protection_enabled"`
		DNSPort    int    `json:"dns_port"`
		Version    string `json:"version"`
	}
	if err := get("status", &status); err != nil {
		return err
	}
	if status.Running == nil || !*status.Running || status.Protection == nil || !*status.Protection || status.DNSPort != 53535 || strings.TrimPrefix(status.Version, "v") != strings.TrimPrefix(installed.Release, "v") {
		return errors.New("Network DNS server is stopped, unprotected or differs from the installed endpoint/version")
	}
	var dns struct {
		Upstreams []string `json:"upstream_dns"`
	}
	if err := get("dns_info", &dns); err != nil {
		return err
	}
	if len(dns.Upstreams) != 1 || dns.Upstreams[0] != installed.DNSPolicy.LocalResolver {
		return errors.New("Network upstream differs from the plan's existing local resolver")
	}
	var filter struct {
		Enabled *bool `json:"enabled"`
	}
	if err := get("filtering/status", &filter); err != nil {
		return err
	}
	if filter.Enabled == nil || *filter.Enabled {
		return errors.New("Network filtering is no longer limited to selected clients")
	}
	var clients struct {
		Clients []struct {
			IDs       []string `json:"ids"`
			Global    *bool    `json:"use_global_settings"`
			Filtering *bool    `json:"filtering_enabled"`
		} `json:"clients"`
	}
	if err := get("clients", &clients); err != nil {
		return err
	}
	if len(clients.Clients) != len(installed.DNSPolicy.SelectedClients) {
		return errors.New("Network selected client policy drifted; re-apply the plan")
	}
	check := func(host, address string, blocked bool) error {
		var decision struct {
			Reason string `json:"reason"`
		}
		values := url.Values{"name": {host}, "client": {address}, "qtype": {"A"}}
		if err := get("filtering/check_host?"+values.Encode(), &decision); err != nil {
			return err
		}
		if blocked && decision.Reason != "FilteredBlackList" || !blocked && decision.Reason != "NotFilteredNotFound" {
			return errors.New("Network filter decision differs from installed blocked domains or local zone")
		}
		return nil
	}
	for index, selected := range installed.DNSPolicy.SelectedClients {
		prefix, err := netip.ParsePrefix(selected)
		if err != nil {
			return errors.New("Network installed client range is invalid")
		}
		found := false
		for _, current := range clients.Clients {
			if slices.Contains(current.IDs, selected) {
				if len(current.IDs) != 1 || current.Global == nil || *current.Global || current.Filtering == nil || !*current.Filtering {
					return errors.New("Network selected client filtering is inactive or has drifted")
				}
				found = true
			}
		}
		if !found {
			return errors.New("Network selected client is absent from the installed application")
		}
		domains := installed.DNSPolicy.BlockedDomains
		if index > 0 {
			domains = domains[:1]
		}
		for _, domain := range domains {
			if err := check(domain, prefix.Addr().String(), true); err != nil {
				return err
			}
		}
		if err := check("kit."+installed.LocalDomain, prefix.Addr().String(), false); err != nil {
			return err
		}
	}
	return nil
}
