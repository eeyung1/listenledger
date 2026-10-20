/**
 * F28 — pending-review count in the navigation.
 *
 * Covers the navigation contract (the count appears on Transactions, is named
 * for screen readers, is capped and disappears at zero) and the update module
 * (initial read, socket and reconnect refreshes, failed reads, coalescing and
 * teardown). Viewport behaviour is not asserted here.
 */

import test from "node:test";
import assert from "node:assert/strict";
import { setupDOMEnvironment, parseHTMLFragment } from "./test_dom.mjs";
import { renderNavigation, syncActiveNav } from "../dist/nav.js";
import { startReviewBadge } from "../dist/review_badge.js";
import { CONNECTION_STATUS_EVENT, DOM_EVENT_NAMES } from "../dist/ws.js";

const SHELL = `
<div class="app-shell">
  <aside class="app-sidebar" data-shell-chrome>
    <nav class="app-nav app-nav--sidebar" aria-label="Main" data-hook="nav-sidebar"></nav>
  </aside>
  <div class="app-shell__content">
    <main id="app" class="app-main"></main>
    <nav class="app-nav app-nav--mobile" aria-label="Main" data-shell-chrome data-hook="nav-mobile"></nav>
  </div>
</div>`;

const tick = () => new Promise((resolve) => setTimeout(resolve, 10));

function linksIn(container) {
  return container.querySelectorAll("a[data-route]");
}

function transactionsLink(container) {
  return linksIn(container).find((link) => link.dataset.route === "/transactions");
}

function mountShell(document) {
  document.body.replaceChildren();
  parseHTMLFragment(SHELL, document.body);
  return {
    sidebar: document.querySelector('[data-hook="nav-sidebar"]'),
    mobile: document.querySelector('[data-hook="nav-mobile"]'),
  };
}

