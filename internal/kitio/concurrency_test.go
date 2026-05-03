package kitio

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrency tests for the kit pipeline. The production hot paths are:
//
//   - Hash computation (CanonicalHash) — called per kit-import POST
//   - LocalRoundTrip — called per `stackkit kit roundtrip`
//   - ExportYAML — called per `stackkit kit export`
//
// All three should be safe to call from multiple goroutines on the SAME
// KitDefinition value (read-only). The tests below assert that property
// across the live kit corpus, not just synthetic data.
//
// What's NOT tested here: parallel kit-import POSTs against a real DB.
// That race is properly handled by Postgres (sk_stackkit.slug UNIQUE
// + transaction isolation in the kit-import endpoint). Testing that
// requires a live admin instance and is gated behind the live-roundtrip
// env vars elsewhere.

const concurrencyGoroutines = 50

// TestCanonicalHashIsThreadSafe runs CanonicalHash against the same
// KitDefinition from N goroutines and asserts every result equals the
// first. Catches non-determinism in the canonicalize() pipeline (e.g.
// shared mutable state, unsorted map iteration leaking).
func TestCanonicalHashIsThreadSafe(t *testing.T) {
	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			def, err := Import(readKitYAML(t, kit))
			if err != nil {
				t.Fatalf("import %s: %v", kit, err)
			}

			// Compute the reference hash once
			want, err := CanonicalHash(def)
			if err != nil {
				t.Fatalf("first hash: %v", err)
			}

			var (
				wg      sync.WaitGroup
				mismatches int32
			)
			wg.Add(concurrencyGoroutines)
			for i := 0; i < concurrencyGoroutines; i++ {
				go func(seq int) {
					defer wg.Done()
					got, err := CanonicalHash(def)
					if err != nil {
						t.Errorf("goroutine %d: hash error %v", seq, err)
						atomic.AddInt32(&mismatches, 1)
						return
					}
					if got != want {
						t.Errorf("goroutine %d: hash %s != reference %s", seq, got, want)
						atomic.AddInt32(&mismatches, 1)
					}
				}(i)
			}
			wg.Wait()
			if got := atomic.LoadInt32(&mismatches); got != 0 {
				t.Errorf("kit %s: %d/%d goroutines disagreed with reference hash",
					kit, got, concurrencyGoroutines)
			}
		})
	}
}

// TestExportYAMLIsThreadSafe asserts ExportYAML is safe for concurrent
// reads of the SAME KitDefinition. We compare each goroutine's output
// to a reference rendered serially up-front. They must all match
// byte-for-byte; otherwise yaml.v3 has shared state we can't trust.
func TestExportYAMLIsThreadSafe(t *testing.T) {
	def, err := Import(readKitYAML(t, "base-kit"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}

	var (
		wg          sync.WaitGroup
		differences int32
	)
	wg.Add(concurrencyGoroutines)
	for i := 0; i < concurrencyGoroutines; i++ {
		go func(seq int) {
			defer wg.Done()
			got, err := ExportYAML(def)
			if err != nil {
				t.Errorf("goroutine %d: export error %v", seq, err)
				atomic.AddInt32(&differences, 1)
				return
			}
			if string(got) != string(want) {
				t.Errorf("goroutine %d: export differed (%d vs %d bytes)",
					seq, len(got), len(want))
				atomic.AddInt32(&differences, 1)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&differences); got != 0 {
		t.Errorf("base-kit: %d/%d goroutines produced different yaml output",
			got, concurrencyGoroutines)
	}
}

// TestLocalRoundTripIsThreadSafe asserts the in-memory roundtrip
// (Import → ExportYAML → Import → Diff) is safe to run from N
// goroutines. Each goroutine starts from a fresh yaml byte-slice, so
// no shared state should leak between them. Mismatches indicate a
// concurrency bug in any of the four phases.
func TestLocalRoundTripIsThreadSafe(t *testing.T) {
	yamlBytes := readKitYAML(t, "base-kit")
	want, err := LocalRoundTrip(yamlBytes)
	if err != nil {
		t.Fatalf("first roundtrip: %v", err)
	}
	if !want.HashesEqual {
		t.Fatal("base-kit reference roundtrip is not lossless — fix that first")
	}

	var (
		wg      sync.WaitGroup
		errored int32
	)
	wg.Add(concurrencyGoroutines)
	for i := 0; i < concurrencyGoroutines; i++ {
		go func(seq int) {
			defer wg.Done()
			// Each goroutine gets its own copy of the bytes — yaml.Unmarshal
			// must NEVER hold onto the input slice
			localCopy := make([]byte, len(yamlBytes))
			copy(localCopy, yamlBytes)

			got, err := LocalRoundTrip(localCopy)
			if err != nil {
				t.Errorf("goroutine %d: roundtrip error %v", seq, err)
				atomic.AddInt32(&errored, 1)
				return
			}
			if got.OriginalHash != want.OriginalHash {
				t.Errorf("goroutine %d: original hash diverged %s vs %s",
					seq, got.OriginalHash, want.OriginalHash)
				atomic.AddInt32(&errored, 1)
			}
			if got.ReconstructedHash != want.ReconstructedHash {
				t.Errorf("goroutine %d: reconstructed hash diverged %s vs %s",
					seq, got.ReconstructedHash, want.ReconstructedHash)
				atomic.AddInt32(&errored, 1)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&errored); got != 0 {
		t.Errorf("base-kit: %d/%d roundtrip goroutines failed", got, concurrencyGoroutines)
	}
}
