package cue

import "testing"

func TestContractHashDeterministic(t *testing.T) {
	a := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":    "immich",
			"version": "1.0.0",
			"core":    true,
		},
		"requires": map[string]interface{}{
			"services": map[string]interface{}{
				"postgres": map[string]interface{}{
					"minVersion": "17",
					"optional":   false,
				},
			},
		},
	}
	b := map[string]interface{}{
		"requires": map[string]interface{}{
			"services": map[string]interface{}{
				"postgres": map[string]interface{}{
					"optional":   false,
					"minVersion": "17",
				},
			},
		},
		"metadata": map[string]interface{}{
			"core":    true,
			"version": "1.0.0",
			"name":    "immich",
		},
	}
	ha, err := ContractHash(a)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, err := ContractHash(b)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if ha != hb {
		t.Fatalf("hashes differ: a=%s b=%s", ha, hb)
	}
	if len(ha) != 64 {
		t.Fatalf("expected 64-char hex, got %d", len(ha))
	}
}

func TestContractHashChangesOnValueChange(t *testing.T) {
	a := map[string]interface{}{"name": "foo", "n": 1}
	b := map[string]interface{}{"name": "foo", "n": 2}
	ha, _ := ContractHash(a)
	hb, _ := ContractHash(b)
	if ha == hb {
		t.Fatalf("expected different hashes")
	}
}
