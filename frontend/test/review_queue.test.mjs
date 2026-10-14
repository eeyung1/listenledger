import test from "node:test";
import assert from "node:assert/strict";
import {
  confirmReceipt,
  confirmTransaction,
  rejectReceipt,
  rejectTransaction,
  ApiError,
} from "../dist/api.js";

test("Review Queue Action Dispatches Suite", async (t) => {
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  await t.test("confirmTransaction dispatches POST /api/transactions/{id}/confirm without corrections", async () => {
    let capturedUrl = "";
    let capturedMethod = "";
    let capturedBody = null;

    globalThis.fetch = async (url, init) => {
      capturedUrl = String(url);
      capturedMethod = init?.method ?? "GET";
      capturedBody = init?.body ? JSON.parse(String(init.body)) : null;

      return {
        ok: true,
        status: 200,
        json: async () => ({
          id: "txn_rev_1",
          shop_id: "shop_1",
          type: "sale",
          product_name: "Milo 500g",
          quantity: 2,
          unit: "pack",
          amount: { amount: 400000, currency: "NGN" },
          confidence: { overall: 0.98 },
          status: "confirmed",
          occurred_at: "2026-10-14T10:00:00Z",
          created_at: "2026-10-14T10:00:00Z",
        }),
      };
    };

    const result = await confirmTransaction("txn_rev_1");

    assert.equal(capturedUrl, "/api/transactions/txn_rev_1/confirm");
    assert.equal(capturedMethod, "POST");
    assert.deepEqual(capturedBody, {});
    assert.equal(result.status, "confirmed");
    assert.equal(result.id, "txn_rev_1");
  });

  await t.test("confirmTransaction dispatches corrections payload when provided", async () => {
    let capturedUrl = "";
    let capturedBody = null;

    globalThis.fetch = async (url, init) => {
      capturedUrl = String(url);
      capturedBody = init?.body ? JSON.parse(String(init.body)) : null;

      return {
        ok: true,
        status: 200,
        json: async () => ({
          id: "txn_rev_2",
          shop_id: "shop_1",
          type: "sale",
          product_id: "prd_milo_corrected",
          product_name: "Milo Refill 400g",
          quantity: 3,
          unit: "pack",
          amount: { amount: 600000, currency: "NGN" },
          status: "confirmed",
          occurred_at: "2026-10-14T10:00:00Z",
          created_at: "2026-10-14T10:00:00Z",
        }),
      };
    };

    const corrections = {
      product_id: "prd_milo_corrected",
      quantity: 3,
      unit: "pack",
      amount: { amount: 600000, currency: "NGN" },
    };

    const result = await confirmTransaction("txn_rev_2", corrections);

    assert.equal(capturedUrl, "/api/transactions/txn_rev_2/confirm");
    assert.deepEqual(capturedBody, corrections);
    assert.equal(result.status, "confirmed");
    assert.equal(result.quantity, 3);
    assert.equal(result.product_id, "prd_milo_corrected");
  });

  await t.test("rejectTransaction dispatches POST /api/transactions/{id}/reject with notes", async () => {
    let capturedUrl = "";
    let capturedBody = null;

    globalThis.fetch = async (url, init) => {
      capturedUrl = String(url);
      capturedBody = init?.body ? JSON.parse(String(init.body)) : null;

      return {
        ok: true,
        status: 200,
        json: async () => ({
          id: "txn_rev_3",
          shop_id: "shop_1",
          type: "sale",
          status: "rejected",
          occurred_at: "2026-10-14T10:00:00Z",
          created_at: "2026-10-14T10:00:00Z",
        }),
      };
    };

    const result = await rejectTransaction("txn_rev_3", "Cashier test utterance");

    assert.equal(capturedUrl, "/api/transactions/txn_rev_3/reject");
    assert.deepEqual(capturedBody, { notes: "Cashier test utterance" });
    assert.equal(result.status, "rejected");
  });

  await t.test("confirmReceipt and rejectReceipt dispatch receipt-level endpoints", async () => {
    let capturedConfirmUrl = "";
    let capturedRejectUrl = "";
    let capturedRejectBody = null;

    globalThis.fetch = async (url, init) => {
      const urlStr = String(url);
      if (urlStr.includes("/confirm")) {
        capturedConfirmUrl = urlStr;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            receipt_id: "rcp_100",
            shop_id: "shop_1",
            transactions: [],
            total_count: 2,
            confirmed_count: 2,
            review_count: 0,
            rejected_count: 0,
          }),
        };
      } else {
        capturedRejectUrl = urlStr;
        capturedRejectBody = init?.body ? JSON.parse(String(init.body)) : null;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            receipt_id: "rcp_101",
            shop_id: "shop_1",
            transactions: [],
            total_count: 3,
            confirmed_count: 0,
            review_count: 0,
            rejected_count: 3,
          }),
        };
      }
    };

    const confirmRes = await confirmReceipt("rcp_100");
    assert.equal(capturedConfirmUrl, "/api/receipts/rcp_100/confirm");
    assert.equal(confirmRes.confirmed_count, 2);

    const rejectRes = await rejectReceipt("rcp_101", "Counter audio distorted");
    assert.equal(capturedRejectUrl, "/api/receipts/rcp_101/reject");
    assert.deepEqual(capturedRejectBody, { notes: "Counter audio distorted" });
    assert.equal(rejectRes.rejected_count, 3);
  });

  await t.test("API actions properly propagate ApiError on failure", async () => {
    globalThis.fetch = async () => ({
      ok: false,
      status: 409,
      text: async () => JSON.stringify({ error: "transaction already resolved" }),
    });

    await assert.rejects(
      async () => {
        await confirmTransaction("txn_already_resolved");
      },
      (err) => {
        assert(err instanceof ApiError);
        assert.equal(err.status, 409);
        assert.match(err.message, /confirm/);
        return true;
      },
    );
  });
});
