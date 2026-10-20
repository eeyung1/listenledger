import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountInventory } from "../dist/views/inventory.js";
import { mountTransactions } from "../dist/views/transactions.js";

const INVENTORY_HTML = fs.readFileSync(
  new URL("../src/components/inventory.html", import.meta.url),
  "utf8"
);
const TRANSACTIONS_HTML = fs.readFileSync(
  new URL("../src/components/transactions.html", import.meta.url),
  "utf8"
);

test("UI End-to-End Acceptance Suite: Catalogue, Reviews, Manual Fallback & Reversals", async (t) => {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  await t.test("1. Catalogue Management: Add, Edit, Stock Audit, Archiving & Restoring", async () => {
    let mockProducts = [
      {
        id: "prd_milo_1",
        shop_id: "shop_acc_1",
        sku: "MILO-500",
        name: "Milo 500g",
        price: { amount: 250000, currency: "NGN" },
        quantity: 15,
        base_unit: "pack",
        aliases: ["milo", "chocolate drink"],
        packaging: [{ from: "carton", to: "pack", factor: 12 }],
        low_stock_threshold: 5,
        archived_at: null,
      },
    ];

    let lastCreatedInput = null;
    let lastUpdatedInput = null;
    let lastAdjustedInput = null;
    let lastArchivedId = null;
    let lastUnarchivedId = null;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/inventory")) {
        const isArchivedQuery = u.includes("status=archived");
        const isAllQuery = u.includes("status=all");
        let filtered = mockProducts;
        if (isArchivedQuery) {
          filtered = mockProducts.filter((p) => p.archived_at !== null);
        } else if (!isAllQuery) {
          filtered = mockProducts.filter((p) => p.archived_at === null);
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: filtered,
            count: filtered.length,
            low_stock_count: filtered.filter((p) => p.quantity <= p.low_stock_threshold).length,
          }),
        };
      }

      if (u.endsWith("/api/products") && method === "POST") {
        lastCreatedInput = JSON.parse(String(init.body));
        const newProd = {
          id: "prd_indomie_1",
          shop_id: "shop_acc_1",
          sku: lastCreatedInput.sku,
          name: lastCreatedInput.name,
          price: lastCreatedInput.price,
          quantity: lastCreatedInput.quantity,
          base_unit: lastCreatedInput.base_unit,
          aliases: lastCreatedInput.aliases,
          packaging: [],
          low_stock_threshold: lastCreatedInput.low_stock_threshold,
          archived_at: null,
        };
        mockProducts.push(newProd);
        return { ok: true, status: 201, json: async () => newProd };
      }

      if (u.endsWith("/api/products/prd_milo_1") && method === "PATCH") {
        lastUpdatedInput = JSON.parse(String(init.body));
        const p = mockProducts.find((p) => p.id === "prd_milo_1");
        Object.assign(p, {
          price: lastUpdatedInput.price,
          low_stock_threshold: lastUpdatedInput.low_stock_threshold,
        });
        return { ok: true, status: 200, json: async () => p };
      }

      if (u.endsWith("/api/products/prd_milo_1/adjust") && method === "POST") {
        lastAdjustedInput = JSON.parse(String(init.body));
        const p = mockProducts.find((p) => p.id === "prd_milo_1");
        // 2 cartons of 12 = +24 packs
        p.quantity += 24;
        return { ok: true, status: 200, json: async () => p };
      }

      if (u.endsWith("/api/products/prd_milo_1/archive") && method === "POST") {
        lastArchivedId = "prd_milo_1";
        const p = mockProducts.find((p) => p.id === "prd_milo_1");
        p.archived_at = "2026-10-14T12:00:00Z";
        return { ok: true, status: 200, json: async () => p };
      }

      if (u.endsWith("/api/products/prd_milo_1/unarchive") && method === "POST") {
        lastUnarchivedId = "prd_milo_1";
        const p = mockProducts.find((p) => p.id === "prd_milo_1");
        p.archived_at = null;
        return { ok: true, status: 200, json: async () => p };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    // Mount view into #app
    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = INVENTORY_HTML;
    document.body.append(root);

    const cleanup = mountInventory(root, "shop_acc_1");

    // Wait for initial load
    await new Promise((r) => setTimeout(r, 20));

    const countLabel = root.querySelector('[data-hook="inventory-count"]');
    assert.equal(countLabel.textContent, "1");

    const tbody = root.querySelector('[data-hook="catalogue-body"]');
    assert.equal(tbody.children.length, 1);
    const row1 = tbody.children[0];
    assert.match(row1.textContent, /Milo 500g/);

    // --- Sub-workflow A: Add Product Modal ---
    const addBtn = root.querySelector('[data-hook="catalogue-add"]');
    assert.ok(addBtn);
    addBtn.dispatchEvent(new MockEvent("click"));

    const modalDialog = root.querySelector(".modal-dialog");
    assert.ok(modalDialog, "Add Product modal opened");

    const nameInput = modalDialog.querySelector('input[type="text"]');
    const skuInput = modalDialog.querySelectorAll('input[type="text"]')[1];
    const priceInput = modalDialog.querySelector('input[type="number"]');
    const form = modalDialog.querySelector("form");

    nameInput.value = "Indomie Instant Noodles";
    skuInput.value = "IND-NOODLE";
    priceInput.value = "350.00"; // NGN 350.00 -> 35,000 kobo

    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastCreatedInput.sku, "IND-NOODLE");
    assert.equal(lastCreatedInput.price.amount, 35000);
    assert.equal(countLabel.textContent, "2");
    assert.equal(tbody.children.length, 2);

    // --- Sub-workflow B: Edit Product Modal ---
    const editBtn = row1.querySelector(".button--secondary");
    assert.ok(editBtn);
    editBtn.dispatchEvent(new MockEvent("click"));

    const editDialog = root.querySelector(".modal-dialog");
    assert.ok(editDialog, "Edit Product modal opened");
    const editPriceInput = editDialog.querySelector('input[type="number"]');
    editPriceInput.value = "280.00"; // NGN 280 -> 28,000 kobo
    const editForm = editDialog.querySelector("form");
    editForm.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastUpdatedInput.price.amount, 28000);

    // --- Sub-workflow C: Stock Count Audit Modal ---
    const auditBtn = row1.querySelectorAll(".button--secondary")[1];
    assert.equal(auditBtn.textContent, "Audit");
    auditBtn.dispatchEvent(new MockEvent("click"));

    const auditDialog = document.body.querySelector(".modal-dialog");
    assert.ok(auditDialog, "Stock Count Audit modal opened");
    const auditQty = auditDialog.querySelector('input[type="number"]');
    auditQty.value = "2";
    const auditReason = auditDialog.querySelector('input[placeholder*="Physical inventory count"]');
    auditReason.value = "Physical inventory count verified with storekeeper";
    const auditForm = auditDialog.querySelector("form");
    auditForm.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastAdjustedInput.quantity, 2);
    assert.equal(lastAdjustedInput.reason, "Physical inventory count verified with storekeeper");

    // --- Sub-workflow D: Archive and Restore ---
    const archiveBtn = row1.querySelectorAll(".button--secondary")[3];
    assert.equal(archiveBtn.textContent, "Archive");
    archiveBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastArchivedId, "prd_milo_1");
    // Under active tab, Milo is now removed
    assert.equal(tbody.children.length, 1);
    assert.match(tbody.children[0].textContent, /Indomie Instant Noodles/);

    cleanup();
  });

  await t.test("2. Review Queue: Confirm, Edit & Confirm Corrections, and Rejection", async () => {
    let mockHeld = [
      {
        id: "txn_rev_clean",
        shop_id: "shop_acc_1",
        type: "sale",
        product_name: "Milo 500g",
        product_id: "prd_milo_1",
        quantity: 1,
        unit: "pack",
        amount: { amount: 250000, currency: "NGN" },
        confidence: { overall: 0.88, product: 0.85, quantity: 0.95, amount: 0.9 },
        status: "review_required",
        transcript: "Customer bought one pack of Milo",
        has_audio: true,
        occurred_at: "2026-10-14T10:10:00Z",
        created_at: "2026-10-14T10:10:00Z",
      },
      {
        id: "txn_rev_edit",
        shop_id: "shop_acc_1",
        type: "restock",
        product_name: "Uncertain Item",
        product_id: null,
        quantity: 1,
        unit: "pack",
        amount: { amount: 100000, currency: "NGN" },
        confidence: { overall: 0.52, product: 0.4, quantity: 0.9, amount: 0.7 },
        status: "review_required",
        transcript: "Supplier dropped two cartons of Indomie",
        occurred_at: "2026-10-14T10:05:00Z",
        created_at: "2026-10-14T10:05:00Z",
      },
      {
        id: "txn_rev_reject",
        shop_id: "shop_acc_1",
        type: "sale",
        product_name: "Unknown chatter",
        quantity: 1,
        unit: "pack",
        amount: { amount: 50000, currency: "NGN" },
        confidence: { overall: 0.3, product: 0.2, quantity: 0.5, amount: 0.4 },
        status: "review_required",
        transcript: "Can you pass the key",
        occurred_at: "2026-10-14T10:00:00Z",
        created_at: "2026-10-14T10:00:00Z",
      },
    ];

    let mockLedger = [];
    let lastCorrectionPayload = null;
    let lastRejectionNotes = null;
    let audioDetailFetches = 0;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/transactions/txn_rev_clean?") && method === "GET") {
        audioDetailFetches += 1;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            id: "txn_rev_clean",
            has_audio: true,
            audio_data: "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=",
          }),
        };
      }

      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [
              {
                id: "prd_indomie_1",
                sku: "IND-1",
                name: "Indomie Onion",
                price: { amount: 350000, currency: "NGN" },
                base_unit: "pack",
                quantity: 100,
              },
            ],
            count: 1,
          }),
        };
      }

      if (u.includes("/api/transactions") && method === "GET") {
        if (u.includes("status=review_required")) {
          return {
            ok: true,
            status: 200,
            json: async () => ({ transactions: mockHeld, count: mockHeld.length, total: mockHeld.length }),
          };
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({ transactions: mockLedger, count: mockLedger.length, total: mockLedger.length }),
        };
      }

      if (u.endsWith("/api/transactions/txn_rev_clean/confirm") && method === "POST") {
        const item = mockHeld.find((t) => t.id === "txn_rev_clean");
        mockHeld = mockHeld.filter((t) => t.id !== "txn_rev_clean");
        const confirmed = { ...item, status: "confirmed" };
        mockLedger.unshift(confirmed);
        return { ok: true, status: 200, json: async () => confirmed };
      }

      if (u.endsWith("/api/transactions/txn_rev_edit/confirm") && method === "POST") {
        lastCorrectionPayload = JSON.parse(String(init.body));
        const item = mockHeld.find((t) => t.id === "txn_rev_edit");
        mockHeld = mockHeld.filter((t) => t.id !== "txn_rev_edit");
        const confirmed = {
          ...item,
          product_name: "Indomie Onion",
          product_id: lastCorrectionPayload.product_id,
          quantity: lastCorrectionPayload.quantity,
          unit: lastCorrectionPayload.unit,
          amount: lastCorrectionPayload.amount,
          status: "confirmed",
        };
        mockLedger.unshift(confirmed);
        return { ok: true, status: 200, json: async () => confirmed };
      }

      if (u.endsWith("/api/transactions/txn_rev_reject/reject") && method === "POST") {
        const body = JSON.parse(String(init.body));
        lastRejectionNotes = body.notes;
        const item = mockHeld.find((t) => t.id === "txn_rev_reject");
        mockHeld = mockHeld.filter((t) => t.id !== "txn_rev_reject");
        const rejected = { ...item, status: "rejected" };
        mockLedger.unshift(rejected);
        return { ok: true, status: 200, json: async () => rejected };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = TRANSACTIONS_HTML;
    document.body.append(root);

    const cleanup = mountTransactions(root, "shop_acc_1");
    await new Promise((r) => setTimeout(r, 20));

    const queueCountBadge = root.querySelector('[data-hook="review-queue-count"]');
    assert.equal(queueCountBadge.textContent, "3");

    const reviewCards = root.querySelectorAll(".review-card");
    assert.equal(reviewCards.length, 3);

    // --- Action A: Quick Confirm ---
    const cleanCard = reviewCards[0];
    const audioWrapper = cleanCard.querySelector(".review-card__audio");
    assert.notEqual(audioWrapper, null, "cleanCard with has_audio must render an audio control");
    const playAudioBtn = audioWrapper.querySelector("button");
    assert.notEqual(playAudioBtn, null, "audio control must be a button");
    assert.match(playAudioBtn.textContent, /Play audio/);
    assert.equal(audioDetailFetches, 0, "rendering the list must not fetch the snippet");
    playAudioBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(audioDetailFetches, 1, "requesting playback must fetch the shop-scoped detail snippet");

    // Second review card without has_audio must not render an audio control
    assert.equal(reviewCards[1].querySelector(".review-card__audio"), null);

    const confirmBtn = cleanCard.querySelector(".button--primary");
    assert.equal(confirmBtn.textContent, "Confirm");
    confirmBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(queueCountBadge.textContent, "2");
    const ledgerRows = root.querySelectorAll('[data-hook="ledger-body"] tr');
    assert.equal(ledgerRows.length, 1);
    assert.match(ledgerRows[0].textContent, /Milo 500g/);

    const replayBtn = ledgerRows[0].querySelector(".button--audio-replay");
    assert.notEqual(replayBtn, null, "ledger row must render audio replay button for transaction with audio snippet");
    assert.ok(replayBtn.textContent.includes("Audio"));

    // --- Action B: Edit & Confirm with Corrections ---
    const editCard = root.querySelectorAll(".review-card")[0]; // now first
    const editBtn = editCard.querySelectorAll(".button--secondary")[0];
    assert.equal(editBtn.textContent, "Edit & Confirm");
    editBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    const editDialog = document.body.querySelector(".modal-dialog");
    assert.ok(editDialog, "Edit & Confirm modal opened");

    const productSelect = editDialog.querySelector("select");
    const qtyInput = editDialog.querySelector('input[type="number"]');
    const unitSelect = editDialog.querySelectorAll("select")[1];
    const amountInput = editDialog.querySelectorAll('input[type="number"]')[1];
    const notesInput = editDialog.querySelector('input[type="text"]');

    productSelect.value = "prd_indomie_1";
    qtyInput.value = "2";
    unitSelect.value = "carton";
    amountInput.value = "700000"; // NGN 7,000
    notesInput.value = "Corrected Indomie supplier restock";

    const aliasCheckbox = editDialog.querySelector("#save-as-alias-checkbox");
    assert.ok(aliasCheckbox, "Save as alias checkbox rendered");
    aliasCheckbox.checked = true;

    const editForm = editDialog.querySelector("form");
    editForm.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastCorrectionPayload.product_id, "prd_indomie_1");
    assert.equal(lastCorrectionPayload.quantity, 2);
    assert.equal(lastCorrectionPayload.unit, "carton");
    assert.equal(lastCorrectionPayload.amount.amount, 700000);
    assert.equal(lastCorrectionPayload.notes, "Corrected Indomie supplier restock");
    assert.equal(lastCorrectionPayload.save_as_alias, true);
    assert.equal(lastCorrectionPayload.alias_text, "Uncertain Item");

    assert.equal(queueCountBadge.textContent, "1");

    // --- Action C: Reject Review Card ---
    window.prompt = () => "Non-transactional ambient conversation";
    const rejectCard = root.querySelectorAll(".review-card")[0];
    const rejectBtn = rejectCard.querySelectorAll(".button--secondary")[1];
    assert.equal(rejectBtn.textContent, "Reject");
    rejectBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastRejectionNotes, "Non-transactional ambient conversation");
    assert.equal(queueCountBadge.textContent, "0");

    cleanup();
  });

  await t.test("3. Manual Counter Fallback and Void / Reversal Actions", async () => {
    let mockLedger = [
      {
        id: "txn_to_void",
        shop_id: "shop_acc_1",
        type: "sale",
        product_name: "Milo 500g",
        product_id: "prd_milo_1",
        quantity: 1,
        unit: "pack",
        amount: { amount: 250000, currency: "NGN" },
        confidence: { overall: 1.0 },
        status: "confirmed",
        occurred_at: "2026-10-14T11:00:00Z",
        created_at: "2026-10-14T11:00:00Z",
      },
    ];

    let lastManualInput = null;
    let lastManualHeaders = null;
    let lastVoidedId = null;
    let lastVoidReason = null;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [
              {
                id: "prd_milo_1",
                sku: "MILO-500",
                name: "Milo 500g",
                price: { amount: 250000, currency: "NGN" },
                base_unit: "pack",
                quantity: 50,
              },
            ],
            count: 1,
          }),
        };
      }

      if (u.includes("/api/transactions") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({ transactions: mockLedger, count: mockLedger.length, total: mockLedger.length }),
        };
      }

      if (u.endsWith("/api/transactions/manual") && method === "POST") {
        lastManualInput = JSON.parse(String(init.body));
        lastManualHeaders = init.headers;
        const newTxn = {
          id: "txn_manual_1",
          shop_id: "shop_acc_1",
          type: lastManualInput.type,
          product_name: "Milo 500g",
          product_id: lastManualInput.product_id,
          quantity: lastManualInput.quantity,
          unit: lastManualInput.unit,
          amount: lastManualInput.amount || { amount: 250000, currency: "NGN" },
          confidence: { overall: 1.0 },
          status: "confirmed",
          occurred_at: "2026-10-14T11:15:00Z",
          created_at: "2026-10-14T11:15:00Z",
        };
        mockLedger.unshift(newTxn);
        return { ok: true, status: 201, json: async () => newTxn };
      }

      if (u.includes("/reverse") && method === "POST") {
        const idMatch = u.match(/\/api\/transactions\/([^/]+)\/reverse/);
        lastVoidedId = idMatch ? idMatch[1] : null;
        const body = JSON.parse(String(init.body));
        lastVoidReason = body.reason;
        const item = mockLedger.find((t) => t.id === lastVoidedId) || mockLedger[0];
        item.status = "cancelled";
        return {
          ok: true,
          status: 200,
          json: async () => ({
            transaction: item,
            reversal: { ...item, id: "txn_reversal_1", status: "confirmed" },
          }),
        };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = TRANSACTIONS_HTML;
    document.body.append(root);

    const cleanup = mountTransactions(root, "shop_acc_1");
    await new Promise((r) => setTimeout(r, 20));

    // --- Action A: Manual Entry Fallback ---
    const manualBtn = root.querySelector('[data-hook="manual-entry"]');
    assert.ok(manualBtn);
    manualBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    const modalDialog = document.body.querySelector(".modal-dialog");
    assert.ok(modalDialog, "Manual entry modal opened");

    const typeSelect = modalDialog.querySelector("select");
    typeSelect.value = "sale";

    const prodSelect = modalDialog.querySelectorAll("select")[1];
    prodSelect.value = "prd_milo_1";

    const qtyInput = modalDialog.querySelector('input[type="number"]');
    qtyInput.value = "3";

    const unitSelect = modalDialog.querySelectorAll("select")[2];
    unitSelect.value = "pack";

    const notesInput = modalDialog.querySelector('input[placeholder*="Customer spoke quietly"]');
    notesInput.value = "Loud generator, entered manually";

    const form = modalDialog.querySelector("form");
    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(lastManualInput.type, "sale");
    assert.equal(lastManualInput.product_id, "prd_milo_1");
    assert.equal(lastManualInput.quantity, 3);
    assert.equal(lastManualInput.unit, "pack");
    assert.equal(lastManualInput.notes, "Loud generator, entered manually");
    assert.equal(typeof lastManualInput.request_id, "string");
    assert.ok(lastManualInput.request_id.length > 0, "manual entry carries a stable request id");
    assert.equal(
      lastManualHeaders["Idempotency-Key"],
      lastManualInput.request_id,
      "the request id is sent as the idempotency key so a retry can be deduplicated",
    );

    // --- Action B: Void / Reversal of Confirmed Transaction ---
    window.prompt = () => "Customer returned unopened merchandise";
    const ledgerTable = root.querySelector('[data-hook="ledger-body"]');
    const voidBtn = ledgerTable.querySelector(".button--secondary");
    assert.equal(voidBtn.textContent, "Void");
    voidBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.ok(lastVoidedId === "txn_manual_1" || lastVoidedId === "txn_to_void");
    assert.equal(lastVoidReason, "Customer returned unopened merchandise");

    cleanup();
  });
});
