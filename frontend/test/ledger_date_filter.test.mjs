import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { dayBounds, mountTransactions } from "../dist/views/transactions.js";

const TRANSACTIONS_HTML = fs.readFileSync(
  new URL("../src/components/transactions.html", import.meta.url),
  "utf8",
);

const tick = () => new Promise((resolve) => setTimeout(resolve, 20));

/** Reads a sent RFC3339 instant back as the shopkeeper's local wall clock. */
function localParts(instant) {
  const d = new Date(instant);
  return {
    date: `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`,
    hours: d.getHours(),
    minutes: d.getMinutes(),
    seconds: d.getSeconds(),
    ms: d.getMilliseconds(),
  };
}

function query(url) {
  return new URL(url, "http://localhost").searchParams;
}

/** Requests the ledger page rather than the review queue it fetches alongside. */
function isLedgerRequest(url) {
  const parsed = new URL(url, "http://localhost");
  return parsed.pathname === "/api/transactions" && parsed.searchParams.get("status") !== "review_required";
}

function mountHarness() {
  const { document, window } = setupDOMEnvironment();
  const requests = [];
  const originalFetch = globalThis.fetch;

  globalThis.fetch = async (url) => {
    requests.push(String(url));
    return {
      ok: true,
      status: 200,
      json: async () => ({ transactions: [], count: 0, total: 0 }),
    };
  };

  const root = document.createElement("div");
  root.id = "app";
  root.innerHTML = TRANSACTIONS_HTML;
  document.body.append(root);

  return {
    document,
    window,
    root,
    requests,
    ledgerRequests: () => requests.filter(isLedgerRequest),
    cleanup: () => {
      globalThis.fetch = originalFetch;
      document.body.replaceChildren();
    },
  };
}

