package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// productColumns is the column list every product read uses, in the order
// scanProduct expects.
const productColumns = `id, shop_id, name, sku, aliases, price_amount,
	price_currency, quantity, base_unit, packaging, low_stock_threshold, archived_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// ProductQuery specifies server-side search, status filtering, and pagination parameters.
type ProductQuery struct {
	Search string // Matches name, SKU, or aliases (case-insensitive substring)
	Status string // "active" (archived_at IS NULL), "archived" (archived_at IS NOT NULL), or "all" (default)
	Limit  int
	Offset int
}

// QueryProducts returns filtered catalogue items for a shop along with total matching count.
func (s *Store) QueryProducts(ctx context.Context, shopID string, q ProductQuery) ([]models.Product, int, error) {
	if shopID == "" {
		return nil, 0, errors.New("storage: QueryProducts requires a shop_id")
	}

	whereClause := "WHERE shop_id = ?"
	args := []any{shopID}

	switch strings.ToLower(strings.TrimSpace(q.Status)) {
	case "active":
		whereClause += " AND archived_at IS NULL"
	case "archived":
		whereClause += " AND archived_at IS NOT NULL"
	}

	if search := strings.TrimSpace(q.Search); search != "" {
		pattern := "%" + strings.ToLower(search) + "%"
		whereClause += " AND (LOWER(name) LIKE ? OR LOWER(sku) LIKE ? OR LOWER(aliases) LIKE ?)"
		args = append(args, pattern, pattern, pattern)
	}

	// 1. Get total matching count
	countQuery := "SELECT COUNT(*) FROM products " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind(countQuery), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count products: %w", err)
	}

	// 2. Query products with ordering and optional pagination
	dataQuery := "SELECT " + productColumns + " FROM products " + whereClause + " ORDER BY name ASC"
	dataArgs := append([]any{}, args...)
	if q.Limit > 0 {
		dataQuery += " LIMIT ?"
		dataArgs = append(dataArgs, q.Limit)
		if q.Offset > 0 {
			dataQuery += " OFFSET ?"
			dataArgs = append(dataArgs, q.Offset)
		}
	}

	products, err := s.queryProducts(ctx, s.db, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, err
	}

	return products, total, nil
}

// ListProducts returns the whole catalogue for a shop, ordered by name.
//
// The pilot catalogue is 20-50 products, so returning all of them is cheaper
// than paging and lets the validation engine hold the full alias set in memory
// when matching a capture.
func (s *Store) ListProducts(ctx context.Context, shopID string) ([]models.Product, error) {
	if shopID == "" {
		return nil, errors.New("storage: ListProducts requires a shop_id")
	}

	query := `SELECT ` + productColumns + ` FROM products WHERE shop_id = ? ORDER BY name`
	return s.queryProducts(ctx, s.db, query, shopID)
}

// GetProductByID returns one product, scoped to its shop.
func (s *Store) GetProductByID(ctx context.Context, shopID, productID string) (models.Product, error) {
	query := `SELECT ` + productColumns + ` FROM products WHERE shop_id = ? AND id = ?`
	return s.getProduct(ctx, s.db, query, shopID, productID)
}

// GetProductBySKU returns one product by its shop-scoped SKU.
func (s *Store) GetProductBySKU(ctx context.Context, shopID, sku string) (models.Product, error) {
	query := `SELECT ` + productColumns + ` FROM products WHERE shop_id = ? AND sku = ?`
	return s.getProduct(ctx, s.db, query, shopID, sku)
}

// InsertProduct adds a catalogue entry.
//
// The caller supplies the ID: storage does not invent identifiers, so that the
// same ID can be used in the event published to dashboards and in logs without
// a read-back.
func (s *Store) InsertProduct(ctx context.Context, product models.Product) error {
	if product.ShopID == "" {
		return errors.New("storage: InsertProduct requires a shop_id")
	}
	if product.ID == "" {
		return errors.New("storage: InsertProduct requires an id")
	}

	aliases, err := encodeJSONList("aliases", product.Aliases)
	if err != nil {
		return err
	}
	packaging, err := encodeJSONList("packaging", product.Packaging)
	if err != nil {
		return err
	}

	var archivedAtVal any = nil
	if product.ArchivedAt != nil {
		archivedAtVal = formatTime(*product.ArchivedAt)
	}

	now := formatTime(time.Now())
	const query = `
		INSERT INTO products (
			id, shop_id, name, sku, aliases, price_amount, price_currency,
			quantity, base_unit, packaging, low_stock_threshold,
			created_at, updated_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, s.rebind(query),
		product.ID, product.ShopID, product.Name, product.SKU,
		aliases, product.Price.Amount, product.Price.Currency,
		product.Quantity, string(product.BaseUnit), packaging,
		product.LowStockThreshold, now, now, archivedAtVal,
	)
	if err != nil {
		return fmt.Errorf("storage: insert product %s: %w", product.ID, err)
	}
	return nil
}

