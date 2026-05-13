package netenv

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// isPrivateIP
// ---------------------------------------------------------------------------

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		private bool
	}{
		// 10.0.0.0/8
		{"10.x start", "10.0.0.1", true},
		{"10.x middle", "10.42.0.5", true},
		{"10.x end", "10.255.255.254", true},
		// 172.16.0.0/12
		{"172.16.x start", "172.16.0.1", true},
		{"172.16.x end", "172.31.255.254", true},
		{"172 outside", "172.32.0.1", false},
		// 192.168.0.0/16
		{"192.168.x start", "192.168.0.1", true},
		{"192.168.x end", "192.168.255.254", true},
		// Public IPs
		{"public 8.8.8.8", "8.8.8.8", false},
		{"public 1.1.1.1", "1.1.1.1", false},
		{"public 203.0.113.5", "203.0.113.5", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip).To4()
			require.NotNil(t, ip)
			assert.Equal(t, tt.private, isPrivateIP(ip))
		})
	}
}

// ---------------------------------------------------------------------------
// fetchIP (uses httptest)
// ---------------------------------------------------------------------------

func TestFetchIP(t *testing.T) {
	t.Run("returns valid IPv4", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("203.0.113.42\n"))
		}))
		defer srv.Close()

		ip := fetchIP(context.Background(), srv.URL)
		assert.Equal(t, "203.0.113.42", ip)
	})

	t.Run("returns valid IPv6", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("2001:db8::1\n"))
		}))
		defer srv.Close()

		ip := fetchIP(context.Background(), srv.URL)
		assert.Equal(t, "2001:db8::1", ip)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("  198.51.100.10  \n"))
		}))
		defer srv.Close()

		ip := fetchIP(context.Background(), srv.URL)
		assert.Equal(t, "198.51.100.10", ip)
	})

	t.Run("returns empty for invalid IP", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not-an-ip"))
		}))
		defer srv.Close()

		ip := fetchIP(context.Background(), srv.URL)
		assert.Empty(t, ip)
	})

	t.Run("returns empty for HTML response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html><body>blocked</body></html>"))
		}))
		defer srv.Close()

		ip := fetchIP(context.Background(), srv.URL)
		assert.Empty(t, ip)
	})

	t.Run("returns empty on server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		// The function reads the (empty) body and tries to parse — should be empty
		ip := fetchIP(context.Background(), srv.URL)
		assert.Empty(t, ip)
	})

	t.Run("returns empty for unreachable URL", func(t *testing.T) {
		ip := fetchIP(context.Background(), "http://192.0.2.1:1") // TEST-NET, unreachable
		assert.Empty(t, ip)
	})

	t.Run("sets User-Agent header", func(t *testing.T) {
		var gotUA string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			w.Write([]byte("1.2.3.4"))
		}))
		defer srv.Close()

		fetchIP(context.Background(), srv.URL)
		assert.Equal(t, "stackkit/netenv", gotUA)
	})
}

// ---------------------------------------------------------------------------
// isKombifyCloud (env-var path only — /etc/kombify/context not testable here)
// ---------------------------------------------------------------------------

func TestIsKombifyCloud(t *testing.T) {
	t.Run("true when KOMBIFY_CONTEXT=cloud", func(t *testing.T) {
		t.Setenv("KOMBIFY_CONTEXT", "cloud")
		assert.True(t, isKombifyCloud())
	})

	t.Run("false when KOMBIFY_CONTEXT is empty", func(t *testing.T) {
		t.Setenv("KOMBIFY_CONTEXT", "")
		assert.False(t, isKombifyCloud())
	})

	t.Run("false when KOMBIFY_CONTEXT is other value", func(t *testing.T) {
		t.Setenv("KOMBIFY_CONTEXT", "dev")
		assert.False(t, isKombifyCloud())
	})
}

// ---------------------------------------------------------------------------
// FormatEnvironment
// ---------------------------------------------------------------------------

func TestFormatEnvironment(t *testing.T) {
	tests := []struct {
		env      models.NetworkEnvironment
		expected string
	}{
		{models.NetEnvHome, "Home/office network (behind NAT)"},
		{models.NetEnvVPS, "VPS/dedicated server (public IP)"},
		{models.NetEnvCloud, "kombify Cloud (managed)"},
		{models.NetEnvUnknown, "Unknown"},
		{"something-else", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.env), func(t *testing.T) {
			assert.Equal(t, tt.expected, FormatEnvironment(tt.env))
		})
	}
}

// ---------------------------------------------------------------------------
// SuggestDomain
// ---------------------------------------------------------------------------

