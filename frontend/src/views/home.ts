/**
 * Overview / Command Centre (route: #/home).
 *
 * Renders today's KPIs, the live feed and the low-stock alerts, then keeps them
 * current from the WebSocket stream. The view owns no server state: it refetches
 * a snapshot on mount and after any gap in the stream, and otherwise applies
 * events to what it already holds.
 *
 * The KPIs are derived from the transaction page the snapshot returns, not from
 * a server-side aggregate — none exists yet. They are therefore correct only
 * over that page, which is why the fetch asks for the maximum the server will
 * return and why the limitation is noted in TODO.md.
 */

import {
  MAX_TRANSACTION_LIMIT,
  fetchDevices,
  fetchInventory,
  fetchOnboardingStatus,
  fetchSummary,
  fetchTransactions,
  runTestCapture,
  type DailySummary,
  type Device,
  type OnboardingStatus,
  type Product,
  type Transaction,
} from "../api.js";
import {
  clear,
  el,
  formatMoney,
  formatQuantity,
  formatStatus,
  formatTime,
  hook,
  isLowStock,
  isToday,
  setText,
} from "../format.js";
import {
  CONNECTION_STATUS_EVENT,
  getSocket,
  type ConnectionStatus,
  type ConnectionStatusDetail,
} from "../ws.js";
import type { ViewCleanup } from "./index.js";

/** How many movements the overview feed shows before it stops growing. */
const FEED_LIMIT = 20;

const CONNECTION_LABELS: Readonly<Record<ConnectionStatus, string>> = {
  connecting: "Connecting…",
  open: "Live",
  reconnecting: "Reconnecting…",
  closed: "Offline",
};

interface HomeState {
  transactions: Transaction[];
  products: Product[];
  summary?: DailySummary | undefined;
  onboarding?: OnboardingStatus | undefined;
  devices?: readonly Device[] | undefined;
}

