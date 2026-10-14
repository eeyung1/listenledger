import test from "node:test";
import assert from "node:assert/strict";
import {
  applyFilteredTransactionToList,
  matchesTransactionFilters,
  sortTransactions,
} from "../dist/format.js";

test("Filtered Realtime Ledger Updates & Event Reconciliation Suite", async (t) => {
  const sampleSale = {
    id: "txn_001",
    shop_id: "shop_1",
    type: "sale",
    product_name: "Milo 500g",
    quantity: 2,
    unit: "pack",
    amount: { amount: 400000, currency: "NGN" },
    confidence: { overall: 0.95 },
    status: "confirmed",
    receipt_id: "rcp_100",
    occurred_at: "2026-10-14T10:00:00Z",
    created_at: "2026-10-14T10:00:00Z",
  };

  const sampleRestock = {
    id: "txn_002",
    shop_id: "shop_1",
    type: "restock",
    product_name: "Peak Milk",
    quantity: 3,
    unit: "carton",
    amount: { amount: 10000000, currency: "NGN" },
    confidence: { overall: 0.92 },
    status: "confirmed",
    receipt_id: "rcp_200",
    occurred_at: "2026-10-14T11:00:00Z",
    created_at: "2026-10-14T11:00:00Z",
  };

  const sampleReview = {
    id: "txn_003",
    shop_id: "shop_1",
    type: "sale",
    product_name: "Unknown Drink",
    quantity: 1,
    unit: "bottle",
    amount: { amount: 50000, currency: "NGN" },
    confidence: { overall: 0.45 },
    status: "review_required",
    occurred_at: "2026-10-14T12:00:00Z",
    created_at: "2026-10-14T12:00:00Z",
  };

  await t.test("matchesTransactionFilters correctly evaluates active filter criteria", () => {
    // 1. Empty filter matches everything
    assert.equal(matchesTransactionFilters(sampleSale, {}), true);
    assert.equal(matchesTransactionFilters(sampleRestock, { type: "" }), true);

    // 2. Type filter
    assert.equal(matchesTransactionFilters(sampleSale, { type: "sale" }), true);
    assert.equal(matchesTransactionFilters(sampleSale, { type: "restock" }), false);
    assert.equal(matchesTransactionFilters(sampleRestock, { type: "restock" }), true);

    // 3. Status filter
    assert.equal(matchesTransactionFilters(sampleSale, { status: "confirmed" }), true);
    assert.equal(matchesTransactionFilters(sampleReview, { status: "confirmed" }), false);
    assert.equal(matchesTransactionFilters(sampleReview, { status: "review_required" }), true);

    // 4. Receipt ID filter
    assert.equal(matchesTransactionFilters(sampleSale, { receipt_id: "rcp_100" }), true);
    assert.equal(matchesTransactionFilters(sampleSale, { receipt_id: "rcp_999" }), false);
    assert.equal(matchesTransactionFilters(sampleReview, { receipt_id: "rcp_100" }), false);

    // 5. Date range filters
    assert.equal(
      matchesTransactionFilters(sampleSale, {
        from: "2026-10-14T09:00:00Z",
        to: "2026-10-14T10:30:00Z",
      }),
      true,
    );
    assert.equal(
      matchesTransactionFilters(sampleSale, {
        from: "2026-10-14T11:00:00Z",
      }),
      false,
    );
  });

  await t.test("applyFilteredTransactionToList admits matching items on page 1 and ignores non-matching", () => {
    const list = [{ ...sampleSale }];
    const filter = { type: "sale" };

    // A restock arrives via WebSocket -> must be ignored from the Sales view
    const res1 = applyFilteredTransactionToList(list, sampleRestock, true, filter, 0);
    assert.equal(res1.action, "ignored");
    assert.equal(res1.totalIncrement, 0);
    assert.equal(list.length, 1);
    assert.equal(list[0].id, "txn_001");

    // A new sale arrives via WebSocket -> must be inserted and sorted newest-first
    const newSale = {
      ...sampleSale,
      id: "txn_004",
      occurred_at: "2026-10-14T13:00:00Z",
    };
    const res2 = applyFilteredTransactionToList(list, newSale, true, filter, 0);
    assert.equal(res2.action, "inserted");
    assert.equal(res2.totalIncrement, 1);
    assert.equal(list.length, 2);
    assert.equal(list[0].id, "txn_004"); // newest
    assert.equal(list[1].id, "txn_001");
  });

  await t.test("applyFilteredTransactionToList removes items when update causes them to no longer match", () => {
    // User is filtering by "review_required"
    const list = [{ ...sampleReview }];
    const filter = { status: "review_required" };

    assert.equal(list.length, 1);

    // Owner confirms review -> status becomes "confirmed"
    const confirmedTxn = { ...sampleReview, status: "confirmed" };
    const res = applyFilteredTransactionToList(list, confirmedTxn, false, filter, 0);

    assert.equal(res.action, "removed");
    assert.equal(res.totalIncrement, -1);
    assert.equal(list.length, 0); // cleanly removed from the review-required view!
  });

  await t.test("applyFilteredTransactionToList updates items in place when update still matches", () => {
    const list = [{ ...sampleSale }];
    const filter = { type: "sale" };

    const updatedSale = {
      ...sampleSale,
      amount: { amount: 450000, currency: "NGN" },
    };
    const res = applyFilteredTransactionToList(list, updatedSale, false, filter, 0);

    assert.equal(res.action, "updated");
    assert.equal(res.totalIncrement, 0);
    assert.equal(list.length, 1);
    assert.equal(list[0].amount.amount, 450000);
  });

  await t.test("applyFilteredTransactionToList preserves page slice stability on pageOffset > 0", () => {
    // User is browsing Page 2 (items 51-100) with pageOffset = 50
    const pageTwoList = [
      { ...sampleSale, id: "txn_page2_item1" },
      { ...sampleSale, id: "txn_page2_item2" },
    ];
    const filter = { type: "sale" };

    const incomingSale = {
      ...sampleSale,
      id: "txn_brand_new",
      occurred_at: "2026-10-14T15:00:00Z",
    };

    // When on pageOffset = 50, new transactions should NOT inject into page 2 slice,
    // but totalIncrement must reflect the new transaction so pageInfo updates.
    const res = applyFilteredTransactionToList(pageTwoList, incomingSale, true, filter, 50);

    assert.equal(res.action, "ignored");
    assert.equal(res.totalIncrement, 1);
    assert.equal(pageTwoList.length, 2);
    assert.equal(pageTwoList[0].id, "txn_page2_item1");
    assert.equal(pageTwoList[1].id, "txn_page2_item2");
  });

  await t.test("applyFilteredTransactionToList clamps list to limit on newest page", () => {
    const list = [{ ...sampleSale, id: "txn_old", occurred_at: "2026-10-14T08:00:00Z" }];
    const filter = {};

    const newer1 = { ...sampleSale, id: "txn_mid", occurred_at: "2026-10-14T09:00:00Z" };
    const newer2 = { ...sampleSale, id: "txn_new", occurred_at: "2026-10-14T10:00:00Z" };

    applyFilteredTransactionToList(list, newer1, true, filter, 0, 2);
    applyFilteredTransactionToList(list, newer2, true, filter, 0, 2);

    assert.equal(list.length, 2);
    assert.equal(list[0].id, "txn_new");
    assert.equal(list[1].id, "txn_mid");
    assert.equal(list.find((t) => t.id === "txn_old"), undefined); // clamped
  });
});
