/**
 * F30 — Inventory stock filters.
 *
 * The catalogue already had search, active/archived tabs and the add/import
 * actions. This suite covers the missing supported stock filter: low stock and
 * out of stock, composed with the tab and search, with counts that follow the
 * rows on the tab and update from live inventory events.
 */

import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountInventory } from "../dist/views/inventory.js";

const TEMPLATE = fs.readFileSync(new URL("../src/components/inventory.html", import.meta.url), "utf8");

function product(overrides = {}) {
  return {
    id: "prd_default",
    shop_id: "shop_test",
    name: "Default Product",
    sku: "SKU-DEFAULT",
    aliases: [],
    price: { amount: 250000, currency: "NGN" },
    quantity: 20,
    base_unit: "pack",
    packaging: [],
    low_stock_threshold: 5,
    ...overrides,
  };
}

const MILO = product({ id: "prd_milo", name: "Milo 500g", sku: "MILO-500", quantity: 20 });
const PEAK_LOW = product({
  id: "prd_peak",
  name: "Peak Milk",
  sku: "PEAK-1",
  quantity: 3,
  aliases: ["peek"],
});
const OIL_OUT = product({ id: "prd_oil", name: "Kings Oil", sku: "OIL-1", quantity: 0 });
const BEANS_LOW = product({ id: "prd_beans", name: "Beans 1kg", sku: "BEANS-1", quantity: 1, low_stock_threshold: 2 });
const ARCHIVED_OUT = product({
  id: "prd_old",
  name: "Old Stock",
  sku: "OLD-1",
  quantity: 0,
  archived_at: "2026-09-01T10:00:00Z",
});

const ALL_PRODUCTS = [MILO, PEAK_LOW, OIL_OUT, BEANS_LOW, ARCHIVED_OUT];

const settle = (ms = 25) => new Promise((resolve) => setTimeout(resolve, ms));

function stockSelect(root) {
  return root.querySelector('[data-hook="catalogue-stock-filter"]');
}

function chooseStock(root, value) {
  const select = stockSelect(root);
  select.value = value;
  select.dispatchEvent(new MockEvent("change"));
}

function optionLabel(root, value) {
  const select = stockSelect(root);
  const option = Array.from(select.options).find((candidate) => candidate.value === value);
  return option?.textContent ?? "";
}

function rows(root) {
  return root.querySelector('[data-hook="catalogue-body"]').children;
}

function rowText(root) {
  return Array.from(rows(root)).map((row) => row.textContent).join("\n");
}

function mount(root, document) {
  root.innerHTML = TEMPLATE;
  document.body.append(root);
  return mountInventory(root, "shop_test");
}