test("Ledger date-range filter suite", async (t) => {
  await t.test("dayBounds spans a whole local day, inclusive at both ends", () => {
    const bounds = dayBounds("2026-10-14");
    assert.notEqual(bounds, null);

    const start = localParts(bounds.from);
    assert.deepEqual(start, {
      date: "2026-10-14",
      hours: 0,
      minutes: 0,
      seconds: 0,
      ms: 0,
    });

    const end = localParts(bounds.to);
    assert.deepEqual(end, {
      date: "2026-10-14",
      hours: 23,
      minutes: 59,
      seconds: 59,
      ms: 999,
    });

    // A one-day range is a real range, not a zero-width instant.
    assert.ok(new Date(bounds.from).getTime() < new Date(bounds.to).getTime());
  });

  await t.test("dayBounds refuses values a date input could not have produced", () => {
    assert.equal(dayBounds(""), null);
    assert.equal(dayBounds("14/10/2026"), null);
    assert.equal(dayBounds("2026-13-01"), null);
    assert.equal(dayBounds("2026-02-31"), null, "a rolled-over date must not become March");
  });

  await t.test("the ledger sends no date bounds until a day is chosen", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const params = query(h.ledgerRequests().at(-1));
      assert.equal(params.has("from"), false);
      assert.equal(params.has("to"), false);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("a start day narrows the query to that day's first instant", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      const before = h.ledgerRequests().length;

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      assert.notEqual(fromInput, null, "the template must provide a From date input");
      fromInput.value = "2026-10-14";
      fromInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const requests = h.ledgerRequests();
      assert.equal(requests.length, before + 1, "changing the start day must requery the ledger");
      const params = query(requests.at(-1));
      const from = localParts(params.get("from"));
      assert.equal(from.date, "2026-10-14");
      assert.equal(from.hours, 0);
      assert.equal(from.minutes, 0);
      assert.equal(params.has("to"), false, "an open-ended range sends only the start");
      assert.equal(params.get("offset"), "0", "a new range starts at the first page");
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("an end day includes the whole of that day", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      assert.notEqual(toInput, null, "the template must provide a To date input");
      toInput.value = "2026-10-14";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const to = localParts(query(h.ledgerRequests().at(-1)).get("to"));
      assert.equal(to.date, "2026-10-14");
      assert.equal(to.hours, 23);
      assert.equal(to.minutes, 59);
      assert.equal(to.seconds, 59);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("the range composes with the direction and status filters", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const typeSelect = h.root.querySelector('[data-hook="filter-type"]');
      typeSelect.value = "restock";
      typeSelect.dispatchEvent(new MockEvent("change"));
      const statusSelect = h.root.querySelector('[data-hook="filter-status"]');
      statusSelect.value = "confirmed";
      statusSelect.dispatchEvent(new MockEvent("change"));

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      fromInput.value = "2026-10-01";
      fromInput.dispatchEvent(new MockEvent("change"));
      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      toInput.value = "2026-10-31";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const params = query(h.ledgerRequests().at(-1));
      assert.equal(params.get("type"), "restock");
      assert.equal(params.get("status"), "confirmed");
      assert.equal(localParts(params.get("from")).date, "2026-10-01");
      assert.equal(localParts(params.get("to")).date, "2026-10-31");
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("the CSV export carries the same range as the page", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      fromInput.value = "2026-10-01";
      fromInput.dispatchEvent(new MockEvent("change"));
      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      toInput.value = "2026-10-31";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const href = h.root.querySelector('[data-hook="export-csv"]').href;
      const exported = query(href);
      assert.equal(exported.get("format"), "csv");
      assert.equal(localParts(exported.get("from")).date, "2026-10-01");
      assert.equal(localParts(exported.get("to")).date, "2026-10-31");
      assert.equal(exported.get("from"), query(h.ledgerRequests().at(-1)).get("from"));
      assert.equal(exported.get("to"), query(h.ledgerRequests().at(-1)).get("to"));
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("an inverted range is explained instead of shown as an empty ledger", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      fromInput.value = "2026-10-20";
      fromInput.dispatchEvent(new MockEvent("change"));
      await tick();
      // A start day on its own is a valid open-ended range, so the query count
      // is taken after it: only the inversion may be refused.
      const before = h.ledgerRequests().length;

      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      toInput.value = "2026-10-10";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      assert.equal(
        h.ledgerRequests().length,
        before,
        "a refused range must not be sent as a query that would look empty",
      );
      const body = h.root.querySelector('[data-hook="ledger-body"]').textContent;
      assert.match(body, /start date is after the end date/i);
      assert.doesNotMatch(body, /No transactions recorded yet/);
      // The pagination totals still describe the refused range's predecessor,
      // so they must not be left claiming a page of rows that is not shown.
      assert.equal(h.root.querySelector('[data-hook="page-info"]').textContent, "Fix the date range");
      assert.equal(h.root.querySelector('[data-hook="page-next"]').disabled, true);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("correcting the range clears the message and queries again", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      fromInput.value = "2026-10-20";
      fromInput.dispatchEvent(new MockEvent("change"));
      toInput.value = "2026-10-10";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      toInput.value = "2026-10-25";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const params = query(h.ledgerRequests().at(-1));
      assert.equal(localParts(params.get("from")).date, "2026-10-20");
      assert.equal(localParts(params.get("to")).date, "2026-10-25");
      const body = h.root.querySelector('[data-hook="ledger-body"]').textContent;
      assert.doesNotMatch(body, /start date is after the end date/i);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("a live movement outside the chosen days is not added to the list", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();

      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      fromInput.value = "2026-10-14";
      fromInput.dispatchEvent(new MockEvent("change"));
      const toInput = h.root.querySelector('[data-hook="filter-to"]');
      toInput.value = "2026-10-14";
      toInput.dispatchEvent(new MockEvent("change"));
      await tick();

      const bounds = dayBounds("2026-10-14");
      const movement = (id, name, occurredAt) => ({
        id,
        shop_id: "shop_1",
        type: "sale",
        product_name: name,
        quantity: 1,
        unit: "pack",
        amount: { amount: 100000, currency: "NGN" },
        confidence: { overall: 0.97 },
        status: "confirmed",
        occurred_at: occurredAt,
        created_at: occurredAt,
      });

      h.window.dispatchEvent(
        new globalThis.CustomEvent("ll:transaction-received", {
          detail: {
            data: movement(
              "txn_before",
              "Movement Before Window",
              new Date(new Date(bounds.from).getTime() - 60_000).toISOString(),
            ),
          },
        }),
      );
      h.window.dispatchEvent(
        new globalThis.CustomEvent("ll:transaction-received", {
          detail: {
            data: movement(
              "txn_inside",
              "Movement Inside Window",
              new Date(new Date(bounds.from).getTime() + 60_000).toISOString(),
            ),
          },
        }),
      );
      await tick();

      const body = h.root.querySelector('[data-hook="ledger-body"]').textContent;
      assert.doesNotMatch(body, /Movement Before Window/);
      assert.match(body, /Movement Inside Window/);
      cleanup();
    } finally {
      h.cleanup();
    }
  });

  await t.test("cleanup stops the date inputs from querying", async () => {
    const h = mountHarness();
    try {
      const cleanup = mountTransactions(h.root, "shop_1");
      await tick();
      const before = h.ledgerRequests().length;

      cleanup();
      const fromInput = h.root.querySelector('[data-hook="filter-from"]');
      fromInput.value = "2026-10-14";
      fromInput.dispatchEvent(new MockEvent("change"));
      await tick();

      assert.equal(h.ledgerRequests().length, before);
    } finally {
      h.cleanup();
    }
  });
});