// UpdateProduct rewrites the editable fields of an existing product: name,
// price, aliases, unit, packaging and reorder level.
//
// Quantity is deliberately excluded — stock moves through AdjustStock, which
// is the only path that guards against a negative result.
func (s *Store) UpdateProduct(ctx context.Context, product models.Product) (models.Product, error) {
	if product.ShopID == "" {
		return models.Product{}, errors.New("storage: UpdateProduct requires a shop_id")
	}

	aliases, err := encodeJSONList("aliases", product.Aliases)
	if err != nil {
		return models.Product{}, err
	}
	packaging, err := encodeJSONList("packaging", product.Packaging)
	if err != nil {
		return models.Product{}, err
	}

	const query = `
		UPDATE products
		   SET name = ?, sku = ?, aliases = ?, price_amount = ?,
		       price_currency = ?, base_unit = ?, packaging = ?,
		       low_stock_threshold = ?, updated_at = ?
		 WHERE shop_id = ? AND id = ?`

	result, err := s.db.ExecContext(ctx, s.rebind(query),
		product.Name, product.SKU, aliases, product.Price.Amount,
		product.Price.Currency, string(product.BaseUnit), packaging,
		product.LowStockThreshold, formatTime(time.Now()),
		product.ShopID, product.ID,
	)
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: update product %s: %w", product.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: update product %s: %w", product.ID, err)
	}
	if affected == 0 {
		return models.Product{}, ErrNotFound
	}
	return s.GetProductByID(ctx, product.ShopID, product.ID)
}

// AdjustStock applies a signed delta to a product's stock and returns the
// updated row. A sale passes a negative delta, a restock a positive one.
//
// The adjustment is refused if it would drive the quantity below zero. That is
// a deliberate guard rather than an oversight: silently recording negative
// stock hides the two failures that actually matter — a sale of goods that
// were never received, and a missing restock — and both need the owner's
// attention rather than a plausible-looking number. Callers should surface
// ErrInsufficientStock as a review item.
//
// The caller is responsible for unit conversion: a restock spoken in cartons
// must be converted to the product's base unit before it reaches here.
func (s *Store) AdjustStock(ctx context.Context, shopID, productID string, delta int) (models.Product, error) {
	if shopID == "" {
		return models.Product{}, errors.New("storage: AdjustStock requires a shop_id")
	}
	if delta == 0 {
		return s.GetProductByID(ctx, shopID, productID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: adjust stock: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if s.Dialect().IsPostgres() {
		var dummy string
		_ = tx.QueryRowContext(ctx, s.rebind(`SELECT id FROM products WHERE shop_id = ? AND id = ? FOR UPDATE`), shopID, productID).Scan(&dummy)
	}

	const update = `
		UPDATE products
		   SET quantity = quantity + ?, updated_at = ?
		 WHERE shop_id = ? AND id = ? AND quantity + ? >= 0`

	result, err := tx.ExecContext(ctx, s.rebind(update), delta, formatTime(time.Now()), shopID, productID, delta)
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: adjust stock for %s: %w", productID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: adjust stock for %s: %w", productID, err)
	}
	if affected == 0 {
		// Either the product is not in this shop, or the result would have
		// been negative. They need different handling, so tell them apart.
		var exists bool
		err := tx.QueryRowContext(ctx,
			s.rebind(`SELECT EXISTS (SELECT 1 FROM products WHERE shop_id = ? AND id = ?)`),
			shopID, productID,
		).Scan(&exists)
		if err != nil {
			return models.Product{}, fmt.Errorf("storage: adjust stock for %s: %w", productID, err)
		}
		if !exists {
			return models.Product{}, ErrNotFound
		}
		return models.Product{}, ErrInsufficientStock
	}

	product, err := s.getProduct(ctx, tx,
		`SELECT `+productColumns+` FROM products WHERE shop_id = ? AND id = ?`,
		shopID, productID)
	if err != nil {
		return models.Product{}, err
	}

	if err := tx.Commit(); err != nil {
		return models.Product{}, fmt.Errorf("storage: adjust stock for %s: %w", productID, err)
	}
	return product, nil
}

