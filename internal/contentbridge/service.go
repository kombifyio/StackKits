package contentbridge

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// failureTTL is how long an unreachable or rejected app answer is remembered.
// It spares a down app one slow timeout per request without ever serving a
// stale count: only the status is remembered.
const failureTTL = 5 * time.Second

// Adapter answers one use case from one app.
type Adapter interface {
	UseCase() UseCase
	// Ceiling is the highest tier this adapter can answer today.
	Ceiling() Tier
	// Counts returns the summary tier counts, or one of the outcome errors.
	Counts(ctx context.Context) (any, error)
}

// ServiceConfig configures a Service.
type ServiceConfig struct {
	// HomelabID is answered when the caller supplies none.
	HomelabID string
	Adapters  []Adapter
	// CacheTTL is clamped to MaxCacheTTL; zero selects MaxCacheTTL.
	CacheTTL time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// Service assembles homelab-content/v1 documents.
type Service struct {
	homelabID string
	adapters  map[UseCase]Adapter
	ttl       time.Duration
	now       func() time.Time
	logger    *slog.Logger

	mu    sync.Mutex
	cache map[UseCase]cached
}

type cached struct {
	counts any
	status Status
	at     time.Time
}

// NewService builds a Service.
func NewService(cfg ServiceConfig) *Service {
	ttl := cfg.CacheTTL
	if ttl <= 0 || ttl > MaxCacheTTL {
		ttl = MaxCacheTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	adapters := make(map[UseCase]Adapter, len(cfg.Adapters))
	for _, adapter := range cfg.Adapters {
		adapters[adapter.UseCase()] = adapter
	}
	return &Service{
		homelabID: cfg.HomelabID, adapters: adapters, ttl: ttl, now: now, logger: logger,
		cache: map[UseCase]cached{},
	}
}

// HomelabID is the identifier answered when the caller supplies none.
func (s *Service) HomelabID() string { return s.homelabID }

// Summarize answers the requested use cases at the granted tier. The
// effective tier of each use case is the minimum of the granted tier and the
// adapter's ceiling. At the off tier nothing is read.
func (s *Service) Summarize(ctx context.Context, useCases []UseCase, granted Tier, homelabID string) Document {
	document := Document{Version: Version, HomelabID: homelabID, AsOf: formatTime(s.now())}
	for _, useCase := range useCases {
		document.UseCases = append(document.UseCases, s.answer(ctx, useCase, granted))
	}
	return document
}

func (s *Service) answer(ctx context.Context, useCase UseCase, granted Tier) UseCaseResult {
	started := s.now()
	result := UseCaseResult{UseCase: useCase, App: useCaseApps[useCase]}
	adapter, ok := s.adapters[useCase]
	switch {
	case !ok:
		result.Status, result.Tier = StatusNotConfigured, TierOff
	case granted == TierOff:
		result.Status, result.Tier = StatusConsentRequired, TierOff
	default:
		result.Tier = MinTier(granted, adapter.Ceiling())
		counts, status, at := s.read(ctx, adapter)
		result.Status = status
		if status == StatusOK {
			result.Counts, result.AsOf = counts, formatTime(at)
		}
	}
	// Only the use case, the outcome and the latency are logged: never a
	// count, an identifier, a credential or anything the app answered.
	s.logger.Info("content bridge answer",
		"use_case", string(useCase), "status", string(result.Status), "tier", string(result.Tier),
		"latency_ms", s.now().Sub(started).Milliseconds())
	return result
}

// read returns the adapter's counts from memory when they are younger than
// the cache lifetime, and otherwise asks the app. The lock is held across the
// app call on purpose: concurrent requests wait for one read and then share
// its answer instead of each asking the app.
func (s *Service) read(ctx context.Context, adapter Adapter) (any, Status, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	useCase := adapter.UseCase()
	now := s.now()
	if entry, ok := s.cache[useCase]; ok {
		lifetime := s.ttl
		if entry.status != StatusOK {
			lifetime = failureTTL
		}
		if age := now.Sub(entry.at); age >= 0 && age < lifetime {
			return entry.counts, entry.status, entry.at
		}
	}
	counts, err := adapter.Counts(ctx)
	if err != nil && ctx.Err() != nil {
		// The caller gave up. That says nothing about the app, so it is not
		// remembered for the next caller.
		return nil, StatusAppUnreachable, now
	}
	status := statusOf(err)
	s.cache[useCase] = cached{counts: counts, status: status, at: now}
	return counts, status, now
}

func statusOf(err error) Status {
	switch {
	case err == nil:
		return StatusOK
	case errors.Is(err, ErrCredentialInvalid):
		return StatusCredentialInvalid
	case errors.Is(err, ErrNotConfigured):
		return StatusNotConfigured
	default:
		return StatusAppUnreachable
	}
}
