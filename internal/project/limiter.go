package project

import (
	"fmt"
	"strings"
	"sync"
)

// SlotLimiter is a non-blocking shared concurrency limiter.
type SlotLimiter struct {
	mu       sync.Mutex
	capacity int
	used     int
	queue    []string
	queued   map[string]struct{}
}

// NewSlotLimiter creates a limiter with a fixed positive capacity.
func NewSlotLimiter(limit int) (*SlotLimiter, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("slot limit must be positive, got %d", limit)
	}
	return &SlotLimiter{capacity: limit, queued: make(map[string]struct{})}, nil
}

// TryAcquire reserves one slot when capacity is available.
func (l *SlotLimiter) TryAcquire() bool {
	if l == nil {
		return true
	}
	return l.tryAcquire("")
}

// TryAcquireFor reserves one slot for an identified project when capacity is available.
// When multiple projects are waiting, released slots are offered in first-denied order.
func (l *SlotLimiter) TryAcquireFor(owner string) bool {
	if l == nil {
		return true
	}
	owner = strings.TrimSpace(owner)
	return l.tryAcquire(owner)
}

func (l *SlotLimiter) tryAcquire(owner string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if owner == "" {
		if l.used < l.capacity && len(l.queue) == 0 {
			l.used++
			return true
		}
		return false
	}

	if len(l.queue) > 0 {
		if l.queue[0] == owner && l.used < l.capacity {
			l.popQueuedOwner()
			l.used++
			return true
		}
		l.enqueueOwner(owner)
		return false
	}

	if l.used < l.capacity {
		l.used++
		return true
	}

	l.enqueueOwner(owner)
	return false
}

// Release returns one slot to the limiter.
func (l *SlotLimiter) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used > 0 {
		l.used--
	}
}

// ForgetOwner removes a project from the waiting queue.
func (l *SlotLimiter) ForgetOwner(owner string) {
	if l == nil {
		return
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.queued[owner]; !ok {
		return
	}
	delete(l.queued, owner)
	filtered := l.queue[:0]
	for _, queuedOwner := range l.queue {
		if queuedOwner != owner {
			filtered = append(filtered, queuedOwner)
		}
	}
	l.queue = filtered
}

// Used returns the number of currently reserved slots.
func (l *SlotLimiter) Used() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// Capacity returns the configured slot capacity.
func (l *SlotLimiter) Capacity() int {
	if l == nil {
		return 0
	}
	return l.capacity
}

func (l *SlotLimiter) enqueueOwner(owner string) {
	if _, ok := l.queued[owner]; ok {
		return
	}
	l.queued[owner] = struct{}{}
	l.queue = append(l.queue, owner)
}

func (l *SlotLimiter) popQueuedOwner() {
	if len(l.queue) == 0 {
		return
	}
	owner := l.queue[0]
	delete(l.queued, owner)
	copy(l.queue, l.queue[1:])
	l.queue = l.queue[:len(l.queue)-1]
}
