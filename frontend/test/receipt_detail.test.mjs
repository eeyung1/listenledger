import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountTransactions } from "../dist/views/transactions.js";
import { formatMoney } from "../dist/format.js";

const TRANSACTIONS_HTML = fs.readFileSync(
  new URL("../src/components/transactions.html", import.meta.url),
  "utf8",
);

const RECEIPT_ID = "rcp_9";
const tick = () => new Promise((resolve) => setTimeout(resolve, 20));

const sale = {
  id: "txn_sale",
  shop_id: "shop_1",
  type: "sale",
  product_name: "Milo 500g",
  quantity: 2,
  unit: "pack",
  amount: { amount: 400000, currency: "NGN" },
  confidence: { overall: 0.97, product: 0.99, quantity: 0.96, amount: 0.98 },
  status: "confirmed",
  receipt_id: RECEIPT_ID,
  transcript: "two milo",
  occurred_at: "2026-10-14T10:00:00Z",
  created_at: "2026-10-14T10:00:00Z",
};

const held = {
  id: "txn_held",
  shop_id: "shop_1",
  type: "sale",
  product_name: "Rice 5kg",
  quantity: 1,
  unit: "bag",
  amount: { amount: 150000, currency: "NGN" },
  confidence: { overall: 0.55, product: 0.55, quantity: 0.98, amount: 0.97 },
  status: "review_required",
  receipt_id: RECEIPT_ID,
  transcript: "one rice",
  occurred_at: "2026-10-14T10:00:00Z",
  created_at: "2026-10-14T10:00:00Z",
};

/** Mirrors the server: only confirmed movements count toward the total. */
function receiptGroup({ saleStatus = "confirmed", heldStatus = "review_required" } = {}) {
  const transactions = [
    { ...sale, status: saleStatus },
    { ...held, status: heldStatus },
  ];
  const count = (status) => transactions.filter((t) => t.status === status).length;
  const totalAmount = transactions
    .filter((t) => t.status === "confirmed")
    .reduce((sum, t) => sum + t.amount.amount, 0);
  return {
    receipt_id: RECEIPT_ID,
    shop_id: "shop_1",
    transactions,
    total_count: transactions.length,
    confirmed_count: count("confirmed"),
    review_count: count("review_required"),
    rejected_count: count("rejected"),
    total_amount: { amount: totalAmount, currency: "NGN" },
  };
}

