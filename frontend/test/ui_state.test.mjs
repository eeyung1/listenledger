/**
 * F05 — shared UI states.
 *
 * Covers the shared state module directly, then proves two views actually go
 * through it: a failed catalogue load and an empty ledger/review queue render
 * the shared block with the right recovery action. Layout and viewport
 * behaviour are deliberately out of scope here (F06/F40).
 */

import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  connectionStatusLabel,
  installConnectionBanner,
  setFormStatus,
  uiState,
  uiStateItem,
  uiStateRow,
} from "../dist/ui_state.js";
import { mountInventory } from "../dist/views/inventory.js";
import { mountTransactions } from "../dist/views/transactions.js";

const INVENTORY_HTML = fs.readFileSync(
  new URL("../src/components/inventory.html", import.meta.url),
  "utf8",
);
const TRANSACTIONS_HTML = fs.readFileSync(
  new URL("../src/components/transactions.html", import.meta.url),
  "utf8",
);

function jsonResponse(payload) {
  return { ok: true, status: 200, json: async () => payload };
}

test("F05 shared UI states", async (t) => {
  const { document } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  await t.test("1. One block per kind, with the right live-region role", () => {
    const roles = { loading: "status", empty: "status", error: "alert", offline: "alert" };
    for (const [kind, role] of Object.entries(roles)) {
      const box = uiState(kind, { title: "A title" });
      assert.ok(box.classList.contains("ui-state"));
      assert.ok(box.classList.contains(`ui-state--${kind}`));
      assert.equal(box.getAttribute("data-state"), kind);
      // A failed load interrupts the owner; an empty state merely informs.
      assert.equal(box.getAttribute("role"), role);
      assert.equal(box.querySelector(".ui-state__title").textContent, "A title");
      const icon = box.querySelector(".ui-state__icon");
      assert.equal(icon.getAttribute("aria-hidden"), "true");
      assert.ok(icon.textContent.length > 0);
    }
  });

  await t.test("2. Detail and a recovery action are optional and wired", () => {
    const plain = uiState("empty", { title: "Nothing here" });
    assert.equal(plain.querySelector(".ui-state__detail"), null);
    assert.equal(plain.querySelector(".ui-state__action"), null);

    let clicks = 0;
    const box = uiState("error", {
      title: "Load failed",
      detail: "Try again",
      actionLabel: "Retry",
      onAction: () => {
        clicks += 1;
      },
    });
    assert.equal(box.querySelector(".ui-state__detail").textContent, "Try again");
    const action = box.querySelector(".ui-state__action");
    assert.equal(action.textContent, "Retry");
    action.dispatchEvent(new MockEvent("click"));
    assert.equal(clicks, 1);
  });

  await t.test("3. List-item and table-row hosts keep the block intact", () => {
    const item = uiStateItem("empty", { title: "None" });
    assert.equal(item.tagName.toLowerCase(), "li");
    assert.ok(item.classList.contains("ui-state--empty"));

    const row = uiStateRow("loading", { title: "Loading…" }, 8);
    assert.equal(row.tagName.toLowerCase(), "tr");
    assert.equal(row.className, "ui-state-row");
    const cell = row.querySelector("td");
    assert.equal(cell.colSpan, 8);
    assert.ok(cell.querySelector(".ui-state--loading"));
  });

  await t.test("4. Form status distinguishes saving, saved and failed", () => {
    const holder = document.createElement("span");
    holder.hidden = true;

    setFormStatus(holder, "info", "Saving…");
    assert.equal(holder.className, "form__status form__status--info");
    assert.equal(holder.hidden, false);
    assert.equal(holder.textContent, "Saving…");

    setFormStatus(holder, "success", "Saved");
    assert.ok(holder.classList.contains("form__status--success"));

    setFormStatus(holder, "error", "Failed");
    assert.ok(holder.classList.contains("form__status--error"));

    assert.doesNotThrow(() => setFormStatus(null, "error", "x"));
  });

  await t.test("5. Connection labels use the shared wording", () => {
    assert.equal(connectionStatusLabel("open"), "Live");
    assert.equal(connectionStatusLabel("reconnecting"), "Reconnecting…");
    assert.equal(connectionStatusLabel("closed"), "Offline");
    assert.equal(connectionStatusLabel("something-new"), "something-new");
  });

  await t.test("6. The disconnected banner answers connection events and Retry", () => {
    document.body.replaceChildren();
    let retries = 0;
    const cleanup = installConnectionBanner(document, {
      eventName: "ll:connection-status",
      onRetry: () => {
        retries += 1;
      },
    });

    const banner = document.body.querySelector(".app-banner");
    assert.ok(banner, "banner installed");
    assert.equal(banner.hidden, true, "hidden before any drop");

    // A first connect must not claim a connection was lost.
    window.dispatchEvent(
      new CustomEvent("ll:connection-status", {
        detail: { status: "connecting", resyncRequired: false, attempt: 0 },
      }),
    );
    assert.equal(banner.hidden, true);

    window.dispatchEvent(
      new CustomEvent("ll:connection-status", {
        detail: { status: "reconnecting", resyncRequired: false, attempt: 3 },
      }),
    );
    assert.equal(banner.hidden, false);
    assert.ok(banner.classList.contains("app-banner--reconnecting"));

    window.dispatchEvent(
      new CustomEvent("ll:connection-status", {
        detail: { status: "open", resyncRequired: true, attempt: 0 },
      }),
    );
    assert.equal(banner.hidden, true);

    window.dispatchEvent(
      new CustomEvent("ll:connection-status", {
        detail: { status: "closed", resyncRequired: false, attempt: 0 },
      }),
    );
    assert.equal(banner.hidden, false);
    assert.ok(banner.classList.contains("app-banner--closed"));

    if (typeof navigator !== "undefined") {
      // The browser reporting offline outranks the socket's own state.
      Object.defineProperty(navigator, "onLine", { value: false, configurable: true, writable: true });
      window.dispatchEvent(new CustomEvent("offline"));
      assert.ok(banner.classList.contains("app-banner--offline"));
      Object.defineProperty(navigator, "onLine", { value: true, configurable: true, writable: true });
    }

    banner.querySelector(".app-banner__retry").dispatchEvent(new MockEvent("click"));
    assert.equal(retries, 1);

    cleanup();
  });

  await t.test("7. A failed catalogue load renders the shared error state with Retry", async () => {
    globalThis.fetch = async () => ({ ok: false, status: 503, json: async () => ({ error: "down" }) });

    const root = document.createElement("div");
    root.innerHTML = INVENTORY_HTML;
    document.body.append(root);

    const cleanup = mountInventory(root, "shop_f05");
    await new Promise((resolve) => setTimeout(resolve, 20));

    const body = root.querySelector('[data-hook="catalogue-body"]');
    const error = body.querySelector(".ui-state--error");
    assert.ok(error, "error state rendered in the catalogue body");
    assert.equal(error.getAttribute("role"), "alert");
    assert.equal(error.querySelector(".ui-state__action").textContent, "Retry");

    cleanup();
  });

  await t.test("8. An empty catalogue offers the next step; an empty ledger explains itself", async () => {
    globalThis.fetch = async (url) => {
      const target = String(url);
      if (target.includes("/api/inventory")) return jsonResponse({ products: [], count: 0 });
      if (target.includes("/api/transactions")) {
        return jsonResponse({ transactions: [], count: 0, total: 0, last_seq: 0 });
      }
      return jsonResponse({});
    };

    const invRoot = document.createElement("div");
    invRoot.innerHTML = INVENTORY_HTML;
    document.body.append(invRoot);
    const invCleanup = mountInventory(invRoot, "shop_f05");
    await new Promise((resolve) => setTimeout(resolve, 20));

    const empty = invRoot.querySelector('[data-hook="catalogue-body"]').querySelector(".ui-state--empty");
    assert.ok(empty, "empty catalogue renders the shared empty state");
    assert.ok(empty.querySelector(".ui-state__action"), "an empty catalogue offers its next step");

    const txRoot = document.createElement("div");
    txRoot.innerHTML = TRANSACTIONS_HTML;
    document.body.append(txRoot);
    const txCleanup = mountTransactions(txRoot, "shop_f05");
    await new Promise((resolve) => setTimeout(resolve, 20));

    assert.ok(txRoot.querySelector('[data-hook="review-queue"]').querySelector(".ui-state--empty"));
    assert.ok(txRoot.querySelector('[data-hook="ledger-body"]').querySelector(".ui-state--empty"));

    invCleanup();
    txCleanup();
  });
});
