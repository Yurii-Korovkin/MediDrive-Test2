// Command demo shows SafeInventoryService in action: basic operations,
// a concurrent stress test that proves no oversell happens, and an
// all-or-nothing ReserveMultiple example.
//
// Run it with:
//
//	go run ./cmd/demo
package main

import (
	"fmt"
	"sync"
	"sync/atomic"

	inventory "TaskTest_FOR_MediDrive"
)

func main() {
	fmt.Println("=== 1. Базові операції ===")
	basicDemo()

	fmt.Println("\n=== 2. Конкурентний стрес-тест: 100 одиниць, 200 горутин ===")
	concurrentOversellDemo()

	fmt.Println("\n=== 3. ReserveMultiple: all-or-nothing ===")
	reserveMultipleDemo()

	fmt.Println("\n=== 4. Конкурентний ReserveMultiple: атомарність під навантаженням ===")
	concurrentReserveMultipleDemo()
}

func basicDemo() {
	s := inventory.NewSafeInventoryService()
	s.AddProduct(&inventory.Product{ID: "sku-1", Name: "Дрон DJI Mini", Stock: 10})

	fmt.Printf("Початковий залишок sku-1: %d\n", s.GetStock("sku-1"))

	if err := s.Reserve("sku-1", 3); err != nil {
		fmt.Println("Помилка резервування:", err)
	} else {
		fmt.Println("Зарезервовано 3 одиниці sku-1")
	}
	fmt.Printf("Залишок після резервування: %d\n", s.GetStock("sku-1"))

	if err := s.Reserve("sku-1", 100); err != nil {
		fmt.Println("Очікувана помилка при спробі забрати 100:", err)
	}

	if err := s.Reserve("unknown-sku", 1); err != nil {
		fmt.Println("Очікувана помилка для неіснуючого товару:", err)
	}
}

func concurrentOversellDemo() {
	s := inventory.NewSafeInventoryService()
	s.AddProduct(&inventory.Product{ID: "sku-2", Stock: 100})

	const goroutines = 200
	var wg sync.WaitGroup
	var success, failed int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := s.Reserve("sku-2", 1); err == nil {
				atomic.AddInt64(&success, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("Успішних резервувань: %d (очікувалось 100)\n", success)
	fmt.Printf("Невдалих резервувань:  %d (очікувалось 100)\n", failed)
	fmt.Printf("Фінальний залишок:     %d (очікувалось 0)\n", s.GetStock("sku-2"))

	if success == 100 && s.GetStock("sku-2") == 0 {
		fmt.Println("✓ Oversell НЕ стався — блокування працює коректно")
	} else {
		fmt.Println("✗ Виявлено oversell!")
	}
}

func reserveMultipleDemo() {
	s := inventory.NewSafeInventoryService()
	s.AddProduct(&inventory.Product{ID: "A", Name: "Батарея", Stock: 10})
	s.AddProduct(&inventory.Product{ID: "B", Name: "Пропелер", Stock: 5})

	fmt.Printf("До операції:  A=%d, B=%d\n", s.GetStock("A"), s.GetStock("B"))

	err := s.ReserveMultiple([]inventory.ReserveItem{
		{ProductID: "A", Quantity: 8},
		{ProductID: "B", Quantity: 8}, // B має лише 5 — весь батч має провалитись
	})
	fmt.Println("Результат батчу (A:8, B:8):", err)
	fmt.Printf("Після невдалого батчу: A=%d, B=%d (обидва мають лишитись незмінними)\n",
		s.GetStock("A"), s.GetStock("B"))

	err = s.ReserveMultiple([]inventory.ReserveItem{
		{ProductID: "A", Quantity: 3},
		{ProductID: "B", Quantity: 2},
	})
	fmt.Println("Результат батчу (A:3, B:2):", err)
	fmt.Printf("Після успішного батчу:  A=%d, B=%d\n", s.GetStock("A"), s.GetStock("B"))
}

func concurrentReserveMultipleDemo() {
	s := inventory.NewSafeInventoryService()
	s.AddProduct(&inventory.Product{ID: "A", Stock: 50})
	s.AddProduct(&inventory.Product{ID: "B", Stock: 50})

	const goroutines = 100
	var wg sync.WaitGroup
	var success int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := s.ReserveMultiple([]inventory.ReserveItem{
				{ProductID: "A", Quantity: 1},
				{ProductID: "B", Quantity: 1},
			})
			if err == nil {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("Успішних батчів: %d (очікувалось 50)\n", success)
	fmt.Printf("Залишки: A=%d, B=%d (мають бути однаковими — рухались синхронно)\n",
		s.GetStock("A"), s.GetStock("B"))
}
