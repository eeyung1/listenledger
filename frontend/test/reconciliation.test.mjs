import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountSettings } from "../dist/views/settings.js";

const SETTINGS_HTML = fs.readFileSync(
  new URL("../src/components/settings.html", import.meta.url),
  "utf8"
);

test("Pilot Dual-Entry Reconciliation, Discrepancy Investigation & Cohort Progression Suite", async (t) => {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
  });

  await t.test("1. Initial Mount: Cohort Checklist & Reconciliation List Rendering", async () => {
    let cohortFetched = false;
    let readinessFetched = false;
    let reconciliationsFetched = false;

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/settings") && !u.includes("/target-gates") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop: {
              id: "shop_test_1",
              name: "Mama Tolu Provisions",
              currency: "NGN",
              timezone: "Africa/Lagos",
              confidence_threshold: 0.85,
              sale_phrase: "take am",
              restock_phrase: "drop am",
            },
            devices: [],
          }),
        };
      }

      if (u.endsWith("/api/settings/target-gates") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            is_agreed: true,
            gates: {
              min_full_transaction_accuracy: 90.0,
              min_sku_accuracy: 95.0,
              min_quantity_accuracy: 95.0,
              min_unit_accuracy: 95.0,
              min_amount_accuracy: 95.0,
              max_false_capture_rate: 3.0,
              max_missed_transaction_rate: 5.0,
              max_correction_rate: 10.0,
              min_protocol_compliance_rate: 85.0,
              max_p95_latency_ms: 1500,
              agreed_at: "2026-10-15T08:00:00Z",
              agreed_by: "Mama Tolu",
            },
          }),
        };
      }

      if (u.endsWith("/api/pilot/cohort") && method === "GET") {
        cohortFetched = true;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test_1",
            cohort_phase: "phase1_controlled",
            stage_status: "active",
            notes: "Mama Tolu Provisions - Phase 1 controlled shop trial",
          }),
        };
      }

      if (u.endsWith("/api/pilot/cohort/evaluate-readiness") && method === "POST") {
        readinessFetched = true;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test_1",
            current_phase: "phase1_controlled",
            next_phase: "phase2_dual_shop",
            readiness_status: "blocked",
            checks: [
              {
                requirement: "Formally Agreed Target Gates",
                satisfied: true,
                details: "Agreed by Mama Tolu on 2026-10-15T08:00:00Z",
              },
              {
                requirement: "Cashier Training Protocol Qualified",
                satisfied: true,
                details: "Cashier protocol drill certified on 2026-10-15T08:15:00Z",
              },
              {
                requirement: "Active Trial Discrepancies Investigated",
                satisfied: false,
                details: "1 unexplained discrepancies require validated root cause attribution",
              },
            ],
            all_checks_satisfied: false,
          }),
        };
      }

      if (u.endsWith("/api/pilot/reconciliations") && method === "GET") {
        reconciliationsFetched = true;
        return {
          ok: true,
          status: 200,
          json: async () => ({
            reconciliations: [
              {
                id: "rec_sess_01",
                shop_id: "shop_test_1",
                status: "discrepant",
                period_start: "2026-10-15T08:00:00Z",
                period_end: "2026-10-15T12:00:00Z",
                independent_source: "Paper Counter Register",
                total_independent_records: 20,
                total_captured_transactions: 19,
                matched_records: 19,
                missed_captures: 1,
                false_captures: 0,
                price_discrepancies: 0,
                quantity_discrepancies: 0,
                total_independent_revenue: 5000000,
                total_captured_revenue: 4750000,
                revenue_discrepancy: 250000,
                stock_discrepancy_count: 0,
                conducted_by: "Auditor Chukwudi",
                created_at: "2026-10-15T12:15:00Z",
              },
            ],
          }),
        };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = SETTINGS_HTML;
    document.body.append(root);

    const cleanup = mountSettings(root, "shop_test_1");
    await new Promise((r) => setTimeout(r, 40));

    assert.ok(cohortFetched, "Cohort fetched on mount");
    assert.ok(readinessFetched, "Readiness fetched on mount");
    assert.ok(reconciliationsFetched, "Reconciliations fetched on mount");

    // Verify Cohort Phase badge
    const cohortBadge = root.querySelector('[data-hook="cohort-phase-badge"]');
    assert.ok(cohortBadge);
    assert.equal(cohortBadge.textContent, "Phase 1: Controlled Trial");

    // Verify Readiness Badge & Checks
    const readinessBadge = root.querySelector('[data-hook="cohort-readiness-badge"]');
    assert.ok(readinessBadge);
    assert.equal(readinessBadge.textContent, "Expansion Blocked");

    const checksList = root.querySelector('[data-hook="cohort-checks-list"]');
    assert.ok(checksList);
    assert.equal(checksList.children.length, 3);
    assert.match(checksList.children[0].textContent, /Formally Agreed Target Gates/);
    assert.match(checksList.children[2].textContent, /1 unexplained discrepancies require validated root cause attribution/);

    // Verify Reconciliation Item rendered
    const recList = root.querySelector('[data-hook="reconciliation-list"]');
    assert.ok(recList);
    assert.equal(recList.children.length, 1);
    assert.match(recList.children[0].textContent, /Paper Counter Register/);
    assert.match(recList.children[0].textContent, /Discrepant/);
    assert.match(recList.children[0].textContent, /Auditor Chukwudi/);

    cleanup();
  });

  await t.test("2. Run Dual-Entry Reconciliation Form & Update Audit Results", async () => {
    let lastReconcilePayload = null;
    let mockReconciliations = [];

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/settings") && !u.includes("/target-gates")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop: { id: "shop_test_1", name: "Mama Tolu Provisions", currency: "NGN", timezone: "Africa/Lagos", confidence_threshold: 0.85 },
            devices: [],
          }),
        };
      }
      if (u.endsWith("/api/settings/target-gates")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ is_agreed: true, gates: { min_full_transaction_accuracy: 90.0 } }),
        };
      }
      if (u.endsWith("/api/pilot/cohort")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ shop_id: "shop_test_1", cohort_phase: "phase1_controlled", stage_status: "active", notes: "" }),
        };
      }
      if (u.endsWith("/api/pilot/cohort/evaluate-readiness")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test_1",
            current_phase: "phase1_controlled",
            next_phase: "phase2_dual_shop",
            readiness_status: "qualified",
            checks: [{ requirement: "Dual Entry Ledger", satisfied: true, details: "Reconciled with 0 unexplained" }],
            all_checks_satisfied: true,
          }),
        };
      }
      if (u.endsWith("/api/pilot/reconciliations") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({ reconciliations: mockReconciliations }),
        };
      }
      if (u.endsWith("/api/pilot/reconcile") && method === "POST") {
        lastReconcilePayload = JSON.parse(String(init.body));
        const newRec = {
          id: "rec_perfect_01",
          shop_id: "shop_test_1",
          status: "reconciled",
          period_start: lastReconcilePayload.period_start,
          period_end: lastReconcilePayload.period_end,
          independent_source: lastReconcilePayload.independent_source,
          total_independent_records: lastReconcilePayload.independent_records.length,
          total_captured_transactions: lastReconcilePayload.independent_records.length,
          matched_records: lastReconcilePayload.independent_records.length,
          missed_captures: 0,
          false_captures: 0,
          price_discrepancies: 0,
          quantity_discrepancies: 0,
          total_independent_revenue: 250000,
          total_captured_revenue: 250000,
          revenue_discrepancy: 0,
          stock_discrepancy_count: 0,
          conducted_by: lastReconcilePayload.conducted_by,
          created_at: "2026-10-15T12:00:00Z",
        };
        mockReconciliations.unshift(newRec);
        return {
          ok: true,
          status: 200,
          json: async () => ({
            reconciliation: newRec,
            gate_results: [{ metric: "Full Transaction Accuracy", target: ">= 90.0%", achieved: 100.0, passed: true }],
            all_gates_passed: true,
          }),
        };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = SETTINGS_HTML;
    document.body.append(root);

    const cleanup = mountSettings(root, "shop_test_1");
    await new Promise((r) => setTimeout(r, 40));

    // Fill the dual-entry reconciliation form
    const recSourceInput = root.querySelector('[data-hook="rec-source"]');
    recSourceInput.value = "Independent Verification Register";

    const recAuditorInput = root.querySelector('[data-hook="rec-auditor"]');
    recAuditorInput.value = "Auditor Amaka";

    const recRecordsInput = root.querySelector('[data-hook="rec-records-input"]');
    recRecordsInput.value = JSON.stringify([
      {
        occurred_at: "2026-10-15T10:00:00Z",
        sku_or_name: "MILO-500",
        type: "sale",
        quantity: 1,
        unit: "pack",
        amount: 250000,
      },
    ]);

    const recStockInput = root.querySelector('[data-hook="rec-stock-input"]');
    recStockInput.value = JSON.stringify([
      {
        sku_or_name: "MILO-500",
        physical_count: 49,
      },
    ]);

    const form = root.querySelector('[data-hook="reconciliation-form"]');
    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 40));

    assert.ok(lastReconcilePayload, "Reconcile endpoint called with payload");
    assert.equal(lastReconcilePayload.independent_source, "Independent Verification Register");
    assert.equal(lastReconcilePayload.conducted_by, "Auditor Amaka");
    assert.equal(lastReconcilePayload.independent_records.length, 1);
    assert.equal(lastReconcilePayload.physical_stock_counts.length, 1);

    const statusMsg = root.querySelector('[data-hook="reconcile-status"]');
    assert.ok(statusMsg);
    assert.match(statusMsg.textContent, /All target gates passed!/);

    const recList = root.querySelector('[data-hook="reconciliation-list"]');
    assert.equal(recList.children.length, 1);
    assert.match(recList.children[0].textContent, /Independent Verification Register/);
    assert.match(recList.children[0].textContent, /Reconciled/);

    cleanup();
  });

  await t.test("3. Discrepancy Investigation Workflow & Cohort Promotion", async () => {
    let mockSession = {
      id: "rec_sess_disc",
      shop_id: "shop_test_1",
      status: "discrepant",
      period_start: "2026-10-15T08:00:00Z",
      period_end: "2026-10-15T12:00:00Z",
      independent_source: "Paper Ledger",
      total_independent_records: 10,
      total_captured_transactions: 9,
      matched_records: 9,
      missed_captures: 1,
      false_captures: 0,
      price_discrepancies: 0,
      quantity_discrepancies: 0,
      total_independent_revenue: 1000000,
      total_captured_revenue: 900000,
      revenue_discrepancy: 100000,
      stock_discrepancy_count: 0,
      conducted_by: "Auditor",
      created_at: "2026-10-15T12:00:00Z",
    };

    let investigatedDiscrepancyId = null;
    let investigatedPayload = null;
    let promoteCalls = [];

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/settings") && !u.includes("/target-gates")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop: { id: "shop_test_1", name: "Shop", currency: "NGN", timezone: "Africa/Lagos", confidence_threshold: 0.85 },
            devices: [],
          }),
        };
      }
      if (u.endsWith("/api/settings/target-gates")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ is_agreed: true, gates: { min_full_transaction_accuracy: 90.0 } }),
        };
      }
      if (u.endsWith("/api/pilot/cohort")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test_1",
            cohort_phase: promoteCalls.length > 0 ? "phase2_dual_shop" : "phase1_controlled",
            stage_status: promoteCalls.length > 0 ? "qualified" : "active",
            notes: "",
          }),
        };
      }
      if (u.endsWith("/api/pilot/reconciliations") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({ reconciliations: [mockSession] }),
        };
      }
      if (u.endsWith("/api/pilot/reconciliations/rec_sess_disc") && method === "GET") {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            ...mockSession,
            discrepancies: [
              {
                id: "disc_001",
                reconciliation_id: "rec_sess_disc",
                shop_id: "shop_test_1",
                discrepancy_type: "missed_capture",
                product_name: "Peak Milk",
                expected_quantity: 1,
                actual_quantity: 0,
                expected_amount: 100000,
                actual_amount: 0,
                investigation_status: "unexplained",
                root_cause: "",
                investigation_notes: "",
              },
            ],
          }),
        };
      }
      if (u.includes("/api/pilot/discrepancies/disc_001/investigate") && method === "POST") {
        investigatedDiscrepancyId = "disc_001";
        investigatedPayload = JSON.parse(String(init.body));
        mockSession.status = "reconciled";
        return {
          ok: true,
          status: 200,
          json: async () => ({ discrepancy_id: "disc_001" }),
        };
      }
      if (u.endsWith("/api/pilot/cohort/evaluate-readiness") && method === "POST") {
        const body = JSON.parse(String(init.body));
        if (body.promote) {
          promoteCalls.push(body.promote);
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test_1",
            current_phase: promoteCalls.length > 0 ? "phase2_dual_shop" : "phase1_controlled",
            next_phase: "phase3_expanded_5",
            readiness_status: "qualified",
            checks: [
              { requirement: "Target Gates", satisfied: true, details: "Agreed" },
              { requirement: "Cashier Training", satisfied: true, details: "Certified" },
              { requirement: "Zero Unexplained Discrepancies", satisfied: true, details: "All discrepancies investigated" },
            ],
            all_checks_satisfied: true,
          }),
        };
      }

      return { ok: false, status: 404, json: async () => ({ error: "not found" }) };
    };

    let promptCount = 0;
    window.prompt = () => {
      promptCount++;
      if (promptCount === 1) return "acoustic_noise";
      return "Generator was running loudly during peak hours causing boundary miss";
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = SETTINGS_HTML;
    document.body.append(root);

    const cleanup = mountSettings(root, "shop_test_1");
    await new Promise((r) => setTimeout(r, 40));

    // Inspect & Resolve Discrepancies button
    const recList = root.querySelector('[data-hook="reconciliation-list"]');
    const inspectBtn = recList.querySelector(".button--secondary");
    assert.ok(inspectBtn);
    assert.equal(inspectBtn.textContent, "Inspect & Resolve Discrepancies");

    inspectBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 40));

    assert.equal(investigatedDiscrepancyId, "disc_001");
    assert.equal(investigatedPayload.root_cause, "acoustic_noise");
    assert.equal(investigatedPayload.investigation_notes, "Generator was running loudly during peak hours causing boundary miss");

    // Click "Evaluate Readiness & Promote"
    const evalBtn = root.querySelector('[data-hook="evaluate-cohort-btn"]');
    assert.ok(evalBtn);
    evalBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 40));

    assert.equal(promoteCalls.length, 1, "evaluateCohortReadiness called with promote=true");
    assert.equal(promoteCalls[0], true);
    const cohortBadge = root.querySelector('[data-hook="cohort-phase-badge"]');
    assert.equal(cohortBadge.textContent, "Phase 2: 1–2 Shops");

    cleanup();
  });

  await t.test("4. P4.4 recognition language populates and is saved from the settings form", async () => {
    let patchedPayload = null;
    const shop = {
      id: "shop_test_1",
      name: "Mama Tolu Provisions",
      currency: "NGN",
      timezone: "Africa/Lagos",
      confidence_threshold: 0.85,
      sale_phrase: "take am",
      restock_phrase: "drop am",
      recognition_language: "en-NG",
    };

    globalThis.fetch = async (url, init) => {
      const u = String(url);
      const method = init?.method ?? "GET";

      if (u.includes("/api/settings/target-gates")) {
        return { ok: true, status: 200, json: async () => ({ is_agreed: false, gates: {} }) };
      }
      if (u.includes("/api/settings")) {
        if (method === "PATCH") {
          patchedPayload = JSON.parse(String(init.body));
          return { ok: true, status: 200, json: async () => ({ ...shop, ...patchedPayload }) };
        }
        return { ok: true, status: 200, json: async () => ({ shop, devices: [] }) };
      }
      if (u.includes("/api/pilot/cohort/evaluate-readiness")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ readiness_status: "blocked", checks: [] }),
        };
      }
      if (u.includes("/api/pilot/cohort")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ cohort_phase: "phase1_controlled", stage_status: "criteria_pending" }),
        };
      }
      if (u.includes("/api/pilot/reconciliations")) {
        return { ok: true, status: 200, json: async () => ({ reconciliations: [] }) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    const root = document.createElement("div");
    root.id = "app";
    root.innerHTML = SETTINGS_HTML;
    document.body.append(root);

    const cleanup = mountSettings(root, "shop_test_1");
    await new Promise((r) => setTimeout(r, 40));

    const langInput = root.querySelector('[data-hook="setting-recognition-language"]');
    assert.ok(langInput, "recognition language field must be mounted");
    assert.equal(langInput.value, "en-NG", "form is populated from the shop setting");

    langInput.value = "yo-NG";
    const form = root.querySelector('[data-hook="settings-form"]');
    form.dispatchEvent(new MockEvent("submit"));
    await new Promise((r) => setTimeout(r, 40));

    assert.ok(patchedPayload, "settings PATCH must be sent");
    assert.equal(patchedPayload.recognition_language, "yo-NG", "PATCH carries the recognition language");

    cleanup();
  });
});
