package main

import "testing"

func TestResolveAPIKey(t *testing.T) {
	t.Run("requires key by default", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")
		t.Setenv("STACKKITS_ALLOW_UNAUTHENTICATED", "")

		key, err := resolveAPIKey("", false)

		if err == nil {
			t.Fatal("resolveAPIKey() error = nil, want required-key error")
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
		}
	})

	t.Run("uses flag value", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "env-key")

		key, err := resolveAPIKey("flag-key", false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "flag-key" {
			t.Fatalf("resolveAPIKey() key = %q, want flag-key", key)
		}
	})

	t.Run("uses env value", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "env-key")

		key, err := resolveAPIKey("", false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "env-key" {
			t.Fatalf("resolveAPIKey() key = %q, want env-key", key)
		}
	})

	t.Run("allows explicit unauthenticated local mode", func(t *testing.T) {
		t.Setenv("STACKKITS_API_KEY", "")

		key, err := resolveAPIKey("", true)

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

		key, err := resolveAPIKey("", false)

		if err != nil {
			t.Fatalf("resolveAPIKey() unexpected error: %v", err)
		}
		if key != "" {
			t.Fatalf("resolveAPIKey() key = %q, want empty", key)
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
