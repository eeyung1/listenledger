/**
 * F31 — packaging is editable from the product form.
 *
 * The catalogue already lists each product's packaging, and the CSV import
 * accepts it, but the form gave no way to correct it afterwards. These tests
 * cover the rule check that explains a bad chain before anything is sent, and
 * the form itself: existing relations loaded, rows added and removed, cleared
 * packaging, a blocked invalid save, and packaging included on create.
 */

import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountInventory, packagingProblem } from "../dist/views/inventory.js";

const INVENTORY_HTML = fs.readFileSync(
  new URL("../src/components/inventory.html", import.meta.url),
  "utf8",
);

const tick = () => new Promise((resolve) => setTimeout(resolve, 20));

function milo(overrides = {}) {
  return {
    id: "prd_milo_1",
    shop_id: "shop_1",
    sku: "MILO-500",
    name: "Milo 500g",
    price: { amount: 250000, currency: "NGN" },
    quantity: 15,
    base_unit: "pack",
    aliases: ["milo"],
    packaging: [{ from: "carton", to: "pack", factor: 12 }],
    low_stock_threshold: 5,
    archived_at: null,
    ...overrides,
  };
}

/**
 * Mounts the view with one product and records what the form sends. The
 * response echoes the request so applyProduct sees what the server would.
 */
async function mountWith(product) {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;
  const sent = { patches: [], posts: [] };
  let products = [product];

  globalThis.fetch = async (url, init) => {
    const target = String(url);
    const method = init?.method ?? "GET";

    if (target.includes("/api/inventory")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          products,
          count: products.length,
          low_stock_count: 0,
        }),
      };
    }

    if (target.endsWith("/api/products") && method === "POST") {
      const body = JSON.parse(String(init.body));
      sent.posts.push(body);
      const created = { ...milo(), id: "prd_new", ...body, archived_at: null };
      products = [...products, created];
      return { ok: true, status: 201, json: async () => created };
    }

    if (target.includes("/api/products/") && method === "PATCH") {
      const body = JSON.parse(String(init.body));
      sent.patches.push(body);
      const updated = { ...product, ...body };
      products = [updated];
      return { ok: true, status: 200, json: async () => updated };
    }

    return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
  };

  const root = document.createElement("div");
  root.id = "app";
  root.innerHTML = INVENTORY_HTML;
  document.body.append(root);

  const cleanup = mountInventory(root, "shop_1");
  await tick();

  return {
    root,
    document,
    sent,
    restore: () => {
      cleanup();
      globalThis.fetch = originalFetch;
      document.body.replaceChildren();
    },
  };
}

function openEdit(root, document) {
  const body = root.querySelector('[data-hook="catalogue-body"]');
  const editBtn = Array.from(body.querySelectorAll("button")).find(
    (button) => button.textContent === "Edit",
  );
  assert.ok(editBtn, "the row must offer an Edit action");
  editBtn.dispatchEvent(new MockEvent("click"));
  const dialog = root.querySelector(".modal-dialog");
  assert.ok(dialog, "the product form opened");
  return dialog;
}

function packagingRow(dialog, index = 0) {
  const row = dialog.querySelectorAll(".packaging-row")[index];
  assert.ok(row, `packaging row ${index} exists`);
  return row;
}

function rowValues(row) {
  return {
    from: row.querySelector('[data-role="packaging-from"]').value,
    factor: row.querySelector('[data-role="packaging-factor"]').value,
    to: row.querySelector('[data-role="packaging-to"]').value,
  };
}

async function submit(dialog) {
  dialog.querySelector("form").dispatchEvent(new MockEvent("submit"));
  await tick();
}