func TestSuggestDomain(t *testing.T) {
	t.Run("cloud always returns kombify.me", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvCloud, "stack.local")
		assert.Equal(t, models.DomainKombifyMe, domain)
		assert.Contains(t, reason, "kombify Cloud")
	})

	t.Run("cloud overrides custom domain", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvCloud, "mydomain.com")
		assert.Equal(t, models.DomainKombifyMe, domain)
		assert.NotEmpty(t, reason)
	})

	t.Run("VPS with local domain suggests kombify.me", func(t *testing.T) {
		localDomains := []string{
			"stack.local", "home.lab", "my.lan", "my.home",
			"lab.homebase", "home.localhost", "homelab", "",
		}
		for _, d := range localDomains {
			domain, reason := SuggestDomain(models.NetEnvVPS, d)
			assert.Equal(t, models.DomainKombifyMe, domain, "domain=%q should be corrected", d)
			assert.NotEmpty(t, reason, "domain=%q should have a reason", d)
		}
	})

	t.Run("VPS with real domain keeps it", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvVPS, "mylab.example.com")
		assert.Equal(t, "mylab.example.com", domain)
		assert.Empty(t, reason)
	})

	t.Run("home with empty domain defaults to Kombify Point home zone", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvHome, "")
		assert.Equal(t, models.DomainHomeLab, domain)
		assert.Contains(t, reason, "home network")
	})

	t.Run("home with existing domain keeps it", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvHome, "stack.local")
		assert.Equal(t, "stack.local", domain)
		assert.Empty(t, reason)
	})

	t.Run("unknown keeps whatever was set", func(t *testing.T) {
		domain, reason := SuggestDomain(models.NetEnvUnknown, "whatever.test")
		assert.Equal(t, "whatever.test", domain)
		assert.Empty(t, reason)
	})
}

// ---------------------------------------------------------------------------
// interfaceHasIP
// ---------------------------------------------------------------------------

func TestInterfaceHasIP(t *testing.T) {
	t.Run("invalid IP returns false", func(t *testing.T) {
		assert.False(t, interfaceHasIP("not-an-ip"))
	})

	t.Run("empty IP returns false", func(t *testing.T) {
		assert.False(t, interfaceHasIP(""))
	})

	t.Run("loopback returns true", func(t *testing.T) {
		// 127.0.0.1 is always on the loopback interface
		assert.True(t, interfaceHasIP("127.0.0.1"))
	})

	t.Run("random public IP returns false", func(t *testing.T) {
		// 198.51.100.0/24 is TEST-NET-2, should never be on a local interface
		assert.False(t, interfaceHasIP("198.51.100.42"))
	})
}

// ---------------------------------------------------------------------------
// Result marshaling (roundtrip via capabilities.json)
// ---------------------------------------------------------------------------

func TestResultJSON(t *testing.T) {
	r := &Result{
		Environment:        models.NetEnvVPS,
		PublicIP:           "203.0.113.5",
		PrivateIP:          "10.0.0.2",
		IsNAT:              false,
		HasPublicInterface: true,
	}

	data, err := json.Marshal(r)
	require.NoError(t, err)

	var out Result
	require.NoError(t, json.Unmarshal(data, &out))

	assert.Equal(t, r.Environment, out.Environment)
	assert.Equal(t, r.PublicIP, out.PublicIP)
	assert.Equal(t, r.PrivateIP, out.PrivateIP)
	assert.Equal(t, r.IsNAT, out.IsNAT)
	assert.Equal(t, r.HasPublicInterface, out.HasPublicInterface)
}

// ---------------------------------------------------------------------------
// Detect (integration-style — verifies real local network detection)
// ---------------------------------------------------------------------------

func TestDetect(t *testing.T) {
	t.Run("cloud env var forces cloud environment", func(t *testing.T) {
		t.Setenv("KOMBIFY_CONTEXT", "cloud")

		result := Detect(context.Background())
		assert.Equal(t, models.NetEnvCloud, result.Environment)
	})

	t.Run("without cloud env detects some environment", func(t *testing.T) {
		t.Setenv("KOMBIFY_CONTEXT", "")

		result := Detect(context.Background())
		// On a dev machine this will be home or unknown — just verify it runs
		assert.NotNil(t, result)
		assert.NotEmpty(t, result.Environment)
		// Private IP should be found on any machine with a network interface
		if result.PrivateIP != "" {
			ip := net.ParseIP(result.PrivateIP)
			assert.NotNil(t, ip, "PrivateIP should be a valid IP")
		}
	})
}
