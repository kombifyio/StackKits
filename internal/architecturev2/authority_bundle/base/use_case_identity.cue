// Package base -- shared Use Case identity.
package base

// #UseCaseSlug is deliberately open. The canonical UseCaseCatalog owns the
// registered product intents; this type only gives every cross-contract join a
// stable, DNS-safe identity.
#UseCaseSlug: =~"^[a-z][a-z0-9-]+$"
