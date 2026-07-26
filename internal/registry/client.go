package registry

import (
	"context"
	"fmt"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// Client abstracts the public, embedded registry read path.
type Client interface {
	Source() string
	Snapshot(ctx context.Context) (Snapshot, error)

	// Tool / Module / StackKit return the matching entry or
	// ErrNotFound when the slug is unknown to the registry.
	Tool(ctx context.Context, slug string) (Tool, error)
	Module(ctx context.Context, slug string) (Module, error)
	StackKit(ctx context.Context, slug string) (StackKit, error)
}

// ErrNotFound signals an unknown slug. Callers use errors.Is.
var ErrNotFound = fmt.Errorf("registry: not found")

const retiredHAKitRegistrySlug = stackspecmigration.LegacyHAKitSlug

// activeProductSnapshot removes legacy migration-only rows at the client
// boundary. The Admin DB may retain such a row during its controlled cleanup
// window, but CLI discovery and generated product views must never expose it.
func activeProductSnapshot(snap Snapshot) Snapshot {
	active := make([]StackKit, 0, len(snap.StackKits))
	for _, stackkit := range snap.StackKits {
		if !productkits.IsActive(stackkit.Slug) {
			continue
		}
		active = append(active, stackkit)
	}
	snap.StackKits = active
	return snap
}

func isActiveProductKitSlug(slug string) bool {
	return productkits.IsActive(slug)
}

// EmbeddedClient serves the baked-in snapshot. It is the OSS default and
// only registry backend linked into public builds.
type EmbeddedClient struct {
	snap Snapshot
	err  error
}

// NewEmbeddedClient loads the embedded snapshot eagerly so that
// downstream calls are trivial. Decode errors are captured and returned
// from every method so the caller sees them as soon as they use the
// client.
func NewEmbeddedClient() *EmbeddedClient {
	snap, err := EmbeddedSnapshot()
	return &EmbeddedClient{snap: snap, err: err}
}

// Source returns "embedded".
func (c *EmbeddedClient) Source() string { return "embedded" }

// Snapshot returns a copy of the embedded snapshot.
func (c *EmbeddedClient) Snapshot(_ context.Context) (Snapshot, error) {
	if c.err != nil {
		return Snapshot{}, c.err
	}
	return c.snap, nil
}

// Tool looks up a tool by slug.
func (c *EmbeddedClient) Tool(_ context.Context, slug string) (Tool, error) {
	if c.err != nil {
		return Tool{}, c.err
	}
	for _, t := range c.snap.Tools {
		if t.Slug == slug {
			return t, nil
		}
	}
	return Tool{}, fmt.Errorf("%w: tool %q", ErrNotFound, slug)
}

// Module looks up a module by slug.
func (c *EmbeddedClient) Module(_ context.Context, slug string) (Module, error) {
	if c.err != nil {
		return Module{}, c.err
	}
	for _, m := range c.snap.Modules {
		if m.Slug == slug {
			return m, nil
		}
	}
	return Module{}, fmt.Errorf("%w: module %q", ErrNotFound, slug)
}

// StackKit looks up a stackkit by slug.
func (c *EmbeddedClient) StackKit(_ context.Context, slug string) (StackKit, error) {
	if c.err != nil {
		return StackKit{}, c.err
	}
	if !isActiveProductKitSlug(slug) {
		return StackKit{}, fmt.Errorf("%w: stackkit %q is not an active product", ErrNotFound, slug)
	}
	for _, s := range c.snap.StackKits {
		if s.Slug == slug {
			return s, nil
		}
	}
	return StackKit{}, fmt.Errorf("%w: stackkit %q", ErrNotFound, slug)
}