function jsonResponse(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function mountHarness() {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;
  const ledgerRequests = [];
  const state = { receiptFails: false, receiptCalls: 0, decided: null };

  globalThis.fetch = async (url, init) => {
    const parsed = new URL(String(url), "http://localhost");
    const method = init?.method ?? "GET";

    if (parsed.pathname === "/api/transactions" && method === "GET") {
      if (parsed.searchParams.get("status") === "review_required") {
        return jsonResponse({ transactions: [held], count: 1, total: 1 });
      }
      ledgerRequests.push(parsed);
      return jsonResponse({ transactions: [sale], count: 1, total: 1 });
    }

    if (parsed.pathname === `/api/receipts/${RECEIPT_ID}` && method === "GET") {
      state.receiptCalls += 1;
      if (state.receiptFails) return jsonResponse({ error: "receipt unavailable" }, 503);
      return jsonResponse(receiptGroup());
    }

    if (parsed.pathname === `/api/receipts/${RECEIPT_ID}/confirm` && method === "POST") {
      state.decided = "confirm";
      return jsonResponse(receiptGroup({ heldStatus: "confirmed" }));
    }

    if (parsed.pathname === `/api/receipts/${RECEIPT_ID}/reject` && method === "POST") {
      state.decided = "reject";
      return jsonResponse(receiptGroup({ heldStatus: "rejected" }));
    }

    return jsonResponse({ error: "not found" }, 404);
  };

  const root = document.createElement("div");
  root.id = "app";
  root.innerHTML = TRANSACTIONS_HTML;
  document.body.append(root);

  return {
    document,
    window,
    root,
    state,
    ledgerRequests,
    cleanup: () => {
      globalThis.fetch = originalFetch;
      document.body.replaceChildren();
    },
  };
}

function buttonByText(scope, text) {
  return scope
    .querySelectorAll("button")
    .find((button) => button.textContent.trim() === text);
}

async function openReceipt(h) {
  const badge = h.root.querySelectorAll("button").find((b) => b.textContent.trim() === "Receipt");
  assert.notEqual(badge, undefined, "a ledger row with a receipt must offer a receipt control");
  badge.dispatchEvent(new MockEvent("click"));
  await tick();
  return badge;
}

test("Receipt detail suite", async (t) => {
  await t.test("the ledger receipt control is a button with an accessible name", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const badge = h.root.querySelectorAll("button").find((b) => b.textContent.trim() === "Receipt");
      assert.equal(badge.tagName, "BUTTON");
      assert.equal(badge.getAttribute("aria-label"), `Open receipt ${RECEIPT_ID}`);
      // Opening the receipt must not narrow the ledger on its own: filtering is
      // a decision the owner makes inside the panel.
      assert.equal(
        h.ledgerRequests.every((u) => u.searchParams.get("receipt_id") === null),
        true,
      );
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("opening a receipt lists every movement, the stored total and the status counts", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      await openReceipt(h);

      const dialog = h.document.body.querySelector('[role="dialog"]');
      assert.notEqual(dialog, null, "the receipt must open as a dialog");
      const text = dialog.textContent;
      assert.match(text, /Receipt rcp_9/);
      assert.match(text, /Milo 500g/);
      assert.match(text, /Rice 5kg/, "held items belong to the receipt too");
      assert.match(text, new RegExp(formatMoney({ amount: 400000, currency: "NGN" }).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
      assert.match(text, /2 movements: 1 confirmed, 1 needs review, 0 rejected/);
      assert.match(text, /Heard: two milo \/ one rice/);
      assert.match(text, /rejecting discards them without changing stock/i);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("a receipt's held items can be confirmed together", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      assert.equal(h.root.querySelector('[data-hook="review-queue-count"]').textContent, "1");
      await openReceipt(h);

      const confirmAll = buttonByText(h.document.body, "Confirm all 1 held item");
      assert.notEqual(confirmAll, undefined, "the panel must offer the whole-receipt decision");
      confirmAll.dispatchEvent(new MockEvent("click"));
      await tick();

      assert.equal(h.state.decided, "confirm", "the receipt endpoint is the unit of decision");
      assert.equal(
        h.root.querySelector('[data-hook="review-queue-count"]').textContent,
        "0",
        "a confirmed item must leave the review queue",
      );
      const dialog = h.document.body.querySelector('[role="dialog"]');
      assert.match(dialog.textContent, /2 confirmed, 0 needs review, 0 rejected/);
      assert.equal(
        dialog.querySelectorAll("tr").length,
        3,
        "both movements stay listed once decided (one header row plus two movements)",
      );
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("a receipt that cannot load explains itself and can be retried", async () => {
    const h = mountHarness();
    try {
      h.state.receiptFails = true;
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      await openReceipt(h);

      const dialog = h.document.body.querySelector('[role="dialog"]');
      assert.match(dialog.textContent, /Could not load this receipt/);
      const retry = buttonByText(dialog, "Retry");
      assert.notEqual(retry, undefined);

      h.state.receiptFails = false;
      retry.dispatchEvent(new MockEvent("click"));
      await tick();

      assert.equal(h.state.receiptCalls, 2);
      assert.match(h.document.body.querySelector('[role="dialog"]').textContent, /Rice 5kg/);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("filtering the ledger to the receipt stays an explicit action", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      await openReceipt(h);

      const filterBtn = buttonByText(h.document.body, "Filter ledger to this receipt");
      assert.notEqual(filterBtn, undefined);
      filterBtn.dispatchEvent(new MockEvent("click"));
      await tick();

      assert.equal(h.document.body.querySelector('[role="dialog"]'), null, "the panel closes");
      const last = h.ledgerRequests.at(-1);
      assert.equal(last.searchParams.get("receipt_id"), RECEIPT_ID);
      assert.equal(last.searchParams.get("offset"), "0");
      cleanup();
    } finally {
      h.cleanup();
    }
  });
});
