import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  createCustomer,
  fetchCustomer,
  fetchCustomerDebts,
  fetchCustomers,
  fetchShopCreditSummary,
  recordCustomerDebt,
  updateCustomer,
} from "../dist/api.js";
import { mountHome } from "../dist/views/home.js";
import {
  showCustomerBookModal,
  showCustomerHistoryModal,
  showRecordDebtModal,
  showRegisterCustomerModal,
} from "../dist/views/customers.js";

const HOME_HTML = fs.readFileSync(
  new URL("../src/components/home.html", import.meta.url),
  "utf8"
);

test("Customer Credit & Debt Book Suite", async (t) => {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  await t.test("API Client: customer endpoints dispatch correct requests and parse responses", async () => {
    let lastUrl = "";
    let lastMethod = "";
    let lastBody = null;

    globalThis.fetch = async (url, init = {}) => {
      lastUrl = String(url);
      lastMethod = init.method || "GET";
      lastBody = init.body ? JSON.parse(String(init.body)) : null;

      if (lastUrl.includes("/api/customers/credit-summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            total_customers_with_debt: 2,
            total_outstanding_debt: { amount: 1500000, currency: "NGN" },
            today_credit_sales_amount: { amount: 500000, currency: "NGN" },
            today_repayments_amount: { amount: 200000, currency: "NGN" },
          }),
        };
      }

      if (lastUrl.includes("/debts") && lastMethod === "POST") {
        return {
          ok: true,
          status: 201,
          json: async () => ({
            debt: {
              id: "dbt_100",
              shop_id: "shop_test",
              customer_id: "cus_001",
              type: lastBody.type,
              amount: lastBody.amount,
              balance_after: { amount: 800000, currency: "NGN" },
              description: lastBody.description,
              occurred_at: new Date().toISOString(),
              created_by: "owner",
              created_at: new Date().toISOString(),
            },
            customer: {
              id: "cus_001",
              shop_id: "shop_test",
              name: "Musa",
              phone: "0801",
              notes: "",
              outstanding_balance: { amount: 800000, currency: "NGN" },
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
            },
          }),
        };
      }

      if (lastUrl.includes("/debts") && lastMethod === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            customer_id: "cus_001",
            debts: [
              {
                id: "dbt_001",
                shop_id: "shop_test",
                customer_id: "cus_001",
                type: "credit_sale",
                amount: { amount: 1000000, currency: "NGN" },
                balance_after: { amount: 1000000, currency: "NGN" },
                description: "5 packs noodles",
                occurred_at: new Date().toISOString(),
                created_by: "owner",
                created_at: new Date().toISOString(),
              },
            ],
            count: 1,
            total: 1,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.endsWith("/api/customers") && lastMethod === "POST") {
        return {
          ok: true,
          status: 201,
          json: async () => ({
            id: "cus_001",
            shop_id: "shop_test",
            name: lastBody.name,
            phone: lastBody.phone || "",
            notes: lastBody.notes || "",
            outstanding_balance: lastBody.initial_debt
              ? lastBody.initial_debt
              : { amount: 0, currency: "NGN" },
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.includes("/api/customers/cus_001") && lastMethod === "PUT") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            id: "cus_001",
            shop_id: "shop_test",
            name: lastBody.name,
            phone: lastBody.phone || "",
            notes: lastBody.notes || "",
            outstanding_balance: { amount: 0, currency: "NGN" },
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.includes("/api/customers/cus_001") && lastMethod === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            id: "cus_001",
            shop_id: "shop_test",
            name: "Musa",
            phone: "0801",
            notes: "Neighbour",
            outstanding_balance: { amount: 0, currency: "NGN" },
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          }),
        };
      }

      if (lastUrl.includes("/api/customers") && lastMethod === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            customers: [
              {
                id: "cus_001",
                shop_id: "shop_test",
                name: "Alhaji Musa",
                phone: "08012345678",
                notes: "Stall 4",
                outstanding_balance: { amount: 1500000, currency: "NGN" },
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
              },
            ],
            count: 1,
            total: 1,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      return { ok: true, status: 200, json: async () => ({}) };
    };

    // 1. fetchShopCreditSummary
    const summary = await fetchShopCreditSummary();
    assert.equal(summary.total_customers_with_debt, 2);
    assert.equal(summary.total_outstanding_debt.amount, 1500000);

    // 2. createCustomer
    const created = await createCustomer({
      name: "Alhaji Musa",
      phone: "08012345678",
      notes: "Stall 4",
      initial_debt: { amount: 500000, currency: "NGN" },
    });
    assert.equal(lastMethod, "POST");
    assert.equal(created.name, "Alhaji Musa");
    assert.equal(created.outstanding_balance.amount, 500000);

    // 3. fetchCustomers
    const list = await fetchCustomers({ search: "Musa", has_debt: true });
    assert.ok(lastUrl.includes("search=Musa"));
    assert.ok(lastUrl.includes("has_debt=true"));
    assert.equal(list.total, 1);
    assert.equal(list.customers[0].name, "Alhaji Musa");

    // 4. fetchCustomer
    const fetched = await fetchCustomer("cus_001");
    assert.equal(fetched.name, "Musa");

    // 5. updateCustomer
    const updated = await updateCustomer("cus_001", {
      name: "Alhaji Musa Senior",
      phone: "08099998888",
    });
    assert.equal(lastMethod, "PUT");
    assert.equal(updated.name, "Alhaji Musa Senior");

    // 6. recordCustomerDebt
    const debtRes = await recordCustomerDebt("cus_001", {
      type: "repayment",
      amount: { amount: 200000, currency: "NGN" },
      description: "Cash paid at till",
    });
    assert.equal(lastMethod, "POST");
    assert.equal(debtRes.debt.type, "repayment");
    assert.equal(debtRes.customer.outstanding_balance.amount, 800000);

    // 7. fetchCustomerDebts
    const debtsRes = await fetchCustomerDebts("cus_001");
    assert.equal(debtsRes.total, 1);
    assert.equal(debtsRes.debts[0].amount.amount, 1000000);
  });

  await t.test("Home View: mounts and renders Customer Debt KPI and opens Customer Book modal", async () => {
    globalThis.fetch = async (url, init = {}) => {
      const u = String(url);
      const method = init.method || "GET";

      if (u.includes("/api/transactions")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            transactions: [],
            count: 0,
            limit: 50,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            products: [],
            count: 0,
            total: 0,
            low_stock_count: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/api/customers/credit-summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            total_customers_with_debt: 3,
            total_outstanding_debt: { amount: 4850000, currency: "NGN" }, // 48,500 NGN
            today_credit_sales_amount: { amount: 1200000, currency: "NGN" },
            today_repayments_amount: { amount: 500000, currency: "NGN" },
          }),
        };
      }

      if (u.includes("/api/customers")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            customers: [
              {
                id: "cus_01",
                shop_id: "shop_test",
                name: "Mama Tolu",
                phone: "08031234567",
                notes: "Stall 14",
                outstanding_balance: { amount: 4850000, currency: "NGN" },
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
              },
            ],
            count: 1,
            total: 1,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            today_sales_amount: { amount: 15000000, currency: "NGN" },
            today_sales_count: 10,
            pending_reviews: 0,
            low_stock_count: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      return { ok: true, status: 200, json: async () => ({}) };
    };

    const container = document.createElement("div");
    container.innerHTML = HOME_HTML;
    document.body.append(container);

    const cleanup = mountHome(container, "shop_test");

    // Wait for initial refetch
    await new Promise((r) => setTimeout(r, 20));

    // Check Customer Debt KPI
    const debtKpi = container.querySelector('[data-hook="kpi-customer-debt"]');
    assert.ok(debtKpi, "expected kpi-customer-debt element to exist");
    assert.match(debtKpi.textContent, /48,500/);

    // Click on Customer Book header button
    const bookBtn = container.querySelector('[data-hook="customer-book-btn"]');
    assert.ok(bookBtn, "expected customer-book-btn to exist");
    bookBtn.dispatchEvent(new MockEvent("click"));

    // Verify Customer Book modal opened
    const bookModal = document.querySelector(".modal--customer-book");
    assert.ok(bookModal, "expected .modal--customer-book to be mounted");

    // Wait for customer data load in modal
    await new Promise((r) => setTimeout(r, 25));

    // Verify summary values rendered
    const owingCount = bookModal.querySelector('[data-hook="summary-owing-count"]');
    const totalDebt = bookModal.querySelector('[data-hook="summary-total-debt"]');
    assert.ok(owingCount);
    assert.ok(totalDebt);
    assert.equal(owingCount.textContent, "3");
    assert.match(totalDebt.textContent, /48,500/);

    // Verify customer row
    const row = bookModal.querySelector('tr[data-customer-id="cus_01"]');
    assert.ok(row, "expected row for customer cus_01");
    assert.match(row.textContent, /Mama Tolu/);
    assert.match(row.textContent, /48,500/);

    cleanup();
  });

  await t.test("Customer Book: Register customer modal and debt movements", async () => {
    let customerCreated = false;
    let debtRecorded = false;

    globalThis.fetch = async (url, init = {}) => {
      const u = String(url);
      const method = init.method || "GET";

      if (u.endsWith("/api/customers") && method === "POST") {
        customerCreated = true;
        const body = JSON.parse(String(init.body));
        return {
          ok: true,
          status: 201,
          json: async () => ({
            id: "cus_new_123",
            shop_id: "shop_test",
            name: body.name,
            phone: body.phone,
            notes: body.notes,
            outstanding_balance: { amount: 0, currency: "NGN" },
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          }),
        };
      }

      if (u.includes("/debts") && method === "POST") {
        debtRecorded = true;
        const body = JSON.parse(String(init.body));
        return {
          ok: true,
          status: 201,
          json: async () => ({
            debt: {
              id: "dbt_999",
              shop_id: "shop_test",
              customer_id: "cus_test",
              type: body.type,
              amount: body.amount,
              balance_after: { amount: 500000, currency: "NGN" },
              description: body.description,
              occurred_at: new Date().toISOString(),
              created_by: "owner",
              created_at: new Date().toISOString(),
            },
            customer: {
              id: "cus_test",
              shop_id: "shop_test",
              name: "Emeka",
              phone: "080",
              notes: "",
              outstanding_balance: { amount: 500000, currency: "NGN" },
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
            },
          }),
        };
      }

      if (u.includes("/debts") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            customer_id: "cus_test",
            debts: [
              {
                id: "dbt_1",
                shop_id: "shop_test",
                customer_id: "cus_test",
                type: "credit_sale",
                amount: { amount: 500000, currency: "NGN" },
                balance_after: { amount: 500000, currency: "NGN" },
                description: "Carton of biscuits on credit",
                occurred_at: new Date().toISOString(),
                created_by: "owner",
                created_at: new Date().toISOString(),
              },
            ],
            count: 1,
            total: 1,
            limit: 50,
            offset: 0,
            generated_at: new Date().toISOString(),
          }),
        };
      }

      return { ok: true, status: 200, json: async () => ({}) };
    };

    // 1. Test Register Customer modal
    let registerDone = false;
    showRegisterCustomerModal(() => {
      registerDone = true;
    });

    const regModal = document.querySelector(".modal--edit-confirm");
    assert.ok(regModal, "register modal should be mounted");

    const nameInput = regModal.querySelector('[data-hook="new-customer-name"]');
    const phoneInput = regModal.querySelector('[data-hook="new-customer-phone"]');
    const form = regModal.querySelector("form");

    assert.ok(nameInput);
    assert.ok(phoneInput);
    assert.ok(form);

    nameInput.value = "Brother Joseph";
    phoneInput.value = "08055554444";
    form.dispatchEvent(new MockEvent("submit"));

    await new Promise((r) => setTimeout(r, 20));
    assert.equal(customerCreated, true, "customer should have been created");
    assert.equal(registerDone, true, "onDone callback should have fired");

    // 2. Test Record Debt modal
    const testCustomer = {
      id: "cus_test",
      shop_id: "shop_test",
      name: "Emeka",
      phone: "080",
      notes: "",
      outstanding_balance: { amount: 0, currency: "NGN" },
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };

    let debtDone = false;
    showRecordDebtModal(testCustomer, "credit_sale", () => {
      debtDone = true;
    });

    const debtModal = document.querySelector(".modal--edit-confirm");
    assert.ok(debtModal);
    const amountInput = debtModal.querySelector('[data-hook="debt-amount-input"]');
    const descInput = debtModal.querySelector('[data-hook="debt-desc-input"]');
    const debtForm = debtModal.querySelector("form");

    amountInput.value = "5000"; // 5,000 NGN
    descInput.value = "Carton of biscuits on credit";
    debtForm.dispatchEvent(new MockEvent("submit"));

    await new Promise((r) => setTimeout(r, 20));
    assert.equal(debtRecorded, true, "debt should have been recorded");
    assert.equal(debtDone, true, "onDone callback should have fired");

    // 3. Test Customer History modal
    showCustomerHistoryModal(testCustomer);
    await new Promise((r) => setTimeout(r, 25));

    const historyItems = document.querySelectorAll(".customer-debt-item");
    assert.equal(historyItems.length, 1);
    assert.match(historyItems[0].textContent, /Carton of biscuits on credit/);
  });
});
