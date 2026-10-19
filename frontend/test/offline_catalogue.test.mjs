import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  setCachedCatalogue,
  getCachedCatalogue,
  getCachedCatalogueRecord,
  clearCachedCatalogue,
  getActiveCatalogueDriver,
} from "../dist/offline_catalogue.js";
import {
  enqueueManualTransaction,
  getQueuedCaptures,
  getQueuedCount,
  clearQueue,
  syncOfflineQueue,
  setActiveQueueShop,
} from "../dist/offline_queue.js";
import { mountTransactions } from "../dist/views/transactions.js";
import { mountHome } from "../dist/views/home.js";
import { mountInventory } from "../dist/views/inventory.js";

const TRANSACTIONS_HTML = fs.readFileSync(
  new URL("../src/components/transactions.html", import.meta.url),
  "utf8"
);
const HOME_HTML = fs.readFileSync(
  new URL("../src/components/home.html", import.meta.url),
  "utf8"
);
const INVENTORY_HTML = fs.readFileSync(
  new URL("../src/components/inventory.html", import.meta.url),
  "utf8"
);

const MOCK_MILO = {
  id: "prd_milo_1",
  shop_id: "shop_alpha",
  sku: "MILO-500",
  name: "Milo 500g",
  price: { amount: 250000, currency: "NGN" },
  quantity: 20,
  base_unit: "pack",
  aliases: ["milo", "choco"],
  packaging: [{ from: "carton", to: "pack", factor: 12 }],
  low_stock_threshold: 5,
  archived_at: null,
};

const MOCK_PEAK = {
  id: "prd_peak_1",
  shop_id: "shop_alpha",
  sku: "PEAK-400",
  name: "Peak Milk 400g",
  price: { amount: 350000, currency: "NGN" },
  quantity: 15,
  base_unit: "tin",
  aliases: ["peak milk"],
  packaging: [{ from: "carton", to: "tin", factor: 24 }],
  low_stock_threshold: 4,
  archived_at: null,
};

const MOCK_INDOMIE_BETA = {
  id: "prd_indomie_1",
  shop_id: "shop_beta",
  sku: "INDO-SUP",
  name: "Indomie Super Pack",
  price: { amount: 150000, currency: "NGN" },
  quantity: 50,
  base_unit: "pack",
  aliases: ["indomie"],
  packaging: [{ from: "carton", to: "pack", factor: 40 }],
  low_stock_threshold: 10,
  archived_at: null,
};

