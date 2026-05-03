package backupcontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryQueue_BufferFullBlocksUntilContextCancelled is the
// backpressure guarantee. The buffered channel queue is a stand-in for
// NATS JetStream; the contract from server.go's perspective is "if the
// buffer is full, Publish blocks rather than drops". The follow-up
// NATS-backed implementation must preserve this — drops are how data
// loss happens in backup systems.
func TestMemoryQueue_BufferFullBlocksUntilContextCancelled(t *testing.T) {
	q := NewMemoryQueue(1)
	defer func() { _ = q.Close() }()

	ctx := context.Background()
	require.NoError(t, q.Publish(ctx, JobMessage{JobID: "j1"}))

	// Second publish should block; with a short deadline we must see
	// context.DeadlineExceeded rather than a successful "drop".
	pubCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	err := q.Publish(pubCtx, JobMessage{JobID: "j2"})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestMemoryQueue_CloseUnblocksPublishAndPull confirms Close() does
// not leak goroutines: any in-flight Publish/Pull returns promptly
// with ErrQueueClosed.
func TestMemoryQueue_CloseUnblocksPublishAndPull(t *testing.T) {
	q := NewMemoryQueue(0) // default 256 buffer
	ctx := context.Background()

	// Start a Pull on an empty queue — it must block.
	pullErr := make(chan error, 1)
	go func() {
		_, err := q.Pull(ctx)
		pullErr <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the goroutine reach the select
	require.NoError(t, q.Close())

	select {
	case err := <-pullErr:
		assert.True(t, errors.Is(err, ErrQueueClosed),
			"closed queue should release Pull with ErrQueueClosed, got %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Pull did not return after Close")
	}

	// Publish after Close must also fail.
	err := q.Publish(ctx, JobMessage{JobID: "x"})
	assert.True(t, errors.Is(err, ErrQueueClosed))
}

// TestMemoryQueue_IsIdempotentOnClose protects against double-close
// panics in graceful-shutdown paths where multiple goroutines all want
// to assert "I'm done".
func TestMemoryQueue_IsIdempotentOnClose(t *testing.T) {
	q := NewMemoryQueue(1)
	require.NoError(t, q.Close())
	// Must not panic.
	require.NoError(t, q.Close())
}