export function mountHome(root: HTMLElement, shopId: string): ViewCleanup {
  const state: HomeState = { transactions: [], products: [] };
  let disposed = false;
  const abortController = new AbortController();
  const signal = abortController.signal;

  const feedList = hook(root, "live-feed");
  const feedEmpty = hook(root, "live-feed-empty");
  const lowStockList = hook(root, "low-stock-list");
  const connectionLabel = hook(root, "connection-status");
  const onboardingContainer = hook(root, "onboarding-checklist-container");
  const deviceBadge = hook(root, "device-health-badge");

  function renderOnboarding(): void {
    if (onboardingContainer === null || state.onboarding === undefined) return;
    clear(onboardingContainer);

    const ob = state.onboarding;
    const card = el("section", `onboarding-card ${ob.is_complete ? "onboarding-card--complete" : ""}`);

    // Header
    const header = el("div", "onboarding-card__header");
    const title = el(
      "h2",
      "onboarding-card__title",
      ob.is_complete ? "✓ Shop Setup Complete (Ambient Capture Ready)" : "Getting Started: Shop Setup Checklist",
    );
    const progressBadge = el(
      "span",
      `badge ${ob.is_complete ? "badge--confirmed" : "badge--review"}`,
      `${ob.completed_steps} of ${ob.total_steps} Milestones`,
    );
    header.append(title, progressBadge);

    // Progress Bar
    const progressBar = el("div", "onboarding-progress-bar");
    const fill = el("div", "onboarding-progress-bar__fill");
    fill.style.width = `${(ob.completed_steps / ob.total_steps) * 100}%`;
    progressBar.append(fill);

    // 4 Step Cards
    const stepsGrid = el("div", "onboarding-steps");

    // 1. Profile
    const step1 = el("div", `onboarding-step ${ob.steps.profile.completed ? "onboarding-step--complete" : ""}`);
    const h1 = el("div", "onboarding-step__header");
    h1.append(
      el("span", "onboarding-step__icon", ob.steps.profile.completed ? "✓" : "①"),
      el("h3", "onboarding-step__title", ob.steps.profile.title),
    );
    step1.append(h1, el("p", "onboarding-step__desc", ob.steps.profile.description));
    if (!ob.steps.profile.completed) {
      const act = el("div", "onboarding-step__action");
      act.innerHTML = `<a href="#/settings" class="button button--sm button--secondary">Configure</a>`;
      step1.append(act);
    }
    stepsGrid.append(step1);

    // 2. Catalogue
    const step2 = el("div", `onboarding-step ${ob.steps.catalogue.completed ? "onboarding-step--complete" : ""}`);
    const h2 = el("div", "onboarding-step__header");
    h2.append(
      el("span", "onboarding-step__icon", ob.steps.catalogue.completed ? "✓" : "②"),
      el("h3", "onboarding-step__title", ob.steps.catalogue.title),
    );
    step2.append(
      h2,
      el(
        "p",
        "onboarding-step__desc",
        ob.steps.catalogue.completed
          ? `${ob.steps.catalogue.count} product(s) active in catalogue`
          : ob.steps.catalogue.description,
      ),
    );
    if (!ob.steps.catalogue.completed) {
      const act = el("div", "onboarding-step__action");
      act.innerHTML = `<a href="#/inventory" class="button button--sm button--secondary">Add Products</a>`;
      step2.append(act);
    }
    stepsGrid.append(step2);

    // 3. Devices
    const step3 = el("div", `onboarding-step ${ob.steps.devices.completed ? "onboarding-step--complete" : ""}`);
    const h3 = el("div", "onboarding-step__header");
    h3.append(
      el("span", "onboarding-step__icon", ob.steps.devices.completed ? "✓" : "③"),
      el("h3", "onboarding-step__title", ob.steps.devices.title),
    );
    step3.append(
      h3,
      el(
        "p",
        "onboarding-step__desc",
        ob.steps.devices.completed
          ? `${ob.steps.devices.count} edge device(s) paired`
          : ob.steps.devices.description,
      ),
    );
    if (!ob.steps.devices.completed) {
      const act = el("div", "onboarding-step__action");
      act.innerHTML = `<a href="#/settings" class="button button--sm button--secondary">Pair Device</a>`;
      step3.append(act);
    }
    stepsGrid.append(step3);

    // 4. Test Capture
    const step4 = el("div", `onboarding-step ${ob.steps.test_capture.completed ? "onboarding-step--complete" : ""}`);
    const h4 = el("div", "onboarding-step__header");
    h4.append(
      el("span", "onboarding-step__icon", ob.steps.test_capture.completed ? "✓" : "④"),
      el("h3", "onboarding-step__title", ob.steps.test_capture.title),
    );
    step4.append(
      h4,
      el(
        "p",
        "onboarding-step__desc",
        ob.steps.test_capture.completed
          ? `${ob.steps.test_capture.count} capture(s) recorded in ledger`
          : ob.steps.test_capture.description,
      ),
    );
    const testAct = el("div", "onboarding-step__action");
    const testBtn = el(
      "button",
      "button button--sm button--primary",
      ob.steps.test_capture.completed ? "Test Sound Again" : "Run Audio Test",
    );
    testBtn.type = "button";
    testBtn.addEventListener("click", () => {
      showTestCaptureModal(root, () => {
        void refetch();
      });
    });
    testAct.append(testBtn);
    step4.append(testAct);
    stepsGrid.append(step4);

    card.append(header, progressBar, stepsGrid);
    onboardingContainer.append(card);
  }

  function renderKPIs(): void {
    if (state.summary !== undefined) {
      setText(hook(root, "kpi-sales-today"), formatMoney(state.summary.today_sales_amount));
      setText(hook(root, "kpi-transaction-count"), String(state.summary.today_sales_count));
      setText(hook(root, "kpi-review-count"), String(state.summary.pending_reviews));
      setText(hook(root, "kpi-low-stock-count"), String(state.summary.low_stock_count));
      return;
    }

    const todaySales = state.transactions.filter(
      (transaction) =>
        transaction.type === "sale" &&
        transaction.status === "confirmed" &&
        isToday(transaction.occurred_at),
    );

    // Summed as integers in minor units; the only division happens when the
    // total is formatted for display.
    const revenue = todaySales.reduce((total, transaction) => total + transaction.amount.amount, 0);
    const currency = todaySales[0]?.amount.currency ?? "NGN";

    const awaitingReview = state.transactions.filter(
      (transaction) => transaction.status === "review_required",
    ).length;

    setText(hook(root, "kpi-sales-today"), formatMoney({ amount: revenue, currency }));
    setText(hook(root, "kpi-transaction-count"), String(todaySales.length));
    setText(hook(root, "kpi-review-count"), String(awaitingReview));
    setText(
      hook(root, "kpi-low-stock-count"),
      String(state.products.filter(isLowStock).length),
    );
  }

  function feedRow(transaction: Transaction): HTMLLIElement {
    const row = el("li", `feed__item feed__item--${transaction.status}`);

    const main = el("div", "feed__main");
    main.append(
      el("span", "feed__product", transaction.product_name),
      el(
        "span",
        "feed__meta",
        `${formatQuantity(transaction.quantity, transaction.unit)} · ${formatMoney(transaction.amount)}`,
      ),
    );

    const side = el("div", "feed__side");
    side.append(
      el("span", `feed__type feed__type--${transaction.type}`, transaction.type === "restock" ? "In" : "Out"),
      el("span", `feed__status feed__status--${transaction.status}`, formatStatus(transaction.status)),
      el("time", "feed__time", formatTime(transaction.occurred_at)),
    );

    row.append(main, side);
    return row;
  }

  function renderFeed(): void {
    if (feedList === null) return;
    clear(feedList);

    const recent = state.transactions.slice(0, FEED_LIMIT);
    if (recent.length === 0) {
      feedList.append(el("li", "feed__empty", "Waiting for the first transaction."));
      return;
    }
    for (const transaction of recent) {
      feedList.append(feedRow(transaction));
    }
  }

  function renderLowStock(): void {
    if (lowStockList === null) return;
    clear(lowStockList);

    const low = state.products.filter(isLowStock);
    if (low.length === 0) {
      lowStockList.append(el("li", "alert-list__empty", "Nothing below its reorder level."));
      return;
    }

    for (const product of low) {
      const item = el("li", "alert-list__item");
      item.append(
        el("span", "alert-list__product", product.name),
        el(
          "span",
          "alert-list__detail",
          `${product.quantity} ${product.base_unit} left · reorder at ${product.low_stock_threshold ?? 0}`,
        ),
      );
      lowStockList.append(item);
    }
  }

  function renderDeviceHealth(): void {
    if (deviceBadge === null) return;
    if (state.devices === undefined) {
      setText(deviceBadge, "Hardware: Checking…");
      deviceBadge.className = "badge badge--device badge--device-checking";
      return;
    }
    const active = state.devices.filter((d) => !d.revoked_at);
    if (active.length > 0) {
      setText(deviceBadge, `Hardware: Connected (${active.length})`);
      deviceBadge.className = "badge badge--device badge--device-online";
    } else {
      setText(deviceBadge, "Hardware: Not Paired");
      deviceBadge.className = "badge badge--device badge--device-offline";
    }
  }

  function render(): void {
    renderKPIs();
    renderDeviceHealth();
    renderOnboarding();
    renderFeed();
    renderLowStock();
  }

  /** Fetches transactions, inventory and daily summary snapshots and replaces state wholesale. */
  async function refetch(): Promise<void> {
    try {
      const [transactions, inventory, summary, onboarding, devicesRes] = await Promise.all([
        fetchTransactions(shopId, MAX_TRANSACTION_LIMIT, undefined, signal),
        fetchInventory(shopId, signal),
        fetchSummary(shopId, signal).catch(() => undefined),
        fetchOnboardingStatus(signal).catch(() => undefined),
        fetchDevices(signal).catch(() => undefined),
      ]);
      if (disposed || signal.aborted) return;

      state.transactions = [...transactions.transactions];
      state.products = [...inventory.products];
      state.summary = summary;
      state.onboarding = onboarding;
      state.devices = devicesRes?.devices;
      const maxSeq = Math.max(
        transactions.last_seq ?? 0,
        inventory.last_seq ?? 0,
        summary?.last_seq ?? 0,
      );
      if (maxSeq > 0) {
        getSocket()?.syncSequence(maxSeq);
      }
      render();
    } catch (error) {
      if (disposed || signal.aborted) return;
      console.error("ListenLedger: could not load the overview", error);
      // The template's empty state is still in place, because a failed first
      // fetch means renderFeed never ran to replace it. Reusing it keeps the
      // failure visible where the feed will appear.
      setText(
        feedEmpty,
        "Could not reach the server. The live feed will fill in when the connection returns.",
      );
    }
  }

  /** Inserts a transaction, keeping the feed newest-first and deduplicated by ID. */
  function applyTransaction(transaction: Transaction, _isNew?: boolean): void {
    const index = state.transactions.findIndex((existing) => existing.id === transaction.id);
    if (index === -1) {
      state.transactions.unshift(transaction);
    } else {
      state.transactions[index] = transaction;
    }

    // The KPIs depend on the set, so they are recomputed rather than adjusted
    // incrementally — a running total is one missed event away from drifting.
    render();

    // Synchronize server-authoritative aggregates in the background
    void fetchSummary(shopId, signal)
      .then((summary) => {
        if (disposed || signal.aborted) return;
        state.summary = summary;
        renderKPIs();
      })
      .catch(() => {
        // Fall back to local calculation if summary fetch fails
      });
  }

  function applyInventory(product: Product): void {
    const index = state.products.findIndex((existing) => existing.id === product.id);
    if (index === -1) {
      state.products.push(product);
    } else {
      state.products[index] = product;
    }
    renderKPIs();
    renderLowStock();
  }

  const onTransactionReceived = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    applyTransaction(detail.data, true);
  };

  const onTransactionUpdated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    applyTransaction(detail.data, false);
  };

  const onInventoryUpdated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Product }>).detail;
    applyInventory(detail.data);
  };

  const onProductCreated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Product }>).detail;
    applyInventory(detail.data);
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    setText(connectionLabel, CONNECTION_LABELS[detail.status] ?? detail.status);
    if (connectionLabel !== null) {
      connectionLabel.dataset.status = detail.status;
    }

    // The view cannot know which events it missed while disconnected, so it
    // refetches rather than assuming it only needs to apply future ones.
    if (detail.resyncRequired) {
      void refetch();
    }
  };

  window.addEventListener("ll:transaction-received", onTransactionReceived);
  window.addEventListener("ll:transaction-review-required", onTransactionReceived);
  window.addEventListener("ll:transaction-updated", onTransactionUpdated);
  window.addEventListener("ll:inventory-updated", onInventoryUpdated);
  window.addEventListener("ll:product-created", onProductCreated);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);

  void refetch();

  return () => {
    disposed = true;
    abortController.abort();
    window.removeEventListener("ll:transaction-received", onTransactionReceived);
    window.removeEventListener("ll:transaction-review-required", onTransactionReceived);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  };
}