test("P4.8 Offline Product Catalogue & Resilient Counter Entry Suite", async (t) => {
  setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.beforeEach(async () => {
    await clearCachedCatalogue();
    await clearQueue();
    setActiveQueueShop(null);
    globalThis.fetch = originalFetch;
    if (typeof navigator !== "undefined") {
      navigator.onLine = true;
    }
  });

  t.afterEach(async () => {
    await clearCachedCatalogue();
    await clearQueue();
    setActiveQueueShop(null);
    globalThis.fetch = originalFetch;
    if (typeof navigator !== "undefined") {
      navigator.onLine = true;
    }
    if (document.body) {
      document.body.replaceChildren();
    }
  });

  await t.test("1. Basic Caching: set, get, and metadata timestamps", async () => {
    const products = [MOCK_MILO, MOCK_PEAK];
    const record = await setCachedCatalogue("shop_alpha", products);

    assert.equal(record.shop_id, "shop_alpha");
    assert.equal(record.products.length, 2);
    assert.ok(record.cached_at);
    assert.ok(new Date(record.cached_at).getTime() > 0);

    const retrieved = await getCachedCatalogue("shop_alpha");
    assert.equal(retrieved.length, 2);
    assert.equal(retrieved[0].name, "Milo 500g");
    assert.equal(retrieved[1].name, "Peak Milk 400g");

    const fullRecord = await getCachedCatalogueRecord("shop_alpha");
    assert.ok(fullRecord);
    assert.equal(fullRecord.shop_id, "shop_alpha");
    assert.equal(fullRecord.products.length, 2);
  });

  await t.test("2. Multi-Shop Isolation: Caches never cross-contaminate between shops", async () => {
    await setCachedCatalogue("shop_alpha", [MOCK_MILO]);
    await setCachedCatalogue("shop_beta", [MOCK_INDOMIE_BETA]);

    const alphaProducts = await getCachedCatalogue("shop_alpha");
    assert.equal(alphaProducts.length, 1);
    assert.equal(alphaProducts[0].id, "prd_milo_1");

    const betaProducts = await getCachedCatalogue("shop_beta");
    assert.equal(betaProducts.length, 1);
    assert.equal(betaProducts[0].id, "prd_indomie_1");

    const gammaProducts = await getCachedCatalogue("shop_gamma");
    assert.deepEqual(gammaProducts, []);

    // Clear shop_alpha only
    await clearCachedCatalogue("shop_alpha");
    assert.deepEqual(await getCachedCatalogue("shop_alpha"), []);
    // shop_beta is preserved intact
    const betaStillThere = await getCachedCatalogue("shop_beta");
    assert.equal(betaStillThere.length, 1);
    assert.equal(betaStillThere[0].id, "prd_indomie_1");
  });

  await t.test("3. Clear All: Purges cached catalogues across all shops", async () => {
    await setCachedCatalogue("shop_alpha", [MOCK_MILO]);
    await setCachedCatalogue("shop_beta", [MOCK_INDOMIE_BETA]);

    await clearCachedCatalogue();

    assert.deepEqual(await getCachedCatalogue("shop_alpha"), []);
    assert.deepEqual(await getCachedCatalogue("shop_beta"), []);
  });

  await t.test("4. Storage Driver Fallback: Detects driver gracefully", () => {
    const driver = getActiveCatalogueDriver();
    assert.ok(["indexeddb", "localstorage", "memory"].includes(driver));
  });

  await t.test("5. Manual Entry Modal with Cached Catalogue: Available immediately offline", async () => {
    // 1. Prime the offline catalogue cache for shop_alpha
    await setCachedCatalogue("shop_alpha", [MOCK_MILO, MOCK_PEAK]);

    // 2. Simulate offline cellular drop
    navigator.onLine = false;
    globalThis.fetch = async () => {
      throw new TypeError("Failed to fetch: cellular connection offline");
    };

    // 3. Mount Transactions view
    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = TRANSACTIONS_HTML;
    document.body.append(root);

    const cleanup = mountTransactions(root, "shop_alpha");
    await new Promise((r) => setTimeout(r, 20));

    // 4. Open Manual Entry Modal
    const manualBtn = root.querySelector('[data-hook="manual-entry"]');
    assert.ok(manualBtn, "Direct Counter Entry button exists");
    manualBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    const modalDialog = document.body.querySelector(".modal-dialog");
    assert.ok(modalDialog, "Manual entry modal opened");

    // 5. Verify the offline catalogue badge and options populated from cache
    const offlineBadge = modalDialog.querySelector(".badge--offline-queue");
    assert.ok(offlineBadge);
    assert.match(offlineBadge.textContent, /Offline Catalogue \(2 cached\)/);

    const selects = modalDialog.querySelectorAll("select");
    const typeSelect = selects[0];
    const prodSelect = selects[1];
    const unitSelect = selects[2];

    assert.ok(prodSelect.options.length >= 2, "Products pre-populated from cached catalogue");
    assert.equal(prodSelect.options[0].value, "prd_milo_1");
    assert.equal(prodSelect.options[1].value, "prd_peak_1");

    // 6. Select Milo 500g, set quantity 3, unit pack, and submit
    typeSelect.value = "sale";
    prodSelect.value = "prd_milo_1";
    unitSelect.value = "pack";

    const qtyInput = modalDialog.querySelector('input[type="number"]');
    qtyInput.value = "3";

    const notesInput = modalDialog.querySelector('input[placeholder*="Customer spoke quietly"]');
    notesInput.value = "Off-grid generator noise counter sale";

    const form = modalDialog.querySelector("form");
    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 30));

    // 7. Verify status message shows queued on device
    const statusMsg = modalDialog.querySelector(".status-badge--warning");
    assert.ok(statusMsg);
    assert.match(statusMsg.textContent, /entry queued on this device/i);

    // 8. Verify the transaction is safely preserved in offline_queue
    const queuedCount = await getQueuedCount("shop_alpha");
    assert.equal(queuedCount, 1, "Transaction was queued on device");

    const queuedItems = await getQueuedCaptures("shop_alpha");
    assert.equal(queuedItems.length, 1);
    const item = queuedItems[0];
    assert.equal(item.kind, "manual-transaction");
    assert.equal(item.shop_id, "shop_alpha");
    assert.equal(item.manual_payload.product_id, "prd_milo_1");
    assert.equal(item.manual_payload.quantity, 3);
    assert.equal(item.manual_payload.unit, "pack");
    assert.equal(item.manual_payload.notes, "Off-grid generator noise counter sale");
    assert.ok(item.manual_payload.request_id, "Carries stable request_id");

    cleanup();
  });

  await t.test("6. Manual Entry Modal without Cached Catalogue: Informs user clearly when offline", async () => {
    // No catalogue cached for shop_empty
    navigator.onLine = false;
    globalThis.fetch = async () => {
      throw new TypeError("Failed to fetch");
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = TRANSACTIONS_HTML;
    document.body.append(root);

    const cleanup = mountTransactions(root, "shop_empty");
    await new Promise((r) => setTimeout(r, 20));

    const manualBtn = root.querySelector('[data-hook="manual-entry"]');
    manualBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    const modalDialog = document.body.querySelector(".modal-dialog");
    assert.ok(modalDialog);

    const prodSelect = modalDialog.querySelectorAll("select")[1];
    assert.match(prodSelect.textContent, /Offline: no cached products/i);

    // Submitting without product selection is blocked
    const form = modalDialog.querySelector("form");
    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 10));

    const statusMsg = modalDialog.querySelector(".status-badge--error");
    assert.ok(statusMsg);
    assert.match(statusMsg.textContent, /Please select a product/i);

    cleanup();
  });

  await t.test("7. Home View Hydration: Browsing dashboard caches catalogue for offline use", async () => {
    assert.deepEqual(await getCachedCatalogue("shop_alpha"), []);

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [MOCK_MILO, MOCK_PEAK],
            count: 2,
            low_stock_count: 0,
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            transactions: [],
            count: 0,
            total: 0,
          }),
        };
      }
      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = HOME_HTML;
    document.body.append(root);

    const cleanup = mountHome(root, "shop_alpha");
    await new Promise((r) => setTimeout(r, 50));

    const cached = await getCachedCatalogue("shop_alpha");
    assert.equal(cached.length, 2, "Home overview cached the products for shop_alpha");
    assert.equal(cached[0].id, "prd_milo_1");
    assert.equal(cached[1].id, "prd_peak_1");

    cleanup();
  });

  await t.test("8. Inventory View Hydration: Inventory refresh updates cached catalogue", async () => {
    assert.deepEqual(await getCachedCatalogue("shop_alpha"), []);

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [MOCK_PEAK],
            count: 1,
            low_stock_count: 0,
          }),
        };
      }
      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = INVENTORY_HTML;
    document.body.append(root);

    const cleanup = mountInventory(root, "shop_alpha");
    await new Promise((r) => setTimeout(r, 50));

    const cached = await getCachedCatalogue("shop_alpha");
    assert.equal(cached.length, 1, "Inventory view cached the snapshot for shop_alpha");
    assert.equal(cached[0].id, "prd_peak_1");

    cleanup();
  });

  await t.test("9. Replay & Sync: Reconnect replays offline manual transaction with stable request_id", async () => {
    await setCachedCatalogue("shop_alpha", [MOCK_MILO]);

    // Enqueue manual transaction offline
    const queued = await enqueueManualTransaction(
      "shop_alpha",
      {
        request_id: "man_offline_stable_123",
        type: "sale",
        product_id: "prd_milo_1",
        quantity: 5,
        unit: "pack",
        amount: { amount: 1250000, currency: "NGN" },
        notes: "Recorded offline, replaying now",
      },
      "Milo 500g",
    );
    assert.ok(queued);
    assert.equal(await getQueuedCount("shop_alpha"), 1);

    // Reconnect network
    navigator.onLine = true;
    let postedBody = null;
    let postedHeaders = null;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      if (u.endsWith("/api/transactions/manual")) {
        postedBody = JSON.parse(String(init.body));
        postedHeaders = init.headers;
        return {
          ok: true,
          status: 201,
          json: async () => ({
            id: "txn_replayed_1",
            shop_id: "shop_alpha",
            type: "sale",
            product_name: "Milo 500g",
            product_id: "prd_milo_1",
            quantity: 5,
            unit: "pack",
            amount: { amount: 1250000, currency: "NGN" },
            confidence: { overall: 1.0 },
            status: "confirmed",
          }),
        };
      }
      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    setActiveQueueShop("shop_alpha");
    const stats = await syncOfflineQueue();

    assert.equal(stats.synced, 1);
    assert.equal(stats.failed, 0);
    assert.equal(await getQueuedCount("shop_alpha"), 0, "Queue cleared after successful replay");

    assert.equal(postedBody.request_id, "man_offline_stable_123");
    assert.equal(postedBody.product_id, "prd_milo_1");
    assert.equal(postedBody.quantity, 5);
    assert.equal(postedHeaders["Idempotency-Key"], "man_offline_stable_123");
  });
});