// AdjustStockPackaging converts quantity in the given unit into the product's base unit
// using models.BaseQuantity, applies the signed delta (positive for restock / incoming, negative for sale / outgoing)
// to the product's stock, and returns the updated product.
//
// If unit is empty or equal to product.BaseUnit, quantity is used directly as base units.
func (s *Store) AdjustStockPackaging(ctx context.Context, shopID, productID string, quantity int, unit models.Unit, isSale bool) (models.Product, error) {
	if shopID == "" {
		return models.Product{}, errors.New("storage: AdjustStockPackaging requires a shop_id")
	}
	if productID == "" {
		return models.Product{}, errors.New("storage: AdjustStockPackaging requires a product_id")
	}
	if quantity < 0 {
		return models.Product{}, errors.New("storage: quantity cannot be negative")
	}
	if quantity == 0 {
		return s.GetProductByID(ctx, shopID, productID)
	}

	product, err := s.GetProductByID(ctx, shopID, productID)
	if err != nil {
		return models.Product{}, err
	}

	delta := quantity
	if unit != "" && unit != product.BaseUnit {
		converted, err := models.BaseQuantity(product, quantity, unit)
		if err != nil {
			return models.Product{}, fmt.Errorf("storage: adjust stock packaging: %w", err)
		}
		delta = converted
	}

	if isSale {
		delta = -delta
	}

	return s.AdjustStock(ctx, shopID, productID, delta)
}

func (s *Store) queryProducts(ctx context.Context, q querier, query string, args ...any) ([]models.Product, error) {
	rows, err := q.QueryContext(ctx, s.rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query products: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty catalogue encodes as [] rather than null.
	products := make([]models.Product, 0)
	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate products: %w", err)
	}
	return products, nil
}

func (s *Store) getProduct(ctx context.Context, q querier, query string, args ...any) (models.Product, error) {
	product, err := scanProduct(q.QueryRowContext(ctx, s.rebind(query), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return models.Product{}, ErrNotFound
	}
	if err != nil {
		return models.Product{}, err
	}
	return product, nil
}

func scanProduct(row scanner) (models.Product, error) {
	var (
		product                 models.Product
		aliases, packaging      string
		priceAmount             int64
		priceCurrency, baseUnit string
		archivedAtRaw           sql.NullString
	)

	err := row.Scan(
		&product.ID, &product.ShopID, &product.Name, &product.SKU,
		&aliases, &priceAmount, &priceCurrency, &product.Quantity,
		&baseUnit, &packaging, &product.LowStockThreshold, &archivedAtRaw,
	)
	if err != nil {
		return models.Product{}, err
	}

	product.Price = models.Money{Amount: priceAmount, Currency: priceCurrency}
	product.BaseUnit = models.Unit(baseUnit)
	if archivedAtRaw.Valid && archivedAtRaw.String != "" {
		if t, err := parseTime(archivedAtRaw.String); err == nil {
			product.ArchivedAt = &t
		}
	}

	if err := json.Unmarshal([]byte(aliases), &product.Aliases); err != nil {
		return models.Product{}, fmt.Errorf("storage: decode aliases for %s: %w", product.ID, err)
	}
	if err := json.Unmarshal([]byte(packaging), &product.Packaging); err != nil {
		return models.Product{}, fmt.Errorf("storage: decode packaging for %s: %w", product.ID, err)
	}
	return product, nil
}

// ArchiveProduct soft-deactivates an existing product without deleting historical records.
func (s *Store) ArchiveProduct(ctx context.Context, shopID, productID string) (models.Product, error) {
	if shopID == "" || productID == "" {
		return models.Product{}, errors.New("storage: ArchiveProduct requires shop_id and product_id")
	}

	now := formatTime(time.Now().UTC())
	const query = `UPDATE products SET archived_at = ?, updated_at = ? WHERE shop_id = ? AND id = ? AND archived_at IS NULL`
	res, err := s.db.ExecContext(ctx, s.rebind(query), now, now, shopID, productID)
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: archive product %s: %w", productID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: archive product %s: %w", productID, err)
	}
	if affected == 0 {
		return models.Product{}, ErrNotFound
	}
	return s.GetProductByID(ctx, shopID, productID)
}

// UnarchiveProduct restores an archived product back to the active catalogue.
func (s *Store) UnarchiveProduct(ctx context.Context, shopID, productID string) (models.Product, error) {
	if shopID == "" || productID == "" {
		return models.Product{}, errors.New("storage: UnarchiveProduct requires shop_id and product_id")
	}

	now := formatTime(time.Now().UTC())
	const query = `UPDATE products SET archived_at = NULL, updated_at = ? WHERE shop_id = ? AND id = ? AND archived_at IS NOT NULL`
	res, err := s.db.ExecContext(ctx, s.rebind(query), now, shopID, productID)
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: unarchive product %s: %w", productID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return models.Product{}, fmt.Errorf("storage: unarchive product %s: %w", productID, err)
	}
	if affected == 0 {
		return models.Product{}, ErrNotFound
	}
	return s.GetProductByID(ctx, shopID, productID)
}

// encodeJSONList marshals a slice column, normalising nil to an empty array so
// the NOT NULL constraint holds and the stored value is always valid JSON.
func encodeJSONList[T any](column string, values []T) (string, error) {
	if values == nil {
		values = []T{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("storage: encode %s: %w", column, err)
	}
	return string(encoded), nil
}
