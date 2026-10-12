package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

// devSeeder owns the development dataset.
type devSeeder struct {
	store     *storage.Store
	publisher Publisher
	log       *slog.Logger
}

// NewDevSeeder returns the handler for POST /api/dev/seed.
//
//	curl -X POST 'localhost:8080/api/dev/seed?shop_id=shop-1'
//
// DEVELOPMENT ONLY. Mount it only when the process was started with -dev. It
// wipes the named shop's catalogue and ledger before inserting a known dataset,
// so it is both destructive and unauthenticated: anyone who can reach the port
// can call it.
//
// It exists because the ingestion endpoints have no writers yet. Once the Rust
// edge node posts real captures, this is scaffolding — the dataset is a fixture,
// not a migration.
func NewDevSeeder(store *storage.Store, publisher Publisher, logger *slog.Logger) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	seeder := &devSeeder{store: store, publisher: publisher, log: logger}
	return seeder.serve
}

func (d *devSeeder) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	// The shop is resolved the same way every other endpoint resolves it, so
	// the seeder cannot write somewhere the dashboard would not read.
	shopID, err := auth.QueryShopResolver(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "a valid shop_id is required")
		return
	}

	ctx := r.Context()

	if err := d.store.DeleteShopData(ctx, shopID); err != nil {
		d.log.Error("dev: clear shop before seeding", "shop_id", shopID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clear the shop")
		return
	}

	products := buildSeedProducts(shopID)
	for _, product := range products {
		if err := d.store.InsertProduct(ctx, product); err != nil {
			d.log.Error("dev: insert seed product", "product_id", product.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "could not seed products")
			return
		}
	}

	transactions := buildSeedTransactions(shopID, products)
	for _, transaction := range transactions {
		if err := d.store.InsertTransaction(ctx, transaction); err != nil {
			d.log.Error("dev: insert seed transaction", "transaction_id", transaction.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "could not seed transactions")
			return
		}
	}

	// Published only after every write has committed, so a dashboard reacting
	// to the first event cannot fetch a snapshot that is still half-seeded.
	d.publishAll(shopID, products, transactions)

	d.log.Warn("dev: shop seeded",
		"shop_id", shopID, "products", len(products), "transactions", len(transactions))

	writeJSON(w, http.StatusCreated, map[string]any{
		"shop_id":      shopID,
		"products":     len(products),
		"transactions": len(transactions),
		"review_queue": countByStatus(transactions, models.StatusReviewRequired),
		"note":         "existing data for this shop was deleted first",
	})
}

// publishAll announces the seeded state to any connected dashboard.
//
// Every product gets both product.created and inventory.updated: the catalogue
// view needs the first, and the second is the event that carrying a stock level
// is what lets the overview recompute its low-stock alerts.
func (d *devSeeder) publishAll(shopID string, products []models.Product, transactions []models.Transaction) {
	if d.publisher == nil {
		return
	}

	for _, product := range products {
		d.publish(shopID, ws.EventProductCreated, product)
		d.publish(shopID, ws.EventInventoryUpdated, product)
	}

	for _, transaction := range transactions {
		eventType := ws.EventTransactionCreated
		if transaction.Status == models.StatusReviewRequired {
			eventType = ws.EventTransactionReviewRequired
		}
		d.publish(shopID, eventType, transaction)
	}
}

func (d *devSeeder) publish(shopID string, eventType ws.EventType, payload any) {
	if !d.publisher.Publish(shopID, eventType, payload) {
		d.log.Warn("dev: seed event not delivered", "type", eventType, "shop_id", shopID)
	}
}

// buildSeedProducts returns the four catalogue entries.
//
// Two are deliberately at or below their reorder level so the overview's
// low-stock panel has something to show, and Milo carries a packaging rule so
// the §10 carton-in / pack-out conversion has a product to exercise.
func buildSeedProducts(shopID string) []models.Product {
	ngn := func(minor int64) models.Money {
		return models.Money{Amount: minor, Currency: models.CurrencyNGN}
	}

	return []models.Product{
		{
			ID:       "prd_seed_milo",
			ShopID:   shopID,
			Name:     "Milo",
			SKU:      "MILO-400G",
			Aliases:  []string{"Milo", "Milo tin", "Milo refill", "Milo 400g"},
			Price:    ngn(200000), // NGN 2,000 per pack
			Quantity: 18,
			BaseUnit: models.UnitPack,
			Packaging: []models.PackagingRelation{
				{From: models.UnitCarton, To: models.UnitPack, Factor: 12},
			},
			LowStockThreshold: 6,
		},
		{
			ID:      "prd_seed_peak",
			ShopID:  shopID,
			Name:    "Peak Milk 400g",
			SKU:     "PEAK-400",
			Aliases: []string{"Peak", "Peak tin", "Peak milk", "Big Peak"}, // blueprint §9
			Price:   ngn(350000),                                            // NGN 3,500
			// At the reorder level, so the overview has a second alert.
			Quantity:          4,
			BaseUnit:          models.UnitPiece,
			LowStockThreshold: 6,
		},
		{
			ID:       "prd_seed_indomie",
			ShopID:   shopID,
			Name:     "Indomie",
			SKU:      "INDO-70G",
			Aliases:  []string{"Indomie", "Indomie noodles", "Noodles"},
			Price:    ngn(50000), // NGN 500 per pack
			Quantity: 16,
			BaseUnit: models.UnitPack,
			Packaging: []models.PackagingRelation{
				{From: models.UnitCarton, To: models.UnitPack, Factor: 40},
			},
			LowStockThreshold: 10,
		},
		{
			ID:      "prd_seed_pepsi",
			ShopID:  shopID,
			Name:    "Pepsi 50cl",
			SKU:     "PEPSI-50CL",
			Aliases: []string{"Pepsi", "Pepsi PET", "Pepsi bottle"},
			Price:   ngn(240000), // NGN 2,400 per pack
			// Below the reorder level: the blueprint's §10 restock brings this
			// back up, which is the two-way flow the inventory view shows.
			Quantity:          5,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 8,
		},
	}
}

