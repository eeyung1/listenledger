import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  computeKPIs,
  formatExpenseCategory,
  formatMoney,
} from "../dist/format.js";
import {
  createExpense,
  fetchExpenses,
  deleteExpense,
} from "../dist/api.js";
import { mountHome } from "../dist/views/home.js";

const HOME_HTML = fs.readFileSync(
  new URL("../src/components/home.html", import.meta.url),
  "utf8"
);

test("Operational Expenses & Net Profit Suite", async (t) => {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  await t.test("formatExpenseCategory returns expected human-readable text", () => {
    assert.equal(formatExpenseCategory("generator_fuel"), "Generator Fuel & Oil");
    assert.equal(formatExpenseCategory("store_rent"), "Store Rent & Rates");
    assert.equal(formatExpenseCategory("transport_logistics"), "Transport & Haulage");
    assert.equal(formatExpenseCategory("packaging_supplies"), "Packaging & Bags");
    assert.equal(formatExpenseCategory("staff_wages"), "Staff Daily Wages");
    assert.equal(formatExpenseCategory("utilities"), "Power & Water Utilities");
    assert.equal(formatExpenseCategory("repairs"), "Repairs & Maintenance");
    assert.equal(formatExpenseCategory("other"), "Other Operational Cost");
  });

  await t.test("computeKPIs calculates expenses and net profit from DailySummary", () => {
    const summary = {
      shop_id: "shop_1",
      currency: "NGN",
      today_sales_amount: { amount: 3000000, currency: "NGN" }, // 30,000 NGN
      today_sales_count: 5,
      today_expenses_amount: { amount: 600000, currency: "NGN" }, // 6,000 NGN
      today_expenses_count: 2,
      today_net_profit: { amount: 2400000, currency: "NGN" }, // 24,000 NGN
      pending_reviews: 1,
      low_stock_count: 3,
      generated_at: new Date().toISOString(),
    };

    const kpis = computeKPIs([], [], summary);
    assert.equal(kpis.revenue.amount, 3000000);
    assert.equal(kpis.salesCount, 5);
    assert.equal(kpis.expenses.amount, 600000);
    assert.equal(kpis.expensesCount, 2);
    assert.equal(kpis.netProfit.amount, 2400000);
    assert.equal(kpis.reviewCount, 1);
    assert.equal(kpis.lowStockCount, 3);
  });

  await t.test("computeKPIs calculates expenses and net profit from local lists fallback", () => {
    const today = new Date().toISOString();
    const transactions = [
      {
        id: "txn_001",
        shop_id: "shop_1",
        type: "sale",
        product_name: "Milo",
        quantity: 2,
        unit: "pack",
        amount: { amount: 1000000, currency: "NGN" }, // 10,000 NGN
        confidence: { overall: 0.95 },
        status: "confirmed",
        occurred_at: today,
        created_at: today,
      },
    ];

    const expenses = [
      {
        id: "exp_001",
        shop_id: "shop_1",
        category: "generator_fuel",
        amount: { amount: 350000, currency: "NGN" }, // 3,500 NGN
        description: "Fuel for morning counter run",
        occurred_at: today,
        created_by: "owner",
        created_at: today,
      },
    ];

    const kpis = computeKPIs(transactions, [], undefined, expenses);
    assert.equal(kpis.revenue.amount, 1000000);
    assert.equal(kpis.expenses.amount, 350000);
    assert.equal(kpis.expensesCount, 1);
    // Net profit = 1,000,000 - 350,000 = 650,000 kobo (6,500 NGN)
    assert.equal(kpis.netProfit.amount, 650000);
  });

  await t.test("API Client dispatches createExpense, fetchExpenses, deleteExpense", async () => {
    let lastUrl = "";
    let lastMethod = "";
    let lastBody = null;
    let lastHeaders = null;

    globalThis.fetch = async (url, init) => {
      lastUrl = String(url);
      lastMethod = init?.method ?? "GET";
      lastBody = init?.body ? JSON.parse(String(init.body)) : null;
      lastHeaders = init?.headers ?? {};

      if (lastUrl.includes("/api/expenses") && lastMethod === "POST") {
        return {
          ok: true,
          status: 201,
          json: async () => ({
            id: "exp_test_123",
            shop_id: "shop_test",
            category: lastBody.category,
            amount: { amount: lastBody.amount.amount, currency: "NGN" },
            description: lastBody.description,
            receipt_ref: lastBody.receipt_ref ?? "",
            occurred_at: new Date().toISOString(),
            created_by: "owner",
            created_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.includes("/api/expenses") && lastMethod === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            expenses: [],
            count: 0,
            total: 0,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.includes("/api/expenses/exp_test_123") && lastMethod === "DELETE") {
        return {
          ok: true,
          status: 200,
          json: async () => ({ deleted: true, id: "exp_test_123" }),
        };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    // 1. Create expense
    const created = await createExpense({
      category: "generator_fuel",
      amount: { amount: 500000 },
      description: "10L fuel",
      receipt_ref: "SLIP-12",
    });
    assert.equal(created.id, "exp_test_123");
    assert.equal(lastMethod, "POST");
    assert.equal(lastBody.amount.amount, 500000);

    // 2. Fetch expenses
    const list = await fetchExpenses({ category: "generator_fuel", limit: 20 });
    assert.equal(lastMethod, "GET");
    assert.match(lastUrl, /category=generator_fuel/);
    assert.match(lastUrl, /limit=20/);

    // 3. Delete expense
    const del = await deleteExpense("exp_test_123");
    assert.equal(del.deleted, true);
    assert.equal(lastMethod, "DELETE");
    assert.equal(lastHeaders["X-ListenLedger-Request"], "1");
  });

  await t.test("Home Overview: Renders Expense/Profit KPIs and Modal Interaction", async () => {
    let expenseCreated = false;
    let createdPayload = null;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/transactions") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            transactions: [],
            count: 0,
            total: 0,
            limit: 50,
          }),
        };
      }

      if (u.includes("/api/inventory") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            products: [],
            count: 0,
            total: 0,
          }),
        };
      }

      if (u.includes("/api/summary") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            today_sales_amount: { amount: 1500000, currency: "NGN" },
            today_sales_count: 3,
            today_expenses_amount: { amount: 450000, currency: "NGN" },
            today_expenses_count: 1,
            today_net_profit: { amount: 1050000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/api/onboarding/status") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            is_complete: true,
            completed_steps: 6,
            total_steps: 6,
            steps: {
              profile: true,
              catalogue: true,
              devices: true,
              staff_training: true,
              target_gates: true,
              test_capture: true,
            },
          }),
        };
      }

      if (u.includes("/api/expenses") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            expenses: [],
            count: 0,
            total: 0,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/api/expenses") && method === "POST") {
        expenseCreated = true;
        createdPayload = JSON.parse(String(init.body));
        return {
          ok: true,
          status: 201,
          json: async () => ({
            id: "exp_modal_99",
            shop_id: "shop_test",
            category: createdPayload.category,
            amount: { amount: createdPayload.amount.amount, currency: "NGN" },
            description: createdPayload.description,
            occurred_at: new Date().toISOString(),
            created_by: "owner",
            created_at: new Date().toISOString(),
          }),
        };
      }

      return {
        ok: true,
        status: 200,
        json: async () => ({}),
      };
    };

    const container = document.createElement("div");
    container.innerHTML = HOME_HTML;
    document.body.append(container);

    const cleanup = mountHome(container, "shop_test");

    // Wait for initial refetch
    await new Promise((r) => setTimeout(r, 20));

    // Verify rendered KPIs
    const expensesVal = container.querySelector('[data-hook="kpi-expenses-today"]');
    const profitVal = container.querySelector('[data-hook="kpi-net-profit"]');
    assert.ok(expensesVal);
    assert.ok(profitVal);
    assert.match(expensesVal.textContent, /4,500/);
    assert.match(profitVal.textContent, /10,500/);

    // Open Record Expense modal
    const recordBtn = container.querySelector('[data-hook="record-expense-btn"]');
    assert.ok(recordBtn);
    recordBtn.dispatchEvent(new MockEvent("click"));

    const modal = document.querySelector(".modal--record-expense");
    assert.ok(modal, "expected expense modal to be in DOM");

    const catSelect = modal.querySelector('[data-hook="expense-category-select"]');
    const amountInput = modal.querySelector('[data-hook="expense-amount-input"]');
    const descInput = modal.querySelector('[data-hook="expense-desc-input"]');
    const form = modal.querySelector("form");

    assert.ok(catSelect);
    assert.ok(amountInput);
    assert.ok(descInput);
    assert.ok(form);

    catSelect.value = "generator_fuel";
    amountInput.value = "6000"; // 6,000 NGN -> 600,000 kobo
    descInput.value = "12L Petrol for night operations";

    form.dispatchEvent(new MockEvent("submit"));

    await new Promise((r) => setTimeout(r, 20));

    assert.equal(expenseCreated, true, "expected expense creation request");
    assert.equal(createdPayload.category, "generator_fuel");
    assert.equal(createdPayload.amount.amount, 600000);
    assert.equal(createdPayload.description, "12L Petrol for night operations");

    cleanup();
  });
});