test("F30 Inventory stock filters", async (t) => {
  const { document } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.beforeEach(() => {
    document.body.replaceChildren();
  });

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  function mockCatalogue(products = ALL_PRODUCTS, calls = []) {
    globalThis.fetch = async (url) => {
      calls.push(String(url));
      return {
        ok: true,
        status: 200,
        json: async () => ({
          shop_id: "shop_test",
          products: [...products],
          count: products.length,
          low_stock_count: products.filter((item) => !item.archived_at && item.quantity > 0 && item.quantity <= (item.low_stock_threshold ?? 0)).length,
          last_seq: 1,
        }),
      };
    };
    return calls;
  }

  await t.test("1. Default view keeps the active tab and labels the stock counts", async () => {
    const calls = mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();

    assert.equal(calls.length, 1);
    assert.equal(rows(root).length, 4, "active tab should show four products, not the archived one");
    assert.equal(optionLabel(root, ""), "All stock (4)");
    assert.equal(optionLabel(root, "low"), "Low stock (2)");
    assert.equal(optionLabel(root, "out"), "Out of stock (1)");

    cleanup();
  });

  await t.test("2. Low stock filters to above-zero items at or below the threshold", async () => {
    const calls = mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();
    const fetchesBefore = calls.length;

    chooseStock(root, "low");

    assert.equal(rows(root).length, 2);
    const text = rowText(root);
    assert.match(text, /Peak Milk/);
    assert.match(text, /Beans 1kg/);
    assert.doesNotMatch(text, /Milo 500g/);
    assert.doesNotMatch(text, /Kings Oil/);

    // The filter is a view of the loaded catalogue, not another round trip.
    assert.equal(calls.length, fetchesBefore, "changing the stock filter must not refetch");

    cleanup();
  });

  await t.test("3. Out of stock excludes zero-threshold items and combines with search", async () => {
    mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();

    chooseStock(root, "out");
    assert.equal(rows(root).length, 1);
    assert.match(rowText(root), /Kings Oil/);

    // Search narrows the stock-filtered rows; a non-matching search explains
    // both filters and offers one recovery action for both.
    const search = root.querySelector('[data-hook="catalogue-query"]');
    search.value = "milo";
    search.dispatchEvent(new MockEvent("input"));
    await settle(10);

    assert.equal(rows(root).length, 1);
    assert.match(rows(root)[0].textContent, /No products match this search and stock filter/);

    const clear = Array.from(root.querySelectorAll("button")).find((button) => button.textContent === "Clear filters");
    assert.ok(clear, "expected a Clear filters action");
    clear.dispatchEvent(new MockEvent("click"));
    await settle(300);

    assert.equal(stockSelect(root).value, "");
    assert.equal(search.value, "");
    assert.equal(rows(root).length, 4);

    cleanup();
  });

  await t.test("4. Counts follow the archived tab", async () => {
    mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();

    const archived = Array.from(root.querySelectorAll('[data-hook="catalogue-tabs"] .tab')).find(
      (button) => button.textContent === "Archived",
    );
    assert.ok(archived);
    archived.dispatchEvent(new MockEvent("click"));
    await settle();

    assert.equal(rows(root).length, 1);
    assert.match(rowText(root), /Old Stock/);
    assert.equal(optionLabel(root, ""), "All stock (1)");
    assert.equal(optionLabel(root, "low"), "Low stock (0)");
    assert.equal(optionLabel(root, "out"), "Out of stock (1)");

    chooseStock(root, "out");
    assert.equal(rows(root).length, 1);
    assert.match(rowText(root), /Old Stock/);

    cleanup();
  });

  await t.test("5. A live stock update moves the row between filters and updates counts", async () => {
    mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();

    chooseStock(root, "out");
    assert.equal(rows(root).length, 1);

    // Kings Oil is restocked to 4, above zero but still at/below its threshold
    // of 5, so it leaves "out" and joins "low".
    window.dispatchEvent(
      new CustomEvent("ll:inventory-updated", { detail: { data: { ...OIL_OUT, quantity: 4 } } }),
    );

    assert.equal(rows(root).length, 1);
    assert.match(rowText(root), /No out-of-stock products in this tab/);
    assert.equal(optionLabel(root, "out"), "Out of stock (0)");
    assert.equal(optionLabel(root, "low"), "Low stock (3)");

    chooseStock(root, "low");
    assert.equal(rows(root).length, 3);
    assert.match(rowText(root), /Kings Oil/);

    cleanup();
  });

  await t.test("6. Cleanup removes the stock listener", async () => {
    const calls = mockCatalogue();
    const root = document.createElement("div");
    const cleanup = mount(root, document);
    await settle();
    const fetchesBefore = calls.length;
    const rowsBefore = rows(root).length;

    cleanup();

    chooseStock(root, "out");
    const search = root.querySelector('[data-hook="catalogue-query"]');
    search.value = "milo";
    search.dispatchEvent(new MockEvent("input"));
    await settle(300);
    window.dispatchEvent(
      new CustomEvent("ll:inventory-updated", { detail: { data: { ...OIL_OUT, quantity: 9 } } }),
    );

    assert.equal(calls.length, fetchesBefore, "an unmounted inventory view must not refetch");
    assert.equal(rows(root).length, rowsBefore, "an unmounted inventory view must not rerender");
  });
});
