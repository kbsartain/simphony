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

func TestSlotLimiterTryAcquireForHonorsWaitingOrder(t *testing.T) {
	limiter, err := NewSlotLimiter(1)
	if err != nil {
		t.Fatalf("NewSlotLimiter returned error: %v", err)
	}

	if !limiter.TryAcquireFor("alpha") {
		t.Fatal("alpha initial acquisition failed")
	}
	if limiter.TryAcquireFor("beta") {
		t.Fatal("beta acquired while limiter was full")
	}
	if limiter.TryAcquireFor("gamma") {
		t.Fatal("gamma acquired while limiter was full")
	}

	limiter.Release()
	if limiter.TryAcquireFor("gamma") {
		t.Fatal("gamma bypassed earlier waiting beta")
	}
	if !limiter.TryAcquireFor("beta") {
		t.Fatal("beta did not receive first released slot")
	}

	limiter.Release()
	if !limiter.TryAcquireFor("gamma") {
		t.Fatal("gamma did not receive next released slot")
	}
}

func TestSlotLimiterForgetOwnerRemovesStaleWaiter(t *testing.T) {
	limiter, err := NewSlotLimiter(1)
	if err != nil {
		t.Fatalf("NewSlotLimiter returned error: %v", err)
	}

	if !limiter.TryAcquireFor("alpha") {
		t.Fatal("alpha initial acquisition failed")
	}
	if limiter.TryAcquireFor("beta") || limiter.TryAcquireFor("gamma") {
		t.Fatal("waiting owner acquired while limiter was full")
	}

	limiter.ForgetOwner("beta")
	limiter.Release()
	if !limiter.TryAcquireFor("gamma") {
		t.Fatal("gamma did not acquire after stale beta waiter was removed")
	}
}

func TestSlotLimiterUnownedAcquireDoesNotBypassWaitingProject(t *testing.T) {
	limiter, err := NewSlotLimiter(1)
	if err != nil {
		t.Fatalf("NewSlotLimiter returned error: %v", err)
	}

	if !limiter.TryAcquireFor("alpha") {
		t.Fatal("alpha initial acquisition failed")
	}
	if limiter.TryAcquireFor("beta") {
		t.Fatal("beta acquired while limiter was full")
	}

	limiter.Release()
	if limiter.TryAcquire() {
		t.Fatal("unowned acquisition bypassed waiting beta")
	}
	if !limiter.TryAcquireFor("beta") {
		t.Fatal("beta did not receive released slot")
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
