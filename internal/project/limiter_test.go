package project

import "testing"

func TestSlotLimiterTryAcquireAndRelease(t *testing.T) {
	limiter, err := NewSlotLimiter(2)
	if err != nil {
		t.Fatalf("NewSlotLimiter returned error: %v", err)
	}

	if limiter.Capacity() != 2 {
		t.Fatalf("Capacity = %d, want 2", limiter.Capacity())
	}
	if !limiter.TryAcquire() || !limiter.TryAcquire() {
		t.Fatal("expected first two acquisitions to succeed")
	}
	if limiter.Used() != 2 {
		t.Fatalf("Used = %d, want 2", limiter.Used())
	}
	if limiter.TryAcquire() {
		t.Fatal("third acquisition succeeded, want full limiter")
	}

	limiter.Release()
	if limiter.Used() != 1 {
		t.Fatalf("Used after release = %d, want 1", limiter.Used())
	}
	if !limiter.TryAcquire() {
		t.Fatal("acquisition after release failed")
	}
}

func TestSlotLimiterOverReleaseIsNoop(t *testing.T) {
	limiter, err := NewSlotLimiter(1)
	if err != nil {
		t.Fatalf("NewSlotLimiter returned error: %v", err)
	}

	limiter.Release()
	if limiter.Used() != 0 {
		t.Fatalf("Used after empty release = %d, want 0", limiter.Used())
	}
	if !limiter.TryAcquire() {
		t.Fatal("acquisition after empty release failed")
	}
	limiter.Release()
	limiter.Release()
	if limiter.Used() != 0 {
		t.Fatalf("Used after repeated release = %d, want 0", limiter.Used())
	}
}

func TestNilSlotLimiterIsPermissive(t *testing.T) {
	var limiter *SlotLimiter
	if !limiter.TryAcquire() {
		t.Fatal("nil limiter acquisition failed, want permissive")
	}
	limiter.Release()
	if limiter.Used() != 0 || limiter.Capacity() != 0 {
		t.Fatalf("nil limiter used/capacity = %d/%d, want 0/0", limiter.Used(), limiter.Capacity())
	}
}

func TestSlotLimiterRejectsInvalidLimit(t *testing.T) {
	if _, err := NewSlotLimiter(0); err == nil {
		t.Fatal("NewSlotLimiter(0) returned nil error")
	}
}
