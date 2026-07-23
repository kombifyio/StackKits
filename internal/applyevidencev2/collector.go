package applyevidence

import "context"

// Collector is the provider-neutral producer SPI for one exact collection
// request. Implementations privately own observation, enrollment, signing,
// endpoint, credential, and transport behavior. The consumer validates and
// bounds the returned canonical evidence bundle.
type Collector interface {
	CollectApplyEvidence(context.Context, CollectionRequest) ([]byte, error)
}
