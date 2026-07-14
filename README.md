# TaskTest_FOR_MediDrive — Thread-Safe Inventory Service


## Структура репозиторію

```
.
├── go.mod              # module TaskTest_FOR_MediDrive
├── inventory.go         # SafeInventoryService — потокобезпечна реалізація
├── inventory_test.go    # Конкурентні тести (sync.WaitGroup, -race)
├── cmd/demo/main.go     # Демонстраційна CLI-програма
├── REVIEW.md             # Аналіз 4 race conditions з вихідного коду
└── ANSWERS.md            # Відповіді на контрольні питання (Q1-Q4)
```

## Швидкий старт

Потрібен Go 1.22+.

```bash
git clone <your-repo-url>
cd TaskTest_FOR_MediDrive

# Запустити демо-програму
go run ./cmd/demo

# Запустити тести з race detector
go test -race -v ./...

# Кілька прогонів поспіль для стабільності конкурентних тестів
go test -race -count=10 ./...
```

## API сервісу

```go
s := inventory.NewSafeInventoryService()

s.AddProduct(&inventory.Product{ID: "sku-1", Name: "Дрон DJI Mini", Stock: 10})

stock := s.GetStock("sku-1")              // потокобезпечне читання
err := s.Reserve("sku-1", 3)               // атомарний check-and-reserve

err = s.ReserveMultiple([]inventory.ReserveItem{
    {ProductID: "A", Quantity: 2},
    {ProductID: "B", Quantity: 1},
})                                          // all-or-nothing для кількох товарів
```

| Метод | Блокування | Гарантія |
|---|---|---|
| `GetStock` | `RLock` | Не блокує інші читання |
| `Reserve` | `Lock` | Check + update в одній критичній секції |
| `ReserveMultiple` | `Lock` | Всі товари перевіряються і резервуються атомарно, або жоден |

## Знайдені race conditions

Детальний розбір з production-сценаріями — у [REVIEW.md](./REVIEW.md). Коротко:

1. **Unsynchronized map access** (`GetStock`, `Reserve`) — читання/запис мапи без локу → `fatal error: concurrent map read and map write`.
2. **Check-then-act у `Reserve`** (TOCTOU) — розрив між перевіркою залишку і відніманням дозволяє двом горутинам одночасно "пройти" перевірку → oversell.
3. **Two-phase `ReserveMultiple`** — окремі фази перевірки й застосування без спільного блокування → часткове застосування батчу, відсутність atomicity.
4. **Локальний м'ютекс у `SafeReserve`** — `var mu sync.Mutex` оголошений усередині функції створюється заново при кожному виклику, тому ніколи не конкурує з іншими горутинами — фактично еквівалентно відсутності локу.

## Підхід до синхронізації

`SafeInventoryService` використовує **один** `sync.RWMutex` як поле структури для всього стану (мапи продуктів і їхніх `Stock`), а не per-product локи. Це свідомий вибір:

- `ReserveMultiple` мусить перевірити й змінити кілька товарів як єдину атомарну операцію — per-product локи повертають класичну проблему **lock ordering deadlock** (див. [ANSWERS.md, Q2](./ANSWERS.md#q2)).
- Одне блокування простіше довести до коректності й легше рев'ювати, ціною дещо нижчої паралельності запису. Якщо профілювання в реальному навантаженні покаже, що це вузьке місце — наступний крок не "локи на кожен продукт", а шардинг за детермінованим порядком ключів.

Повні відповіді на питання про deadlock, "фальшиві" фікси з раннім розлочуванням і межі `-race` — у [ANSWERS.md](./ANSWERS.md).

## Тестування

`inventory_test.go` містить:

- `TestGetStock_Basic`, `TestReserve_Basic` — базова коректність
- `TestReserve_ConcurrentOversell` — 100 одиниць стоку, 200 горутин, кожна резервує 1; перевіряється, що рівно 100 успіхів і фінальний залишок 0
- `TestReserveMultiple_Atomicity` — невдалий батч не змінює жоден товар
- `TestReserveMultiple_ConcurrentAtomicity` — та сама гарантія під конкурентним навантаженням (100 горутин)
- `TestGetStock_ConcurrentWithReserve` — одночасні читачі й писачі під `-race`

Усі конкурентні тести перевіряють не лише відсутність варнінгів `-race`, а й точний бізнес-результат (конкретні числа успіхів/невдач), оскільки `-race` ловить лише data races, які реально сталися під час конкретного прогону, і не гарантує відсутність логічних помилок конкурентності (детальніше — [ANSWERS.md, Q4](./ANSWERS.md#q4)).