test("F31 Editable product packaging", async (t) => {
  await t.test("1. A chain the server would reject is explained first", () => {
    assert.equal(
      packagingProblem("pack", [{ from: "carton", to: "pack", factor: 12 }]),
      null,
      "a carton of packs is a usable chain",
    );
    assert.equal(
      packagingProblem("piece", [
        { from: "carton", to: "pack", factor: 12 },
        { from: "pack", to: "piece", factor: 10 },
      ]),
      null,
      "a two-step chain that reaches the base unit is usable",
    );

    assert.match(
      packagingProblem("pack", [{ from: "carton", to: "carton", factor: 12 }]) ?? "",
      /two different units/,
      "a unit cannot contain itself",
    );
    assert.match(
      packagingProblem("pack", [{ from: "pack", to: "piece", factor: 12 }]) ?? "",
      /base unit/,
      "the base unit cannot be the larger unit",
    );
    assert.match(
      packagingProblem("pack", [{ from: "carton", to: "pack", factor: 0 }]) ?? "",
      /whole number/,
      "a factor of zero converts nothing",
    );
    assert.match(
      packagingProblem("pack", [
        { from: "carton", to: "pack", factor: 12 },
        { from: "carton", to: "pack", factor: 24 },
      ]) ?? "",
      /mapped once/,
      "one larger unit cannot mean two different things",
    );
    assert.match(
      packagingProblem("piece", [{ from: "carton", to: "pack", factor: 12 }]) ?? "",
      /never converted to the base unit/,
      "a chain that stops short of the base unit cannot become stock",
    );
    assert.match(
      packagingProblem("piece", [
        { from: "carton", to: "pack", factor: 12 },
        { from: "pack", to: "carton", factor: 2 },
      ]) ?? "",
      /loops back/,
      "a cycle never reaches the base unit",
    );
    assert.equal(packagingProblem("pack", []), null, "no packaging is a valid product");
  });

  await t.test("2. Existing packaging is loaded and a corrected factor is saved", async () => {
    const view = await mountWith(milo());
    try {
      const dialog = openEdit(view.root, view.document);
      const row = packagingRow(dialog);
      assert.deepEqual(rowValues(row), { from: "carton", factor: "12", to: "pack" });
      assert.match(
        dialog.querySelector(".modal-help").textContent,
        /1 carton = 12 pack/,
        "the form says what a packaging row means",
      );

      row.querySelector('[data-role="packaging-factor"]').value = "24";
      await submit(dialog);

      assert.equal(view.sent.patches.length, 1);
      assert.deepEqual(
        view.sent.patches[0].packaging,
        [{ from: "carton", to: "pack", factor: 24 }],
        "the stocking correction reaches the server",
      );
      assert.equal(view.root.querySelector(".modal-dialog"), null, "the form closes on success");
    } finally {
      view.restore();
    }
  });

  await t.test("3. Rows can be added and removed, and the surviving set is saved", async () => {
    const view = await mountWith(milo());
    try {
      const dialog = openEdit(view.root, view.document);
      const addBtn = Array.from(dialog.querySelectorAll("button")).find(
        (button) => button.textContent === "Add packaging",
      );
      assert.ok(addBtn, "the form offers another relationship");
      addBtn.dispatchEvent(new MockEvent("click"));
      assert.equal(dialog.querySelectorAll(".packaging-row").length, 2);

      const added = packagingRow(dialog, 1);
      added.querySelector('[data-role="packaging-from"]').value = "crate";
      added.querySelector('[data-role="packaging-to"]').value = "pack";
      added.querySelector('[data-role="packaging-factor"]').value = "6";

      const removeBtn = Array.from(packagingRow(dialog, 0).querySelectorAll("button")).find(
        (button) => button.textContent === "Remove",
      );
      assert.ok(removeBtn, "a row can be taken back out");
      removeBtn.dispatchEvent(new MockEvent("click"));
      assert.equal(dialog.querySelectorAll(".packaging-row").length, 1);

      await submit(dialog);
      assert.deepEqual(
        view.sent.patches[0].packaging,
        [{ from: "crate", to: "pack", factor: 6 }],
        "only the rows left in the form are saved",
      );
    } finally {
      view.restore();
    }
  });

  await t.test("4. Removing every row clears packaging instead of keeping it", async () => {
    const view = await mountWith(milo());
    try {
      const dialog = openEdit(view.root, view.document);
      const removeBtn = Array.from(packagingRow(dialog).querySelectorAll("button")).find(
        (button) => button.textContent === "Remove",
      );
      removeBtn.dispatchEvent(new MockEvent("click"));
      await submit(dialog);

      assert.deepEqual(
        view.sent.patches[0].packaging,
        [],
        "an empty form must clear the relations, not leave the old ones",
      );
    } finally {
      view.restore();
    }
  });

  await t.test("5. An invalid row is explained and nothing is sent", async () => {
    const view = await mountWith(milo());
    try {
      const dialog = openEdit(view.root, view.document);
      const row = packagingRow(dialog);
      row.querySelector('[data-role="packaging-from"]').value = "pack";
      row.querySelector('[data-role="packaging-to"]').value = "piece";
      await submit(dialog);

      assert.equal(view.sent.patches.length, 0, "no request leaves with a chain the server rejects");
      const error = dialog.querySelector(".form-error");
      assert.equal(error.hidden, false);
      assert.match(error.textContent, /base unit/);
      const submitBtn = Array.from(dialog.querySelectorAll("button")).find(
        (button) => button.textContent === "Save Changes",
      );
      assert.equal(submitBtn.disabled, false, "the owner can fix the row and try again");
    } finally {
      view.restore();
    }
  });

  await t.test("6. A new product can be created with packaging", async () => {
    const view = await mountWith(milo());
    try {
      const addBtn = view.root.querySelector('[data-hook="catalogue-add"]');
      addBtn.dispatchEvent(new MockEvent("click"));
      const dialog = view.root.querySelector(".modal-dialog");
      assert.ok(dialog);

      const texts = dialog.querySelectorAll('input[type="text"]');
      texts[0].value = "Peak Milk 400g";
      texts[1].value = "PEAK-400";
      dialog.querySelector('input[type="number"]').value = "4.5";

      const packagingBtn = Array.from(dialog.querySelectorAll("button")).find(
        (button) => button.textContent === "Add packaging",
      );
      packagingBtn.dispatchEvent(new MockEvent("click"));
      assert.equal(dialog.querySelectorAll(".packaging-row").length, 1, "the row appears in the form");

      const row = packagingRow(dialog);
      assert.equal(rowValues(row).from, "carton", "a fresh row suggests a larger unit");
      assert.equal(rowValues(row).to, "piece", "and converts down to the base unit");
      row.querySelector('[data-role="packaging-factor"]').value = "24";

      await submit(dialog);
      assert.equal(view.sent.posts.length, 1);
      assert.deepEqual(
        view.sent.posts[0].packaging,
        [{ from: "carton", to: "piece", factor: 24 }],
        "packaging is part of the catalogue entry, not an afterthought",
      );
    } finally {
      view.restore();
    }
  });
});