test("F28 Review count in the navigation", async (t) => {
  await t.test("1. The count appears on Transactions on both surfaces", () => {
    const { document } = setupDOMEnvironment();
    const { sidebar, mobile } = mountShell(document);

    renderNavigation(document, { reviewCount: 3 });

    for (const surface of [sidebar, mobile]) {
      const link = transactionsLink(surface);
      const badge = link.querySelector('[data-hook="nav-review-count"]');
      assert.notEqual(badge, null, "the queue must be visible from everywhere");
      assert.equal(badge.textContent, "3");
      assert.match(badge.className, /badge--review/);
      assert.equal(
        link.getAttribute("aria-label"),
        "Transactions, 3 awaiting review",
        "the number must be announced with its destination",
      );
    }
  });

  await t.test("2. Nothing is drawn when the queue is empty", () => {
    const { document } = setupDOMEnvironment();
    const { sidebar } = mountShell(document);

    renderNavigation(document, { reviewCount: 0 });
    assert.equal(
      transactionsLink(sidebar).querySelector('[data-hook="nav-review-count"]'),
      null,
      "a badge reading 0 teaches the owner nothing",
    );
    assert.equal(transactionsLink(sidebar).getAttribute("aria-label"), null);

    renderNavigation(document);
    assert.equal(
      transactionsLink(sidebar).querySelector('[data-hook="nav-review-count"]'),
      null,
      "an omitted count behaves like zero",
    );
  });

  await t.test("3. A long queue is capped so the label keeps its room", () => {
    const { document } = setupDOMEnvironment();
    const { sidebar } = mountShell(document);

    renderNavigation(document, { reviewCount: 12 });
    const link = transactionsLink(sidebar);
    assert.equal(link.querySelector('[data-hook="nav-review-count"]').textContent, "9+");
    assert.equal(link.getAttribute("aria-label"), "Transactions, 12 awaiting review");
  });

  await t.test("4. Redrawing for the count keeps the current destination marked", () => {
    const { document } = setupDOMEnvironment();
    const { sidebar, mobile } = mountShell(document);

    renderNavigation(document);
    syncActiveNav(document, "/inventory");
    renderNavigation(document, { reviewCount: 2 });
    syncActiveNav(document, "/inventory");

    for (const surface of [sidebar, mobile]) {
      const inventory = linksIn(surface).find((link) => link.dataset.route === "/inventory");
      assert.match(inventory.className, /is-active/);
      assert.equal(inventory.getAttribute("aria-current"), "page");
      assert.equal(
        transactionsLink(surface).getAttribute("aria-current"),
        null,
        "only the current page is current",
      );
    }
  });

  await t.test("5. The count is read once at startup from the review total", async () => {
    const { document } = setupDOMEnvironment();
    const originalFetch = globalThis.fetch;
    const urls = [];
    globalThis.fetch = async (url) => {
      urls.push(String(url));
      return { ok: true, status: 200, json: async () => ({ transactions: [], count: 0, total: 4 }) };
    };
    try {
      const seen = [];
      const badge = startReviewBadge({ shopId: "shop_1", onChange: (count) => seen.push(count) });
      await tick();

      assert.deepEqual(seen, [4]);
      const parsed = new URL(urls[0], "http://localhost");
      assert.equal(parsed.searchParams.get("status"), "review_required");
      assert.equal(parsed.searchParams.get("limit"), "1", "the count must not cost a page of rows");
      badge.stop();
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  await t.test("6. Socket events refresh the count, and an unchanged count is not redrawn", async () => {
    const { document, window } = setupDOMEnvironment();
    const originalFetch = globalThis.fetch;
    let total = 2;
    let fetches = 0;
    globalThis.fetch = async () => {
      fetches += 1;
      return { ok: true, status: 200, json: async () => ({ transactions: [], count: 0, total }) };
    };
    try {
      const seen = [];
      const badge = startReviewBadge({ shopId: "shop_1", onChange: (count) => seen.push(count) });
      await tick();
      assert.deepEqual(seen, [2]);

      window.dispatchEvent(new CustomEvent(DOM_EVENT_NAMES["transaction.review_required"], { detail: {} }));
      await tick();
      assert.equal(fetches, 2, "a newly held capture changes the queue");
      assert.deepEqual(seen, [2], "the same number is not redrawn");

      total = 1;
      window.dispatchEvent(new CustomEvent(DOM_EVENT_NAMES["transaction.updated"], { detail: {} }));
      await tick();
      assert.equal(fetches, 3);
      assert.deepEqual(seen, [2, 1], "a decided capture lowers the queue");
      badge.stop();
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  await t.test("7. Only a resync asks for the count after a connection change", async () => {
    const { document, window } = setupDOMEnvironment();
    const originalFetch = globalThis.fetch;
    let fetches = 0;
    globalThis.fetch = async () => {
      fetches += 1;
      return { ok: true, status: 200, json: async () => ({ transactions: [], count: 0, total: 0 }) };
    };
    try {
      const badge = startReviewBadge({ shopId: "shop_1", onChange: () => {} });
      await tick();
      assert.equal(fetches, 1);

      window.dispatchEvent(
        new CustomEvent(CONNECTION_STATUS_EVENT, { detail: { status: "open", resyncRequired: false } }),
      );
      await tick();
      assert.equal(fetches, 1, "a clean reconnect has nothing to reconcile");

      window.dispatchEvent(
        new CustomEvent(CONNECTION_STATUS_EVENT, { detail: { status: "open", resyncRequired: true } }),
      );
      await tick();
      assert.equal(fetches, 2, "a missed stream means the count may be stale");
      badge.stop();
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  await t.test("8. A failed read keeps the last accurate count and does not throw", async () => {
    const { document } = setupDOMEnvironment();
    const originalFetch = globalThis.fetch;
    let fail = false;
    globalThis.fetch = async () => {
      if (fail) return { ok: false, status: 503, json: async () => ({ error: "unavailable" }) };
      return { ok: true, status: 200, json: async () => ({ transactions: [], count: 0, total: 5 }) };
    };
    try {
      const seen = [];
      const badge = startReviewBadge({ shopId: "shop_1", onChange: (count) => seen.push(count) });
      await tick();

      fail = true;
      await badge.refresh();
      assert.deepEqual(seen, [5], "a failed read must not paint a wrong number");
      badge.stop();
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  await t.test("9. Concurrent refreshes share one read, and stop detaches the events", async () => {
    const { document, window } = setupDOMEnvironment();
    const originalFetch = globalThis.fetch;
    let fetches = 0;
    let release;
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    globalThis.fetch = async () => {
      fetches += 1;
      await gate;
      return { ok: true, status: 200, json: async () => ({ transactions: [], count: 0, total: 1 }) };
    };
    try {
      const badge = startReviewBadge({ shopId: "shop_1", onChange: () => {} });
      const second = badge.refresh();
      const third = badge.refresh();
      release();
      await Promise.all([second, third]);
      assert.equal(fetches, 1, "three callers must not mean three requests");

      badge.stop();
      window.dispatchEvent(new CustomEvent(DOM_EVENT_NAMES["transaction.review_required"], { detail: {} }));
      await tick();
      assert.equal(fetches, 1, "a stopped badge must stop listening");
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
