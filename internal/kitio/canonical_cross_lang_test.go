package kitio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ExpectedFixtureHash is the SHA256 of the canonical-JSON encoding of
// testdata/canonical_hash_fixture.json. It is hardcoded here as the cross-
// language anchor: Go must produce this hash, AND the TS canonicalize()
// in kombify-Administration's kit-import endpoint must produce the same
// hash on the same input.
//
// If you change the fixture file or canonicalize() algorithm, you MUST
// update this constant — but ONLY after running both the Go test below
// AND `npm test cross_lang_hash` in kombify-Administration to confirm
// they agree.
//
// The TS-side test is at:
//
//	kombify-Administration/frontend/src/routes/api/v1/sk/registry/stackkits/[id]/kit-import/canonical_hash.test.ts
//
// Updated 2026-04-27 after migration 000084 (useCases → application rename).
// Previous anchor (pre-rename) was:
//
//	b1e5815355939e0da5d568298467a4782a2fe2074a130493704cbf34f9628155
const ExpectedFixtureHash = "e0a2b8779d83ec126d24fbf9c9401ec28f1fe5e2acdecd73270ac761a4c624c8"

// TestCanonicalHashFixtureMatchesAnchor recomputes the hash of the fixture
// using the production canonicalizer (CanonicalHash via Import → marshal).
// Must match ExpectedFixtureHash byte-for-byte. If this drifts, either:
//
//	(a) Someone changed the canonicalizer — they must re-run
//	    UPDATE_FIXTURE_HASH=1 and update the TS-side test in lockstep.
//	(b) Someone changed the fixture — same procedure.
//
// Set UPDATE_FIXTURE_HASH=1 to print the new hash and skip the assertion.
func TestCanonicalHashFixtureMatchesAnchor(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	fixturePath := filepath.Join(filepath.Dir(file), "testdata", "canonical_hash_fixture.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Decode the fixture into a generic map (mirror what Import does for
	// arbitrary content), then run the same canonicalize/sha256 pipeline.
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	canonical, err := canonicalJSON(generic)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	hash := sha256Hex(canonical)

	if os.Getenv("UPDATE_FIXTURE_HASH") == "1" {
		t.Logf("UPDATE_FIXTURE_HASH=1 — anchor hash should be set to:\n%s", hash)
		t.Logf("After updating ExpectedFixtureHash, also update:\n  kombify-Administration/frontend/src/routes/api/v1/sk/registry/stackkits/[id]/kit-import/canonical_hash.test.ts")
		return
	}

	if hash != ExpectedFixtureHash {
		t.Errorf(`Canonical hash drift detected.

Go produced:  %s
Anchor:       %s

If this change is intentional:
  1. UPDATE_FIXTURE_HASH=1 go test ./internal/kitio -run TestCanonicalHashFixture
  2. Update ExpectedFixtureHash with the printed value
  3. ALSO update the TS-side anchor in kombify-Administration:
       frontend/src/routes/api/v1/sk/registry/stackkits/[id]/kit-import/canonical_hash.test.ts
  4. Run both tests to confirm Go ≡ TS

If this change is NOT intentional:
  Someone modified the canonicalizer or the fixture without updating
  the cross-language anchor. Investigate before merging.`, hash, ExpectedFixtureHash)
	}
}

// sha256Hex is a small wrapper to keep this test readable. The production
// canonicalizer is in import.go (canonicalJSON) and ContractHash uses
// SHA256 over its output — same algorithm here.
func sha256Hex(b []byte) string {
	return hashSHA256(b)
}
