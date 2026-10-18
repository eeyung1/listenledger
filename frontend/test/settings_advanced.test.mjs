import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountSettings } from "../dist/views/settings.js";

const html = fs.readFileSync(new URL("../src/components/settings.html", import.meta.url), "utf8");
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const hook = (root, name) => root.querySelector(`[data-hook="${name}"]`);
const shop = {id: "shop_advanced", owner_id: "owner_test", name: "Test shop", currency: "NGN", timezone: "Africa/Lagos", confidence_threshold: 0.85, sale_phrase: "thank you", restock_phrase: "stock received"};
const response = (status, body) => ({ok: status >= 200 && status < 300, status, json: async () => body});

function setup() {
  const {document} = setupDOMEnvironment();
  const root = document.createElement("div"); root.innerHTML = html; document.body.append(root);
  return root;
}

test("F34: Advanced/Pilot is a native closed disclosure with associated threshold and parked audit controls", () => {
  const root = setup();
  const advanced = hook(root, "advanced-pilot-settings");
  assert.equal(advanced.tagName, "DETAILS");
  assert.equal(advanced.hasAttribute("open"), false);
  assert.equal(advanced.querySelector("summary").textContent, "Advanced / Pilot");
  const form = hook(root, "settings-form");
  const range = hook(advanced, "setting-threshold-range");
  assert.equal(range.getAttribute("form"), form.getAttribute("id"));
  assert.equal(form.querySelector('[data-hook="setting-threshold-range"]'), null);
  assert.equal(hook(form, "setting-name").required, true);
  assert.ok(hook(form, "setting-sale-phrase"));
  assert.ok(hook(form, "save-settings-btn"));
  assert.ok(hook(advanced, "target-gates-form"));
  assert.ok(hook(advanced, "reconciliation-form"));
  assert.ok(hook(advanced, "reconciliation-list"));
  assert.ok(hook(advanced, "evaluate-cohort-btn"));
  assert.equal(advanced.querySelector('[data-pilot-parked="target-gates-heading"]').hidden, true);
  assert.equal(advanced.querySelector('[data-pilot-parked="pilot-reconciliation-heading"]').hidden, true);
  assert.equal(advanced.querySelector('[aria-labelledby="privacy-heading"]'), null);
  assert.ok(root.querySelector('[aria-labelledby="privacy-heading"]'));
});

test("F34: relocated threshold loads, updates its percentage and persists through the shop form, including retry", async (t) => {
  const root = setup();
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  const patches = [];
  let rejectSave = true;
  globalThis.fetch = async (url, options = {}) => {
    if (new URL(String(url), "http://localhost").pathname !== "/api/settings") return response(503, {error: "Pilot tool unavailable"});
    if (options.method === "PATCH") {
      const body = JSON.parse(options.body); patches.push(body);
      return rejectSave ? response(400, {error: "Settings save rejected"}) : response(200, {...shop, ...body});
    }
    return response(200, {shop, devices: []});
  };
  const cleanup = mountSettings(root, shop.id); t.after(cleanup);
  await flush();
  const range = hook(root, "setting-threshold-range");
  assert.equal(range.value, "0.85");
  assert.equal(hook(root, "setting-threshold-val").textContent, "85%");
  range.value = "0.90"; range.dispatchEvent(new MockEvent("input"));
  assert.equal(hook(root, "setting-threshold-val").textContent, "90%");
  hook(root, "settings-form").dispatchEvent(new MockEvent("submit")); await flush();
  assert.deepEqual(patches[0], {name: shop.name, currency: "NGN", timezone: shop.timezone, confidence_threshold: 0.9, sale_phrase: shop.sale_phrase, restock_phrase: shop.restock_phrase});
  assert.match(hook(root, "settings-status").textContent, /Settings save rejected/);
  assert.equal(range.value, "0.90");
  assert.equal(hook(root, "save-settings-btn").disabled, false);
  rejectSave = false;
  hook(root, "settings-form").dispatchEvent(new MockEvent("submit")); await flush();
  assert.equal(patches.length, 2); assert.equal(patches[1].confidence_threshold, 0.9);
  assert.match(hook(root, "settings-status").textContent, /saved successfully/);
  assert.equal(range.value, "0.90");
});
