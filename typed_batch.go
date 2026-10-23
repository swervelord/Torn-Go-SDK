package torn

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Batch combines any number of typed selections of one resource and one shared
// target/filter set. AddSelection statically rejects another resource's tokens.
type Batch[R any] struct {
	mu         sync.RWMutex
	send       chan struct{}
	resource   string
	names      []string
	generation uint64
	executed   bool
	data       json.RawMessage
	err        error
}

// Slot gives a selection's concrete response after its batch executes.
type Slot[T any] struct {
	read func() (json.RawMessage, error)
}

// NewBatch starts a resource-safe batch and returns the first typed result slot.
func NewBatch[R, T any](first Selection[R, T]) (*Batch[R], *Slot[T]) {
	b := &Batch[R]{resource: first.resource, send: make(chan struct{}, 1)}
	return b, AddSelection(b, first)
}

// AddSelection adds a token and invalidates any previous execution result.
func AddSelection[R, T any](batch *Batch[R], selection Selection[R, T]) *Slot[T] {
	if batch == nil {
		return &Slot[T]{}
	}
	batch.mu.Lock()
	batch.names = append(batch.names, selection.name)
	batch.generation++
	batch.executed = false
	batch.data = nil
	batch.mu.Unlock()
	return &Slot[T]{read: func() (json.RawMessage, error) {
		batch.mu.RLock()
		defer batch.mu.RUnlock()
		if !batch.executed {
			return nil, errors.New("batch has not executed after its latest selection change")
		}
		if batch.err != nil {
			return nil, batch.err
		}
		return append(json.RawMessage(nil), batch.data...), nil
	}}
}

// Execute plans and sends the batch. Calls on the same batch are serialized;
// adding selections during execution requires executing the modified batch again.
func (b *Batch[R]) Execute(ctx context.Context, c *Client, params map[string]any, opts RequestOptions) (ExecutionReport, error) {
	if b == nil || c == nil {
		return ExecutionReport{}, errors.New("nil batch or client")
	}
	if ctx == nil {
		return ExecutionReport{}, errors.New("nil context")
	}
	if b.send == nil {
		return ExecutionReport{}, errors.New("batch must be constructed with NewBatch")
	}
	select {
	case b.send <- struct{}{}:
	case <-ctx.Done():
		return ExecutionReport{}, ctx.Err()
	}
	defer func() { <-b.send }()
	b.mu.RLock()
	names := append([]string(nil), b.names...)
	resource := b.resource
	generation := b.generation
	b.mu.RUnlock()
	result, err := c.RawSelections(ctx, resource, names, params, opts)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.generation != generation {
		return result.Report, errors.New("batch selections changed during execution; execute the updated batch again")
	}
	b.executed = true
	b.err = err
	b.data = append(b.data[:0], result.Data...)
	return result.Report, err
}

// Value decodes this selection's concrete type without an unsafe assertion.
func (s *Slot[T]) Value() (T, error) {
	var zero T
	if s == nil || s.read == nil {
		return zero, errors.New("invalid batch result slot")
	}
	raw, err := s.read()
	if err != nil {
		return zero, err
	}
	return Decode[T](raw)
}
