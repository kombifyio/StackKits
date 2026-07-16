package architecturev2

// The embedded authority is a generated, drift-tested projection of the CUE
// source of truth. It must never be edited by hand.
//
//go:generate go run ./cmd/bundlegen -repo ../.. -out authority_bundle -distribution-fingerprint-out ../resolvedplan/product_distribution_fingerprint_generated.go
//go:generate go run ./cmd/bundlegen -repo ../.. -out contract_fixture_bundle -contract-fixture
