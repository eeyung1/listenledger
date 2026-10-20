import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountTransactions } from "../dist/views/transactions.js";

const html = fs.readFileSync(new URL("../src/components/transactions.html", import.meta.url), "utf8");
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const hook = (root, name) => root.querySelector(`[data-hook="${name}"]`);

const mockHeldTransactions = [
  {
    id: "txn_held_1",
    shop_id: "shop_f28_test",
    type: "sale",
    product_name: "Peak Milk Tin",
    quantity: 2,
    unit: "tin",
    amount: { amount: 180000, currency: "NGN" },
    confidence: { overall: 0.62, product: 0.58, quantity: 0.95, amount: 0.95 },
    status: "review_required",
    occurred_at: "2026-10-20T09:00:00Z",
    created_at: "2026-10-20T09:00:00Z",
    transcript: "two peak milk tin eighteen hundred",
  },
  {
    id: "txn_held_2",
    shop_id: "shop_f28_test",
    type: "sale",
    product_name: "Milo 500g",
    quantity: 1,
    unit: "pack",
    amount: { amount: 350000, currency: "NGN" },
    confidence: { overall: 0.55, product: 0.92, quantity: 0.60, amount: 0.52 },
    status: "review_required",
    occurred_at: "2026-10-20T09:05:00Z",
    created_at: "2026-10-20T09:05:00Z",
    transcript: "milo five hundred pack thirty five hundred",
  },
];

const mockLedgerTransactions = [
  {
    id: "txn_conf_1",
    shop_id: "shop_f28_test",
    type: "sale",
    product_name: "Indomie Onion",
    quantity: 5,
    unit: "pack",
    amount: { amount: 150000, currency: "NGN" },
    confidence: { overall: 0.98, product: 0.98, quantity: 0.98, amount: 0.98 },
    status: "confirmed",
    occurred_at: "2026-10-20T08:30:00Z",
    created_at: "2026-10-20T08:30:00Z",
    transcript: "five indomie onion fifteen hundred thank you",
  },
];

function setup() {
  const { document } = setupDOMEnvironment();
  const root = document.createElement("div");
  root.innerHTML = html;
  document.body.append(root);
  return root;
}

test("F28: Tab switching between All transactions and Needs review", async (t) => {
  const root = setup();
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });

  globalThis.fetch = async (url) => {
    const u = String(url);
    if (u.includes("status=review_required")) {
      return { ok: true, status: 200, json: async () => ({ transactions: mockHeldTransactions, total: 2 }) };
    }
    return { ok: true, status: 200, json: async () => ({ transactions: mockLedgerTransactions, total: 1 }) };
  };

  const cleanup = mountTransactions(root, "shop_f28_test");
  t.after(cleanup);
  await flush();

  const tabAll = hook(root, "tab-all-transactions");
  const tabReview = hook(root, "tab-needs-review");
  const tabBadge = hook(root, "review-tab-badge");
  const panelLedger = hook(root, "panel-ledger");
  const panelReview = hook(root, "panel-review");

  assert.ok(tabAll, "tab-all-transactions exists");
  assert.ok(tabReview, "tab-needs-review exists");
  assert.ok(panelLedger, "panel-ledger exists");
  assert.ok(panelReview, "panel-review exists");

  // Initial state: All transactions is active
  assert.equal(tabAll.classList.contains("is-active"), true);
  assert.equal(tabAll.getAttribute("aria-selected"), "true");
  assert.equal(tabReview.classList.contains("is-active"), false);
  assert.equal(tabReview.getAttribute("aria-selected"), "false");
  assert.equal(panelLedger.hidden, false);
  assert.equal(panelReview.hidden, true);

  // Review count badge in tab header reflects held transactions
  assert.equal(tabBadge.textContent, "2");
  assert.equal(tabBadge.hidden, false);

  // Switch to Needs review tab
  tabReview.dispatchEvent(new MockEvent("click"));
  await flush();

  assert.equal(tabReview.classList.contains("is-active"), true);
  assert.equal(tabReview.getAttribute("aria-selected"), "true");
  assert.equal(tabAll.classList.contains("is-active"), false);
  assert.equal(tabAll.getAttribute("aria-selected"), "false");
  assert.equal(panelReview.hidden, false);
  assert.equal(panelLedger.hidden, true);

  // Switch back to All transactions tab
  tabAll.dispatchEvent(new MockEvent("click"));
  await flush();

  assert.equal(tabAll.classList.contains("is-active"), true);
  assert.equal(tabAll.getAttribute("aria-selected"), "true");
  assert.equal(panelLedger.hidden, false);
  assert.equal(panelReview.hidden, true);
});

