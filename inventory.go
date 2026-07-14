package inventory

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestGetStock_Basic(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "p1", Name: "Widget", Stock: 50})

	if got := s.GetStock("p1"); got != 50 {
		t.Fatalf("expected 50, got %d", got)
	}
	if got := s.GetStock("missing"); got != 0 {
		t.Fatalf("expected 0 for missing product, got %d", got)
	}
}

func TestReserve_Basic(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "p1", Stock: 10})

	if err := s.Reserve("p1", 4); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.GetStock("p1"); got != 6 {
		t.Fatalf("expected 6, got %d", got)
	}

	if err := s.Reserve("p1", 100); err != ErrInsufficientStock {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	if err := s.Reserve("unknown", 1); err != ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

// TestReserve_ConcurrentOversell is the core regression test for race
// condition #2 in REVIEW.md. Stock starts at 100 and 200 goroutines each
// try to reserve exactly 1 unit concurrently.
//
// With a broken (check-then-act, or unlocked) implementation, more than 100
// reservations can succeed - an oversell. With correct atomic
// check-and-reserve under a single write lock, exactly 100 succeed and 100
// fail, every single time this test runs.
//
// Run with: go test -race -run TestReserve_ConcurrentOversell -count=20
func TestReserve_ConcurrentOversell(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "p1", Stock: 100})

	const goroutines = 200
	var wg sync.WaitGroup
	var successCount int64
	var failCount int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := s.Reserve("p1", 1); err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}()
	}
	wg.Wait()

	if successCount != 100 {
		t.Errorf("expected exactly 100 successful reservations, got %d", successCount)
	}
	if failCount != 100 {
		t.Errorf("expected exactly 100 failed reservations, got %d", failCount)
	}
	if finalStock := s.GetStock("p1"); finalStock != 0 {
		t.Errorf("expected final stock 0, got %d (evidence of oversell/lost update)", finalStock)
	}
}

// TestReserveMultiple_Atomicity verifies the all-or-nothing contract for
// race condition #3: a batch that partially fails must leave every product
// in the batch completely unchanged.
func TestReserveMultiple_Atomicity(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "A", Stock: 10})
	s.AddProduct(&Product{ID: "B", Stock: 5})

	// B only has 5 units, so this batch must fail entirely.
	err := s.ReserveMultiple([]ReserveItem{
		{ProductID: "A", Quantity: 8},
		{ProductID: "B", Quantity: 8},
	})
	if err != ErrInsufficientStock {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	if got := s.GetStock("A"); got != 10 {
		t.Errorf("expected product A to remain unchanged at 10, got %d", got)
	}
	if got := s.GetStock("B"); got != 5 {
		t.Errorf("expected product B to remain unchanged at 5, got %d", got)
	}

	// A batch that should succeed fully.
	err = s.ReserveMultiple([]ReserveItem{
		{ProductID: "A", Quantity: 3},
		{ProductID: "B", Quantity: 2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.GetStock("A"); got != 7 {
		t.Errorf("expected product A to be 7, got %d", got)
	}
	if got := s.GetStock("B"); got != 3 {
		t.Errorf("expected product B to be 3, got %d", got)
	}
}

// TestReserveMultiple_ConcurrentAtomicity hammers ReserveMultiple from many
// goroutines at once, to prove the all-or-nothing guarantee also holds
// under real contention, not just in a single-threaded call.
func TestReserveMultiple_ConcurrentAtomicity(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "A", Stock: 50})
	s.AddProduct(&Product{ID: "B", Stock: 50})

	const goroutines = 100
	var wg sync.WaitGroup
	var successCount int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := s.ReserveMultiple([]ReserveItem{
				{ProductID: "A", Quantity: 1},
				{ProductID: "B", Quantity: 1},
			})
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	if successCount != 50 {
		t.Errorf("expected exactly 50 successful batch reservations, got %d", successCount)
	}

	// A and B must have moved in lockstep - never partially reserved.
	wantRemaining := int(50 - successCount)
	if got := s.GetStock("A"); got != wantRemaining {
		t.Errorf("product A stock mismatch: got %d, want %d", got, wantRemaining)
	}
	if got := s.GetStock("B"); got != wantRemaining {
		t.Errorf("product B stock mismatch: got %d, want %d", got, wantRemaining)
	}
}

// TestGetStock_ConcurrentWithReserve exercises readers and writers running
// at the same time. It doesn't assert exact numbers (the interleaving is
// non-deterministic by nature) - its purpose is to be run under -race to
// prove RWMutex usage doesn't allow a data race between GetStock and
// Reserve.
func TestGetStock_ConcurrentWithReserve(t *testing.T) {
	s := NewSafeInventoryService()
	s.AddProduct(&Product{ID: "p1", Stock: 1000})

	var wg sync.WaitGroup

	// Writers
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			_ = s.Reserve("p1", 1)
		}()
	}

	// Readers, running concurrently with the writers above.
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer wg.Done()
			_ = s.GetStock("p1")
		}()
	}

	wg.Wait()

	if got := s.GetStock("p1"); got != 900 {
		t.Errorf("expected 900 remaining, got %d", got)
	}
}
