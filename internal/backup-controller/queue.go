package backupcontroller

import (
	"context"
	"sync"
	"time"
)

// JobMessage is the unit of work the scheduler pushes onto the queue
// and the agent pulls. It is intentionally small: the agent already
// holds enough state (its own host record, the addon's local config)
// to execute, so the message only needs to identify the Job and carry
// a deadline.
type JobMessage struct {
	JobID    string    `json:"job_id"`
	HostID   string    `json:"host_id"`
	Deadline time.Time `json:"deadline"`
}

// JobQueue is the dispatcher abstraction. Phase 4 will plug in NATS
// JetStream behind this interface; today it ships with an in-memory
// channel-based implementation that is sufficient for tests and for a
// single-process controller deployment.
//
// Publish must return only after the message is durable (in the
// memory implementation that is "slot occupied"; in a NATS impl that
// is "ack received"). Pull blocks until either a message is available
// or the context is canceled.
type JobQueue interface {
	Publish(ctx context.Context, msg JobMessage) error
	Pull(ctx context.Context) (JobMessage, error)
	Close() error
}

// NewMemoryQueue returns a buffered, channel-backed queue. The buffer
// size caps how many in-flight messages can sit between scheduler and
// agents — set generously; backpressure is preferable to drops.
func NewMemoryQueue(buffer int) JobQueue {
	if buffer <= 0 {
		buffer = 256
	}
	return &memoryQueue{
		ch:     make(chan JobMessage, buffer),
		closed: make(chan struct{}),
	}
}

type memoryQueue struct {
	ch     chan JobMessage
	closed chan struct{}
	once   sync.Once
}

func (q *memoryQueue) Publish(ctx context.Context, msg JobMessage) error {
	// Fast-path the closed case: a buffered channel with free space
	// would otherwise win the select race against q.closed and let
	// the message through after Close(). Backup systems must not
	// silently accept work they cannot deliver, so a closed queue
	// always rejects.
	select {
	case <-q.closed:
		return ErrQueueClosed
	default:
	}
	select {
	case q.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closed:
		return ErrQueueClosed
	}
}

func (q *memoryQueue) Pull(ctx context.Context) (JobMessage, error) {
	select {
	case msg := <-q.ch:
		return msg, nil
	case <-ctx.Done():
		return JobMessage{}, ctx.Err()
	case <-q.closed:
		return JobMessage{}, ErrQueueClosed
	}
}

func (q *memoryQueue) Close() error {
	q.once.Do(func() { close(q.closed) })
	return nil
}

// ErrQueueClosed is returned by Publish/Pull after Close has been called.
var ErrQueueClosed = errQueueClosed{}

type errQueueClosed struct{}

func (errQueueClosed) Error() string { return "backup-controller: queue closed" }
