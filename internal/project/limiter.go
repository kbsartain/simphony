package project

import "fmt"

// SlotLimiter is a non-blocking shared concurrency limiter.
type SlotLimiter struct {
	ch chan struct{}
}

// NewSlotLimiter creates a limiter with a fixed positive capacity.
func NewSlotLimiter(limit int) (*SlotLimiter, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("slot limit must be positive, got %d", limit)
	}
	return &SlotLimiter{ch: make(chan struct{}, limit)}, nil
}

// TryAcquire reserves one slot when capacity is available.
func (l *SlotLimiter) TryAcquire() bool {
	if l == nil {
		return true
	}
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns one slot to the limiter.
func (l *SlotLimiter) Release() {
	if l == nil {
		return
	}
	select {
	case <-l.ch:
	default:
	}
}

// Used returns the number of currently reserved slots.
func (l *SlotLimiter) Used() int {
	if l == nil {
		return 0
	}
	return len(l.ch)
}

// Capacity returns the configured slot capacity.
func (l *SlotLimiter) Capacity() int {
	if l == nil {
		return 0
	}
	return cap(l.ch)
}