function showTestCaptureModal(root: HTMLElement, onDone: () => void): void {
  const existing = document.querySelector(".modal-backdrop");
  if (existing) existing.remove();

  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", "Counter Sound & Recognition Test"),
    el(
      "p",
      "modal__subtitle",
      "Simulate spoken counter phrases to test catalogue matching, boundary detection, and confidence scoring.",
    ),
  );

  const form = el("form", "modal__form");
  form.setAttribute("novalidate", "");

  const transcriptLabel = el("label", "field");
  transcriptLabel.append(el("span", "field__label", "Spoken Counter Phrase"));
  const transcriptInput = document.createElement("textarea");
  transcriptInput.rows = 2;
  transcriptInput.required = true;
  transcriptInput.placeholder = "e.g. Two Peak Milk, seven thousand naira. Thank you.";
  transcriptInput.value = "Two Peak Milk, seven thousand naira. Thank you.";
  transcriptLabel.append(transcriptInput);

  // Quick fill pills
  const pills = el("div", "quick-pills");
  const samples = [
    { label: "Sale: 2 Peak Milk", text: "Two Peak Milk, seven thousand naira. Thank you.", type: "sale" },
    { label: "Sale: 1 Milo", text: "One Milo 500g, four thousand naira. Thank you.", type: "sale" },
    { label: "Restock: 3 Cartons", text: "Three cartons of Milo. Stock received.", type: "restock" },
  ];
  for (const s of samples) {
    const btn = el("button", "pill-button", s.label);
    btn.type = "button";
    btn.addEventListener("click", () => {
      transcriptInput.value = s.text;
      typeSelect.value = s.type;
    });
    pills.append(btn);
  }
  transcriptLabel.append(pills);

  const typeLabel = el("label", "field");
  typeLabel.append(el("span", "field__label", "Expected Boundary Type"));
  const typeSelect = document.createElement("select");
  typeSelect.innerHTML = `<option value="sale">Sale ("Thank you")</option><option value="restock">Restock ("Stock received")</option>`;
  typeLabel.append(typeSelect);

  const resultContainer = el("div", "test-capture-result");
  resultContainer.style.display = "none";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "button button--secondary", "Close");
  cancelBtn.type = "button";
  cancelBtn.addEventListener("click", () => {
    backdrop.remove();
    onDone();
  });

  const submitBtn = el("button", "button button--primary", "Verify Recognition");
  submitBtn.type = "submit";

  actions.append(cancelBtn, submitBtn);

  form.append(transcriptLabel, typeLabel, resultContainer, actions);
  modal.append(header, form);
  backdrop.append(modal);
  root.append(backdrop);

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const text = transcriptInput.value.trim();
    if (!text) return;

    submitBtn.disabled = true;
    setText(submitBtn, "Verifying…");
    clear(resultContainer);
    resultContainer.style.display = "block";
    resultContainer.append(el("p", "hint", "Analyzing transcript through validation engine…"));

    try {
      const res = await runTestCapture({
        transcript: text,
        type: typeSelect.value as "sale" | "restock",
      });

      clear(resultContainer);
      const resHeader = el("div", "test-capture-result__header");
      resHeader.append(
        el("strong", "", res.recognized ? "✓ Recognition Succeeded" : "⚠ Review Required"),
        el(
          "span",
          `badge ${res.recognized ? "badge--confirmed" : "badge--review"}`,
          res.closing_phrase ? `Boundary: "${res.closing_phrase}"` : "No boundary phrase",
        ),
      );
      resultContainer.append(resHeader);

      const summaryText = el("p", "panel__hint", res.summary);
      resultContainer.append(summaryText);

      if (res.lines.length > 0) {
        const linesList = el("div", "review-diff");
        for (const line of res.lines) {
          const lineEl = el("div", "review-diff__row");
          const confPct = Math.round(line.confidence.overall * 100);
          lineEl.innerHTML = `
            <div>
              <strong>${line.product_name}</strong> · ${line.quantity} ${line.unit} · ${formatMoney(line.amount)}
              <div class="confidence-bar" style="margin-top: 0.35rem;">
                <span>Confidence: ${confPct}% (${line.matched_by})</span>
                <div class="confidence-bar__track"><div class="confidence-bar__fill" style="width: ${confPct}%;"></div></div>
              </div>
              ${line.review_reasons.length > 0 ? `<div style="color: #ba7d00; font-size: 0.8rem; margin-top: 0.2rem;">Hold reasons: ${line.review_reasons.join(", ")}</div>` : ""}
            </div>
          `;
          linesList.append(lineEl);
        }
        resultContainer.append(linesList);
      }

      setText(submitBtn, "Test Again");
      submitBtn.disabled = false;
      cancelBtn.textContent = "Done & Close";
    } catch (err: unknown) {
      clear(resultContainer);
      const msg = err instanceof Error ? err.message : String(err);
      resultContainer.append(el("p", "error", `Verification error: ${msg}`));
      submitBtn.disabled = false;
      setText(submitBtn, "Verify Recognition");
    }
  });
}

