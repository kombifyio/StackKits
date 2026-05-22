package main

import (
	"testing"
)

func TestResolveAPIKey(t *testing.T) {
	t.Run("requires key by default", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")
		t.Setenv("STACKKITS_ALLOW_UNAUTHENTICATED", "")

		key, err := resolveAPIKey("", false, false)

		if err == nil {
			t.Fatal("resolveAPIKey() error = nil, want required-key error")
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})

	t.Run("uses flag value", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "env-key")

		key, err := resolveAPIKey("flag-key", false, false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "flag-key" {
			t.Fatalf("resolveAPIKey() key = %q, want flag-key", key)
		}
	})

	t.Run("uses env value", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "env-key")

		key, err := resolveAPIKey("", false, false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "env-key" {
			t.Fatalf("resolveAPIKey() key = %q, want env-key", key)
		}
	})

	t.Run("allows explicit unauthenticated local mode", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")

		key, err := resolveAPIKey("", true, false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})

	t.Run("allows env unauthenticated local mode", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")
		t.Setenv("STACKKITS_ALLOW_UNAUTHENTICATED", "true")

		key, err := resolveAPIKey("", false, false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})

	t.Run("rejects unauthenticated production profile", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")
		t.Setenv("STACKKITS_ALLOW_UNAUTHENTICATED", "true")

		key, err := resolveAPIKey("", false, true)

		if err == nil {
			t.Fatal("resolveAPIKey() error = nil, want production guard error")
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})

	t.Run("rejects production bypass even when key is set", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "env-key")

		key, err := resolveAPIKey("", true, true)

		if err == nil {
			t.Fatal("resolveAPIKey() error = nil, want production guard error")
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})
}

func TestResolveCORSOrigins(t *testing.T) {
	t.Run("empty disables browser CORS", func(t *testing.T) {
		t.Setenv("STACKKITS_CORS_ORIGINS", "")
		t.Setenv("STACKKITS_ALLOW_WILDCARD_CORS", "")

		origins, err := resolveCORSOrigins("", false, false)

		if err != nil {
			t.Fatalf("resolveCORSOrigins() unexpected error: %v", err)
		}
		if origins != nil {
			t.Fatalf("resolveCORSOrigins() = %#v, want nil", origins)
		}
	})

	t.Run("wildcard requires explicit local override", func(t *testing.T) {
		t.Setenv("STACKKITS_CORS_ORIGINS", "")

		origins, err := resolveCORSOrigins("", true, false)

		if err != nil {
			t.Fatalf("resolveCORSOrigins() unexpected error: %v", err)
		}
		if len(origins) != 1 || origins[0] != "*" {
			t.Fatalf("resolveCORSOrigins() = %#v, want wildcard", origins)
		}
	})

	t.Run("trims configured origins", func(t *testing.T) {
		origins, err := resolveCORSOrigins(" https://kombify.io,https://stackkits.kombify.io , ", false, false)

		if err != nil {
			t.Fatalf("resolveCORSOrigins() unexpected error: %v", err)
		}
		want := []string{"https://kombify.io", "https://stackkits.kombify.io"}
		if len(origins) != len(want) {
			t.Fatalf("resolveCORSOrigins() len = %d, want %d: %#v", len(origins), len(want), origins)
		}
		for i := range want {
			if origins[i] != want[i] {
				t.Fatalf("resolveCORSOrigins()[%d] = %q, want %q", i, origins[i], want[i])
			}
		}
	})

	t.Run("rejects wildcard production profile from flag", func(t *testing.T) {
		origins, err := resolveCORSOrigins("", true, true)

		if err == nil {
			t.Fatal("resolveCORSOrigins() error = nil, want production guard error")
		}
		if origins != nil {
			t.Fatalf("resolveCORSOrigins() = %#v, want nil", origins)
		}
	})

	t.Run("rejects wildcard production profile from configured origins", func(t *testing.T) {
		origins, err := resolveCORSOrigins("https://kombify.io,*", false, true)

		if err == nil {
			t.Fatal("resolveCORSOrigins() error = nil, want production guard error")
		}
		if origins != nil {
			t.Fatalf("resolveCORSOrigins() = %#v, want nil", origins)
		}
	})
}

func TestResolveRuntimeProfile(t *testing.T) {
	t.Run("defaults to local", func(t *testing.T) {
		t.Setenv("STACKKITS_RUNTIME_PROFILE", "")
		t.Setenv("KOMBIFY_ENV", "")
		t.Setenv("APP_ENV", "")
		t.Setenv("ENVIRONMENT", "")
		t.Setenv("STACKKITS_PRODUCTION", "")

		if got := resolveRuntimeProfile(); got != "local" {
			t.Fatalf("resolveRuntimeProfile() = %q, want local", got)
		}
	})

	t.Run("maps public to production guards", func(t *testing.T) {
		t.Setenv("STACKKITS_RUNTIME_PROFILE", "public")

		if got := resolveRuntimeProfile(); got != "production" {
			t.Fatalf("resolveRuntimeProfile() = %q, want production", got)
		}
		if !runtimeProfileRequiresProductionGuards("production") {
			t.Fatal("runtimeProfileRequiresProductionGuards(production) = false, want true")
		}
	})
}

func TestEnvBool(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("STACKKITS_TEST_BOOL", value)
			if !envBool("STACKKITS_TEST_BOOL") {
				t.Fatalf("envBool(%q) = false, want true", value)
			}
		})
	}
}

func TestResolveTrustedProxies(t *testing.T) {
	t.Run("trims flag value", func(t *testing.T) {
		got := resolveTrustedProxies(" 10.0.0.1, 192.0.2.0/24, ")
		want := []string{"10.0.0.1", "192.0.2.0/24"}

		if len(got) != len(want) {
			t.Fatalf("resolveTrustedProxies() len = %d, want %d: %#v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("resolveTrustedProxies()[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("uses env value", func(t *testing.T) {
		t.Setenv("STACKKITS_TRUSTED_PROXIES", "10.0.0.2")

		got := resolveTrustedProxies("")

		if len(got) != 1 || got[0] != "10.0.0.2" {
			t.Fatalf("resolveTrustedProxies() = %#v, want env proxy", got)
		}
	})
}
