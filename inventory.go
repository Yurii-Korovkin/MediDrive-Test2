package inventory

import (
	"errors"
	"sync"
)

var (
	// ErrProductNotFound is returned when the requested product does not
	// exist in the inventory.
	ErrProductNotFound = errors.New("product not found")

	// ErrInsufficientStock is returned when there is not enough stock to
	// satisfy a reservation.
	ErrInsufficientStock = errors.New("insufficient stock")
)

// Product represents a single stock-keeping unit.
type Product struct {
	ID    string
	Name  string
	Stock int
}

// ReserveItem describes one line item of a multi-product reservation
// request.
type ReserveItem struct {
	ProductID string
	Quantity  int
}

// SafeInventoryService is a thread-safe inventory manager.
//
// It uses a single sync.RWMutex to protect the products map and the Stock
// field of every Product it contains. Reads (GetStock) take an RLock, so
// any number of readers can proceed concurrently. Writes (Reserve,
// ReserveMultiple, AddProduct) take a full Lock, giving them exclusive
// access.
//
// Deliberately NOT used here: per-product (fine-grained) locking. It looks
// tempting for throughput, but ReserveMultiple needs to check and mutate
// several products as a single atomic unit, and per-product locks reintroduce
// the classic lock-ordering deadlock (see ANSWERS.md, Q2). A single
// coarse-grained lock keeps the invariants easy to reason about; if this
// service becomes a real bottleneck, sharding by product ID (with a fixed,
// deterministic shard order) is the next step, not naive per-product locks.
type SafeInventoryService struct {
	mu       sync.RWMutex
	products map[string]*Product
}

// NewSafeInventoryService creates an empty, ready-to-use service.
func NewSafeInventoryService() *SafeInventoryService {
	return &SafeInventoryService{
		products: make(map[string]*Product),
	}
}

// AddProduct registers a product (or overwrites an existing one with the
// same ID). It exists mainly to make tests and setup code straightforward;
// in a real service this would likely be backed by a database instead.
func (s *SafeInventoryService) AddProduct(p *Product) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.products[p.ID] = p
}

// GetStock returns the current stock level for a product, or 0 if the
// product does not exist. Uses RLock so it never blocks other readers.
func (s *SafeInventoryService) GetStock(productID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	product := s.products[productID]
	if product == nil {
		return 0
	}
	return product.Stock
}

// Reserve atomically checks and decrements stock for a single product.
// The check and the update happen inside the same critical section, so
// there is no gap in which another goroutine could interleave and cause an
// oversell.
func (s *SafeInventoryService) Reserve(productID string, quantity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	product := s.products[productID]
	if product == nil {
		return ErrProductNotFound
	}
	if product.Stock < quantity {
		return ErrInsufficientStock
	}

	product.Stock -= quantity
	return nil
}

// ReserveMultiple reserves several products as a single all-or-nothing
// operation. It holds the write lock for the entire duration of both the
// validation pass and the apply pass, so no other goroutine can observe or
// create an intermediate, partially-reserved state.
//
// If any item fails validation (missing product or insufficient stock), no
// changes are made to any product at all.
func (s *SafeInventoryService) ReserveMultiple(items []ReserveItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1: validate every item against the current state.
	for _, item := range items {
		product := s.products[item.ProductID]
		if product == nil {
			return ErrProductNotFound
		}
		if product.Stock < item.Quantity {
			return ErrInsufficientStock
		}
	}

	// Phase 2: apply every item. Safe to do without re-checking because we
	// have held the write lock continuously since phase 1 started - nothing
	// else could have modified the products in between.
	for _, item := range items {
		s.products[item.ProductID].Stock -= item.Quantity
	}

	return nil
}