test("F28: Review cards highlight uncertain fields with badges and styling", async (t) => {
  const root = setup();
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });

  globalThis.fetch = async (url) => {
    const u = String(url);
    if (u.includes("status=review_required")) {
      return { ok: true, status: 200, json: async () => ({ transactions: mockHeldTransactions, total: 2 }) };
    }
    return { ok: true, status: 200, json: async () => ({ transactions: mockLedgerTransactions, total: 1 }) };
  };

  const cleanup = mountTransactions(root, "shop_f28_test");
  t.after(cleanup);
  await flush();

  const cards = Array.from(root.querySelectorAll(".review-card"));
  assert.equal(cards.length, 2);

  // Find Peak Milk card (has product confidence 0.58 -> uncertain product)
  const peakCard = cards.find((c) => c.querySelector(".review-card__product")?.textContent?.includes("Peak Milk"));
  assert.ok(peakCard, "Peak Milk review card found");
  const peakProd = peakCard.querySelector(".review-card__product");
  assert.ok(peakProd.classList.contains("review-card__field--uncertain"), "Uncertain product has uncertain class");
  assert.ok(hook(peakCard, "uncertain-field-product"), "Uncertain product badge is rendered");
  assert.match(hook(peakCard, "uncertain-field-product").textContent, /58%/);

  // Find Milo card (has quantity 0.60 and amount 0.52 -> uncertain quantity and amount)
  const miloCard = cards.find((c) => c.querySelector(".review-card__product")?.textContent?.includes("Milo"));
  assert.ok(miloCard, "Milo review card found");
  assert.ok(hook(miloCard, "uncertain-field-quantity"), "Uncertain quantity badge is rendered");
  assert.match(hook(miloCard, "uncertain-field-quantity").textContent, /60%/);
  assert.ok(hook(miloCard, "uncertain-field-amount"), "Uncertain amount badge is rendered");
  assert.match(hook(miloCard, "uncertain-field-amount").textContent, /52%/);
});

test("F28: Review cards and panel explain confirm and reject consequences", async (t) => {
  const root = setup();
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });

  globalThis.fetch = async (url) => {
    const u = String(url);
    if (u.includes("status=review_required")) {
      return { ok: true, status: 200, json: async () => ({ transactions: mockHeldTransactions, total: 2 }) };
    }
    return { ok: true, status: 200, json: async () => ({ transactions: mockLedgerTransactions, total: 1 }) };
  };

  const cleanup = mountTransactions(root, "shop_f28_test");
  t.after(cleanup);
  await flush();

  // Panel hint explains consequences
  const panelHint = hook(root, "review-panel-hint");
  assert.ok(panelHint);
  assert.match(panelHint.textContent, /Confirming.*applies.*stock/i);
  assert.match(panelHint.textContent, /rejecting.*discards.*without changing inventory/i);

  // Card consequences element explains confirm vs reject consequences
  const cards = root.querySelectorAll(".review-card");
  for (const card of cards) {
    const cons = hook(card, "review-consequences");
    assert.ok(cons, "Consequences explanation element exists on each review card");
    assert.match(cons.textContent, /Confirm:.*Applies stock change/i);
    assert.match(cons.textContent, /Reject:.*Discards capture without modifying inventory/i);

    const confirmBtn = hook(card, "review-confirm-btn");
    const rejectBtn = hook(card, "review-reject-btn");
    assert.ok(confirmBtn, "review-confirm-btn exists");
    assert.ok(rejectBtn, "review-reject-btn exists");
    assert.ok(confirmBtn.getAttribute("title").includes("inventory"), "Confirm button explains inventory consequence");
    assert.ok(rejectBtn.getAttribute("title").includes("without altering stock"), "Reject button explains stock consequence");
  }
});