// buildSeedTransactions returns the six movements.
//
// It covers what the dashboard has to render: both directions, both statuses,
// three units (pack, piece, carton), a resolved product and the confidence
// split of blueprint §11. Timestamps are relative to now so the ledger is
// ordered and the overview's "today" figures are non-zero whenever it is run.
func buildSeedTransactions(shopID string, products []models.Product) []models.Transaction {
	byID := make(map[string]models.Product, len(products))
	for _, product := range products {
		byID[product.ID] = product
	}

	ngn := func(minor int64) models.Money {
		return models.Money{Amount: minor, Currency: models.CurrencyNGN}
	}
	score := func(value float64) *float64 { return &value }

	now := time.Now().UTC()
	ago := func(minutes int) time.Time { return now.Add(-time.Duration(minutes) * time.Minute) }

	// Each entry names its product by seed ID; the name and price are read back
	// from the catalogue so the ledger cannot disagree with it.
	entries := []struct {
		id         string
		kind       models.TransactionType
		productID  string
		quantity   int
		unit       models.Unit
		amount     models.Money
		confidence *models.Confidence
		status     models.TransactionStatus
		minutes    int
		transcript string
	}{
		{
			id: "txn_seed_0001", kind: models.TypeSale, productID: "prd_seed_milo",
			quantity: 2, unit: models.UnitPack, amount: ngn(400000), // NGN 4,000
			confidence: &models.Confidence{Overall: 0.98, Product: score(0.98), Quantity: score(0.99), Amount: score(0.98)},
			status:     models.StatusConfirmed, minutes: 110,
			transcript: "Two Milo, four thousand naira. Thank you.",
		},
		{
			id: "txn_seed_0002", kind: models.TypeSale, productID: "prd_seed_peak",
			quantity: 1, unit: models.UnitPiece, amount: ngn(350000), // NGN 3,500
			confidence: &models.Confidence{Overall: 0.96, Product: score(0.97), Quantity: score(0.95), Amount: score(0.96)},
			status:     models.StatusConfirmed, minutes: 95,
			transcript: "One Peak tin, three thousand five hundred naira. Thank you.",
		},
		{
			// Blueprint §11: the product matched at 55% while quantity and
			// amount were both high, so the weakest anchor holds the capture.
			id: "txn_seed_0003", kind: models.TypeSale, productID: "prd_seed_indomie",
			quantity: 10, unit: models.UnitPack, amount: ngn(500000), // NGN 5,000
			confidence: &models.Confidence{Overall: 0.55, Product: score(0.55), Quantity: score(0.98), Amount: score(0.97)},
			status:     models.StatusReviewRequired, minutes: 70,
			transcript: "Ten Indomie, five thousand naira. Thank you.",
		},
		{
			// Blueprint §10's receipt, first line.
			id: "txn_seed_0004", kind: models.TypeRestock, productID: "prd_seed_pepsi",
			quantity: 5, unit: models.UnitPack, amount: ngn(1200000), // NGN 12,000
			confidence: &models.Confidence{Overall: 0.94, Product: score(0.96), Quantity: score(0.93), Amount: score(0.92)},
			status:     models.StatusConfirmed, minutes: 45,
			transcript: "Five packs of Pepsi, twelve thousand naira. Stock received.",
		},
		{
			// A restock spoken in cartons against a product stocked in packs:
			// the conversion the packaging rule exists for.
			id: "txn_seed_0005", kind: models.TypeRestock, productID: "prd_seed_milo",
			quantity: 3, unit: models.UnitCarton, amount: ngn(4500000), // NGN 45,000
			confidence: &models.Confidence{Overall: 0.91, Product: score(0.93), Quantity: score(0.95), Amount: score(0.88)},
			status:     models.StatusConfirmed, minutes: 25,
			transcript: "Three cartons of Milo, forty-five thousand naira. Stock received.",
		},
		{
			// Blueprint §10: "If confidence is low, the restock should go to
			// Review Required instead of silently changing inventory."
			id: "txn_seed_0006", kind: models.TypeRestock, productID: "prd_seed_indomie",
			quantity: 10, unit: models.UnitPack, amount: ngn(3000000), // NGN 30,000
			confidence: &models.Confidence{Overall: 0.62, Product: score(0.62), Quantity: score(0.90), Amount: score(0.97)},
			status:     models.StatusReviewRequired, minutes: 8,
			transcript: "Ten packs of Indomie, thirty thousand naira. Stock received.",
		},
	}

	transactions := make([]models.Transaction, 0, len(entries))
	for _, entry := range entries {
		occurredAt := ago(entry.minutes)
		transactions = append(transactions, models.Transaction{
			ID:          entry.id,
			ShopID:      shopID,
			Type:        entry.kind,
			ProductID:   entry.productID,
			ProductName: byID[entry.productID].Name,
			Quantity:    entry.quantity,
			Unit:        entry.unit,
			Amount:      entry.amount,
			Confidence:  *entry.confidence,
			Status:      entry.status,
			OccurredAt:  occurredAt,
			CreatedAt:   occurredAt,
			Transcript:  entry.transcript,
		})
	}
	return transactions
}

func countByStatus(transactions []models.Transaction, status models.TransactionStatus) int {
	count := 0
	for _, transaction := range transactions {
		if transaction.Status == status {
			count++
		}
	}
	return count
}
