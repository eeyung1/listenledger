import test from "node:test";
import assert from "node:assert/strict";
import { computeKPIs, formatMoney, isLowStock, isToday } from "../dist/format.js";

test("KPI Calculation Suite", async (t) => {
  await t.test("isToday accurately identifies timestamps in local trading day", () => {
    const now = new Date();
    const todayISO = now.toISOString();
    assert.equal(isToday(todayISO), true);

    const yesterday = new Date(now.getTime() - 24 * 3600 * 1000);
    assert.equal(isToday(yesterday.toISOString()), false);

    assert.equal(isToday("invalid-date-string"), false);
  });

  await t.test("isLowStock detects products at or below threshold", () => {
    const normalProduct = {
      id: "p1",
      shop_id: "s1",
      name: "Milk",
      sku: "MILK",
      aliases: [],
      price: { amount: 10000, currency: "NGN" },
      quantity: 10,
      base_unit: "pack",
      low_stock_threshold: 5,
    };
    assert.equal(isLowStock(normalProduct), false);

    const lowProduct = { ...normalProduct, quantity: 5 };
    assert.equal(isLowStock(lowProduct), true);

    const outOfStock = { ...normalProduct, quantity: 0 };
    assert.equal(isLowStock(outOfStock), true);

    const noThresholdProduct = { ...normalProduct, quantity: 0, low_stock_threshold: 0 };
    assert.equal(isLowStock(noThresholdProduct), false);
  });

  await t.test("computeKPIs correctly derives metrics from transactions and catalogue", () => {
    const today = new Date().toISOString();
    const yesterday = new Date(Date.now() - 24 * 3600 * 1000).toISOString();

    const transactions = [
      // Today's confirmed sale (counts toward sales count and revenue)
      {
        id: "txn_001",
        shop_id: "shop_1",
        type: "sale",
        product_id: "prd_1",
        product_name: "Milo",
        quantity: 2,
        unit: "pack",
        amount: { amount: 400000, currency: "NGN" }, // 4,000 NGN
        confidence: { overall: 0.99 },
        status: "confirmed",
        occurred_at: today,
        created_at: today,
      },
      // Another confirmed sale today
      {
        id: "txn_002",
        shop_id: "shop_1",
        type: "sale",
        product_id: "prd_2",
        product_name: "Peak Milk",
        quantity: 1,
        unit: "piece",
        amount: { amount: 350000, currency: "NGN" }, // 3,500 NGN
        confidence: { overall: 0.97 },
        status: "confirmed",
        occurred_at: today,
        created_at: today,
      },
      // Restock today (MUST NOT count toward sales revenue or sales count)
      {
        id: "txn_003",
        shop_id: "shop_1",
        type: "restock",
        product_id: "prd_1",
        product_name: "Milo",
        quantity: 5,
        unit: "carton",
        amount: { amount: 5000000, currency: "NGN" },
        confidence: { overall: 0.95 },
        status: "confirmed",
        occurred_at: today,
        created_at: today,
      },
      // Review required item (counts toward reviewCount, NOT revenue)
      {
        id: "txn_004",
        shop_id: "shop_1",
        type: "sale",
        product_id: "prd_1",
        product_name: "Milo",
        quantity: 1,
        unit: "pack",
        amount: { amount: 200000, currency: "NGN" },
        confidence: { overall: 0.55 },
        status: "review_required",
        occurred_at: today,
        created_at: today,
      },
      // Yesterday's sale (MUST NOT count toward today's revenue or count)
      {
        id: "txn_005",
        shop_id: "shop_1",
        type: "sale",
        product_id: "prd_1",
        product_name: "Milo",
        quantity: 3,
        unit: "pack",
        amount: { amount: 600000, currency: "NGN" },
        confidence: { overall: 0.98 },
        status: "confirmed",
        occurred_at: yesterday,
        created_at: yesterday,
      },
    ];

    const products = [
      {
        id: "prd_1",
        shop_id: "shop_1",
        name: "Milo",
        sku: "MILO",
        aliases: [],
        price: { amount: 200000, currency: "NGN" },
        quantity: 18,
        base_unit: "pack",
        low_stock_threshold: 6, // 18 > 6: Not low
      },
      {
        id: "prd_2",
        shop_id: "shop_1",
        name: "Peak Milk",
        sku: "PEAK",
        aliases: [],
        price: { amount: 350000, currency: "NGN" },
        quantity: 4,
        base_unit: "piece",
        low_stock_threshold: 6, // 4 <= 6: LOW STOCK
      },
      {
        id: "prd_3",
        shop_id: "shop_1",
        name: "Indomie",
        sku: "INDO",
        aliases: [],
        price: { amount: 50000, currency: "NGN" },
        quantity: 2,
        base_unit: "pack",
        low_stock_threshold: 10, // 2 <= 10: LOW STOCK
      },
    ];

    const kpis = computeKPIs(transactions, products);

    // Revenue = txn_001 (400,000) + txn_002 (350,000) = 750,000 kobo = 7,500 NGN
    assert.equal(kpis.revenue.amount, 750000);
    assert.equal(kpis.revenue.currency, "NGN");
    assert.equal(kpis.salesCount, 2);
    assert.equal(kpis.reviewCount, 1);
    assert.equal(kpis.lowStockCount, 2);

    // Formatted output
    const formattedRevenue = formatMoney(kpis.revenue);
    assert.match(formattedRevenue, /7,500/);
  });

  await t.test("computeKPIs prioritizes server-authoritative DailySummary when provided", () => {
    const summary = {
      shop_id: "shop_1",
      date: "2026-10-14",
      today_sales_amount: { amount: 1500000, currency: "NGN" },
      today_sales_count: 8,
      pending_reviews: 3,
      low_stock_count: 5,
    };

    const kpis = computeKPIs([], [], summary);
    assert.equal(kpis.revenue.amount, 1500000);
    assert.equal(kpis.salesCount, 8);
    assert.equal(kpis.reviewCount, 3);
    assert.equal(kpis.lowStockCount, 5);
  });
});
