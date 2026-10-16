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
  createExpense,
  fetchDevices,
  fetchExpenses,
  fetchInventory,
  fetchOnboardingStatus,
  fetchShopCreditSummary,
  fetchSummary,
  fetchTransactions,
  postCapture,
  runTestCapture,
  runTrainingDrill,
  type DailySummary,
  type Device,
  type DrillRequest,
  type Expense,
  type ExpenseCategory,
  type OnboardingStatus,
  type Product,
  type ShopCreditSummary,
  type Transaction,
} from "../api.js";
import {
  CounterSpeechListener,
  isSpeechRecognitionSupported,
  playAudioFeedback,
  type BoundaryCaptureCandidate,
  type CounterListenerState,
} from "../audio.js";
import {
  clear,
  computeKPIs,
  el,
  formatExpenseCategory,
  formatMoney,
  formatQuantity,
  formatStatus,
  formatTime,
  hook,
  isLowStock,
  setText,
} from "../format.js";
import {
  CONNECTION_STATUS_EVENT,
  getSocket,
  type ConnectionStatus,
  type ConnectionStatusDetail,
} from "../ws.js";
import { showCustomerBookModal } from "./customers.js";
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
  expenses?: Expense[] | undefined;
  summary?: DailySummary | undefined;
  onboarding?: OnboardingStatus | undefined;
  devices?: readonly Device[] | undefined;
  creditSummary?: ShopCreditSummary | undefined;
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

    // 4. Staff Training
    const step4 = el("div", `onboarding-step ${ob.steps.staff_training?.completed ? "onboarding-step--complete" : ""}`);
    const h4 = el("div", "onboarding-step__header");
    h4.append(
      el("span", "onboarding-step__icon", ob.steps.staff_training?.completed ? "✓" : "④"),
      el("h3", "onboarding-step__title", ob.steps.staff_training?.title || "Cashier Protocol Training"),
    );
    step4.append(
      h4,
      el(
        "p",
        "onboarding-step__desc",
        ob.steps.staff_training?.completed
          ? "Staff trained on closing phrases, SKU names, and corrections"
          : (ob.steps.staff_training?.description || "Train cashier staff on sale and restock closing boundary protocols"),
      ),
    );
    const trainAct = el("div", "onboarding-step__action");
    const trainBtn = el(
      "button",
      "button button--sm button--primary",
      ob.steps.staff_training?.completed ? "Practice Drills Again" : "Practice Drills",
    );
    trainBtn.type = "button";
    trainBtn.addEventListener("click", () => {
      showTrainingDrillModal(root, () => {
        void refetch();
      });
    });
    trainAct.append(trainBtn);
    step4.append(trainAct);
    stepsGrid.append(step4);

    // 5. Target Gates Agreement
    const step5 = el("div", `onboarding-step ${ob.steps.target_gates?.completed ? "onboarding-step--complete" : ""}`);
    const h5 = el("div", "onboarding-step__header");
    h5.append(
      el("span", "onboarding-step__icon", ob.steps.target_gates?.completed ? "✓" : "⑤"),
      el("h3", "onboarding-step__title", ob.steps.target_gates?.title || "Pilot Target Gates"),
    );
    step5.append(
      h5,
      el(
        "p",
        "onboarding-step__desc",
        ob.steps.target_gates?.completed
          ? "Owner agreed pilot release accuracy and latency gates"
          : (ob.steps.target_gates?.description || "Agree accuracy, latency, and correction targets with shop owner"),
      ),
    );
    if (!ob.steps.target_gates?.completed) {
      const act = el("div", "onboarding-step__action");
      act.innerHTML = `<a href="#/settings" class="button button--sm button--secondary">Review Targets</a>`;
      step5.append(act);
    }
    stepsGrid.append(step5);

    // 6. Test Capture
    const step6 = el("div", `onboarding-step ${ob.steps.test_capture.completed ? "onboarding-step--complete" : ""}`);
    const h6 = el("div", "onboarding-step__header");
    h6.append(
      el("span", "onboarding-step__icon", ob.steps.test_capture.completed ? "✓" : "⑥"),
      el("h3", "onboarding-step__title", ob.steps.test_capture.title),
    );
    step6.append(
      h6,
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
    step6.append(testAct);
    stepsGrid.append(step6);

    card.append(header, progressBar, stepsGrid);
    onboardingContainer.append(card);
  }

  function renderKPIs(): void {
    const kpis = computeKPIs(state.transactions, state.products, state.summary, state.expenses);
    setText(hook(root, "kpi-sales-today"), formatMoney(kpis.revenue));
    setText(hook(root, "kpi-transaction-count"), String(kpis.salesCount));
    setText(hook(root, "kpi-expenses-today"), formatMoney(kpis.expenses));
    setText(hook(root, "kpi-net-profit"), formatMoney(kpis.netProfit));
    const customerDebtEl = hook(root, "kpi-customer-debt");
    if (customerDebtEl !== null) {
      if (
        state.creditSummary &&
        state.creditSummary.total_outstanding_debt &&
        typeof state.creditSummary.total_outstanding_debt.amount === "number"
      ) {
        setText(customerDebtEl, formatMoney(state.creditSummary.total_outstanding_debt));
      } else {
        setText(customerDebtEl, "—");
      }
    }
    setText(hook(root, "kpi-review-count"), String(kpis.reviewCount));
    setText(hook(root, "kpi-low-stock-count"), String(kpis.lowStockCount));
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
      const [transactions, inventory, summary, onboarding, devicesRes, expensesRes, creditRes] = await Promise.all([
        fetchTransactions(shopId, MAX_TRANSACTION_LIMIT, undefined, signal),
        fetchInventory(shopId, signal),
        fetchSummary(shopId, signal).catch(() => undefined),
        fetchOnboardingStatus(signal).catch(() => undefined),
        fetchDevices(signal).catch(() => undefined),
        fetchExpenses(undefined, signal).catch(() => undefined),
        fetchShopCreditSummary(signal).catch(() => undefined),
      ]);
      if (disposed || signal.aborted) return;

      state.transactions = [...transactions.transactions];
      state.products = [...inventory.products];
      state.summary = summary;
      state.onboarding = onboarding;
      state.devices = devicesRes?.devices;
      state.expenses = expensesRes?.expenses ? [...expensesRes.expenses] : [];
      state.creditSummary = creditRes;
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

  const onExpenseEvent = (): void => {
    void refetch();
  };

  const onCustomerEvent = (): void => {
    void refetch();
  };

  const recordExpenseBtn = hook(root, "record-expense-btn");
  if (recordExpenseBtn !== null) {
    recordExpenseBtn.addEventListener("click", () => {
      showRecordExpenseModal(root, () => {
        void refetch();
      });
    });
  }

  const customerBookBtn = hook(root, "customer-book-btn");
  if (customerBookBtn !== null) {
    customerBookBtn.addEventListener("click", () => {
      showCustomerBookModal(root, () => {
        void refetch();
      });
    });
  }

  const customerDebtKpiLink = hook(root, "kpi-customer-debt-link");
  if (customerDebtKpiLink !== null) {
    customerDebtKpiLink.addEventListener("click", (e) => {
      e.preventDefault();
      showCustomerBookModal(root, () => {
        void refetch();
      });
    });
  }

  // Counter Audio Dock Hooks
  const counterDock = hook(root, "counter-dock");
  const micDot = hook(root, "counter-mic-dot");
  const micTitle = hook(root, "counter-mic-title");
  const micSubtitle = hook(root, "counter-mic-subtitle");
  const micToggleBtn = hook<HTMLButtonElement>(root, "counter-mic-toggle-btn");
  const micBtnIcon = hook(root, "counter-mic-btn-icon");
  const micBtnLabel = hook(root, "counter-mic-btn-label");
  const livePanel = hook(root, "counter-live-panel");
  const levelFill = hook(root, "counter-level-fill");
  const liveTranscript = hook(root, "counter-live-transcript");
  const captureToast = hook(root, "counter-capture-toast");

  let toastTimeout: ReturnType<typeof setTimeout> | null = null;
  function showCaptureToast(text: string, type: "success" | "warning" | "error"): void {
    if (!captureToast) return;
    if (toastTimeout) clearTimeout(toastTimeout);
    captureToast.className = `counter-dock__toast counter-dock__toast--${type}`;
    captureToast.textContent = text;
    captureToast.style.display = "flex";
    toastTimeout = setTimeout(() => {
      if (captureToast) captureToast.style.display = "none";
    }, 6000);
  }

  let counterListener: CounterSpeechListener | null = null;
  const isAudioSupported = isSpeechRecognitionSupported();

  if (!isAudioSupported) {
    if (micSubtitle) {
      micSubtitle.textContent =
        "Speech recognition is not supported in this browser. Use Chrome on Android or Safari on iOS for live counter listening.";
    }
    if (micToggleBtn) {
      micToggleBtn.disabled = true;
      micToggleBtn.title = "Use Google Chrome on Android or Safari on iOS for live counter microphone";
    }
  } else {
    counterListener = new CounterSpeechListener({
      salePhrase: "thank you",
      restockPhrase: "stock received",
      onStateChange: (listenerState: CounterListenerState) => {
        if (!counterDock || !micDot || !micToggleBtn || !micBtnIcon || !micBtnLabel || !livePanel) return;

        counterDock.className = `counter-dock counter-dock--${listenerState}`;
        micDot.className = `counter-dock__status-dot counter-dock__status-dot--${listenerState}`;

        if (listenerState === "listening") {
          setText(micTitle, "Counter Microphone: Active");
          setText(micSubtitle, "Listening for sales at counter. Conclude with 'Thank you'");
          setText(micBtnIcon, "⏹");
          setText(micBtnLabel, "Stop Listening");
          micToggleBtn.className = "btn counter-dock__toggle-btn counter-dock__toggle-btn--active";
          livePanel.style.display = "flex";
        } else if (listenerState === "processing") {
          setText(micTitle, "Processing Spoken Sale…");
          setText(micSubtitle, "Validating items against catalogue and recording movement");
          setText(micBtnIcon, "⏳");
          setText(micBtnLabel, "Processing…");
        } else if (listenerState === "error") {
          setText(micTitle, "Counter Microphone: Error");
          setText(micBtnIcon, "🎙");
          setText(micBtnLabel, "Retry Listening");
          micToggleBtn.className = "btn btn--primary counter-dock__toggle-btn";
        } else {
          setText(micTitle, "Counter Microphone");
          setText(micSubtitle, "Idle — Tap to start ambient sales listening on this phone");
          setText(micBtnIcon, "🎙");
          setText(micBtnLabel, "Start Listening");
          micToggleBtn.className = "btn btn--primary counter-dock__toggle-btn";
          livePanel.style.display = "none";
          if (liveTranscript) {
            liveTranscript.innerHTML = `<em>Say a sale ending with "Thank you"…</em>`;
          }
        }
      },
      onInterimTranscript: (text: string) => {
        if (liveTranscript) {
          liveTranscript.textContent = `Heard: "${text}"`;
        }
      },
      onAudioLevel: (level: number) => {
        if (levelFill) {
          levelFill.style.width = `${Math.min(100, Math.round(level * 100))}%`;
        }
      },
      onBoundaryDetected: async (candidate: BoundaryCaptureCandidate) => {
        try {
          if (liveTranscript) {
            liveTranscript.textContent = `Processing: "${candidate.transcript}"…`;
          }
          const res = await postCapture({
            transcript: candidate.transcript,
            type: candidate.type,
            closing_phrase: candidate.closingPhrase,
          });

          if (res.requires_review) {
            playAudioFeedback("review");
            const first = res.transactions[0];
            const name = first ? first.product_name : "Unrecognized item";
            showCaptureToast(`⚠ Logged for Review: ${name} ("${candidate.transcript}")`, "warning");
          } else {
            playAudioFeedback("sale");
            const first = res.transactions[0];
            const detail = first
              ? `${first.quantity} ${first.product_name} (${formatMoney(first.amount)})`
              : candidate.transcript;
            showCaptureToast(`✓ Logged Sale: ${detail}`, "success");
          }
          void refetch();
        } catch (err) {
          playAudioFeedback("error");
          const msg = err instanceof Error ? err.message : "Failed to record capture";
          showCaptureToast(`❌ Capture Error: ${msg}`, "error");
        }
      },
      onError: (err: Error) => {
        if (micSubtitle) {
          micSubtitle.textContent = err.message;
        }
        showCaptureToast(`Mic notice: ${err.message}`, "error");
      },
    });

    micToggleBtn?.addEventListener("click", () => {
      if (!counterListener) return;
      if (counterListener.isListening()) {
        counterListener.stop();
      } else {
        counterListener.start().catch(() => {
          // Handled via onError
        });
      }
    });
  }

  window.addEventListener("ll:transaction-received", onTransactionReceived);
  window.addEventListener("ll:transaction-review-required", onTransactionReceived);
  window.addEventListener("ll:transaction-updated", onTransactionUpdated);
  window.addEventListener("ll:inventory-updated", onInventoryUpdated);
  window.addEventListener("ll:product-created", onProductCreated);
  window.addEventListener("ll:expense-created", onExpenseEvent);
  window.addEventListener("ll:expense-deleted", onExpenseEvent);
  window.addEventListener("ll:customer-created", onCustomerEvent);
  window.addEventListener("ll:customer-debt-recorded", onCustomerEvent);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);

  void refetch();

  return () => {
    disposed = true;
    abortController.abort();
    if (counterListener) {
      counterListener.stop();
    }
    if (toastTimeout) {
      clearTimeout(toastTimeout);
    }
    window.removeEventListener("ll:transaction-received", onTransactionReceived);
    window.removeEventListener("ll:transaction-review-required", onTransactionReceived);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    window.removeEventListener("ll:expense-created", onExpenseEvent);
    window.removeEventListener("ll:expense-deleted", onExpenseEvent);
    window.removeEventListener("ll:customer-created", onCustomerEvent);
    window.removeEventListener("ll:customer-debt-recorded", onCustomerEvent);
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
      "Test speech recognition and validation against your shop catalogue using either your phone microphone or simulated text.",
    ),
  );

  let activeTestListener: CounterSpeechListener | null = null;
  const isSpeechSupported = isSpeechRecognitionSupported();

  // Tab switchers
  const tabGroup = el("div", "test-tab-group");
  const micTabBtn = el("button", `test-tab-btn ${isSpeechSupported ? "test-tab-btn--active" : ""}`, "🎙 Phone Microphone Test");
  micTabBtn.type = "button";
  const textTabBtn = el("button", `test-tab-btn ${!isSpeechSupported ? "test-tab-btn--active" : ""}`, "⌨ Text Simulator");
  textTabBtn.type = "button";
  tabGroup.append(micTabBtn, textTabBtn);

  // Panels
  const micPanel = el("div", "live-mic-panel");
  const form = el("form", "modal__form");
  form.setAttribute("novalidate", "");

  if (!isSpeechSupported) {
    micPanel.style.display = "none";
    form.style.display = "block";
  } else {
    micPanel.style.display = "flex";
    form.style.display = "none";
  }

  // --- Mic Panel Setup ---
  const micPrompt = el(
    "p",
    "hint",
    'Tap the microphone button, speak a sales phrase naturally into your phone, and finish with your closing boundary phrase (e.g. "Two Peak Milk, seven thousand naira. Thank you").',
  );
  const liveMicBtn = el("button", "button button--primary live-mic-btn", "🎙 Tap to Speak");
  liveMicBtn.type = "button";

  const liveTranscriptBox = el("div", "counter-dock__transcript");
  liveTranscriptBox.style.minHeight = "2.5rem";
  liveTranscriptBox.innerHTML = `<em>Waiting for speech…</em>`;

  const micStatus = el("p", "field__hint", "Microphone is idle.");

  micPanel.append(micPrompt, liveMicBtn, liveTranscriptBox, micStatus);

  // --- Text Form Setup ---
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

  const textSubmitBtn = el("button", "button button--primary", "Verify Recognition");
  textSubmitBtn.type = "submit";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "button button--secondary", "Close");
  cancelBtn.type = "button";

  const cleanupModal = (): void => {
    if (activeTestListener) {
      activeTestListener.stop();
      activeTestListener = null;
    }
    backdrop.remove();
    onDone();
  };
  cancelBtn.addEventListener("click", cleanupModal);
  actions.append(cancelBtn, textSubmitBtn);

  const resultContainer = el("div", "test-capture-result");
  resultContainer.style.display = "none";

  form.append(transcriptLabel, typeLabel, resultContainer, actions);

  // Tab switching logic
  micTabBtn.addEventListener("click", () => {
    micTabBtn.className = "test-tab-btn test-tab-btn--active";
    textTabBtn.className = "test-tab-btn";
    micPanel.style.display = "flex";
    form.style.display = "none";
  });

  textTabBtn.addEventListener("click", () => {
    if (activeTestListener) {
      activeTestListener.stop();
      activeTestListener = null;
      setText(liveMicBtn, "🎙 Tap to Speak");
    }
    textTabBtn.className = "test-tab-btn test-tab-btn--active";
    micTabBtn.className = "test-tab-btn";
    micPanel.style.display = "none";
    form.style.display = "block";
  });

  async function evaluatePhrase(phrase: string, expectedType: "sale" | "restock"): Promise<void> {
    clear(resultContainer);
    resultContainer.style.display = "block";
    resultContainer.append(el("p", "hint", "Analyzing transcript through validation engine…"));

    try {
      const res = await runTestCapture({
        transcript: phrase,
        type: expectedType,
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

      playAudioFeedback(res.recognized ? "sale" : "review");
      cancelBtn.textContent = "Done & Close";
      setText(textSubmitBtn, "Test Again");
      textSubmitBtn.disabled = false;
    } catch (err: unknown) {
      clear(resultContainer);
      const msg = err instanceof Error ? err.message : String(err);
      resultContainer.append(el("p", "error", `Verification error: ${msg}`));
      playAudioFeedback("error");
      textSubmitBtn.disabled = false;
      setText(textSubmitBtn, "Verify Recognition");
    }
  }

  // Mic Button handler
  liveMicBtn.addEventListener("click", () => {
    if (activeTestListener && activeTestListener.isListening()) {
      activeTestListener.stop();
      activeTestListener = null;
      setText(liveMicBtn, "🎙 Tap to Speak");
      setText(micStatus, "Microphone stopped.");
      return;
    }

    setText(liveMicBtn, "⏹ Listening… Tap to Finish");
    setText(micStatus, "Listening… Speak your counter phrase now.");
    liveTranscriptBox.innerHTML = `<em>Listening to microphone…</em>`;

    activeTestListener = new CounterSpeechListener({
      salePhrase: "thank you",
      restockPhrase: "stock received",
      onInterimTranscript: (text: string) => {
        liveTranscriptBox.textContent = `Heard: "${text}"`;
      },
      onBoundaryDetected: (candidate: BoundaryCaptureCandidate) => {
        if (activeTestListener) {
          activeTestListener.stop();
          activeTestListener = null;
        }
        setText(liveMicBtn, "🎙 Tap to Speak");
        setText(micStatus, `Boundary detected ("${candidate.closingPhrase}")! Evaluating…`);
        liveTranscriptBox.textContent = candidate.transcript;
        void evaluatePhrase(candidate.transcript, candidate.type);
      },
      onError: (err: Error) => {
        setText(liveMicBtn, "🎙 Tap to Speak");
        setText(micStatus, `Microphone notice: ${err.message}`);
        activeTestListener = null;
      },
    });

    activeTestListener.start().catch((err: Error) => {
      setText(liveMicBtn, "🎙 Tap to Speak");
      setText(micStatus, err.message);
      activeTestListener = null;
    });
  });

  // Text form submission handler
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const text = transcriptInput.value.trim();
    if (!text) return;
    textSubmitBtn.disabled = true;
    setText(textSubmitBtn, "Verifying…");
    void evaluatePhrase(text, typeSelect.value as "sale" | "restock");
  });

  modal.append(header, tabGroup, micPanel, form);
  backdrop.append(modal);
  root.append(backdrop);
}

function showTrainingDrillModal(root: HTMLElement, onDone: () => void): void {
  const existing = document.querySelector(".modal-backdrop");
  if (existing) existing.remove();

  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", "Cashier Protocol Training & Boundary Drills"),
    el(
      "p",
      "modal__subtitle",
      "Practice closing boundary protocols (e.g. 'Thank you', 'Stock received'), self-correction phrasing, and catalogue articulation.",
    ),
  );

  const form = el("form", "modal__form");
  form.setAttribute("novalidate", "");

  const scenarioLabel = el("label", "field");
  scenarioLabel.append(el("span", "field__label", "Training Scenario"));
  const scenarioSelect = document.createElement("select");
  scenarioSelect.innerHTML = `
    <option value="clean_sale">Clean Sale Boundary ("Thank you")</option>
    <option value="restock">Stock Restock Inflow ("Stock received")</option>
    <option value="spoken_correction">Spoken Self-Correction ("Wait no, make that...")</option>
    <option value="non_commercial_chatter">Non-Commercial Chatter Negative Control</option>
    <option value="custom">Custom Cashier Phrase</option>
  `;
  scenarioLabel.append(scenarioSelect);

  const transcriptLabel = el("label", "field");
  transcriptLabel.append(el("span", "field__label", "Spoken Counter Phrase"));
  const transcriptInput = document.createElement("textarea");
  transcriptInput.rows = 2;
  transcriptInput.required = true;
  transcriptInput.value = "Two Peak Milk, seven thousand naira. Thank you.";
  transcriptLabel.append(transcriptInput);

  // Quick preset pills
  const presets: Record<string, string> = {
    clean_sale: "Two Peak Milk, seven thousand naira. Thank you.",
    restock: "Three cartons of Milo. Stock received.",
    spoken_correction: "Two Peak Milk. Wait no, make that three Peak Milk, ten thousand five hundred naira. Thank you.",
    non_commercial_chatter: "Good afternoon ma, how are the children doing at school today?",
    custom: "",
  };

  scenarioSelect.addEventListener("change", () => {
    const val = scenarioSelect.value;
    if (presets[val] !== undefined && val !== "custom") {
      transcriptInput.value = presets[val];
    }
  });

  const pills = el("div", "quick-pills");
  const drillPills = [
    { label: "Sale Drill", scenario: "clean_sale" as const, text: "Two Peak Milk, seven thousand naira. Thank you." },
    { label: "Restock Drill", scenario: "restock" as const, text: "Three cartons of Milo. Stock received." },
    { label: "Correction Drill", scenario: "spoken_correction" as const, text: "Two Peak Milk. Wait no, make that three Peak Milk, ten thousand five hundred naira. Thank you." },
    { label: "Chatter (Negative)", scenario: "non_commercial_chatter" as const, text: "Good afternoon ma, how are the children doing at school today?" },
  ];
  for (const dp of drillPills) {
    const btn = el("button", "pill-button", dp.label);
    btn.type = "button";
    btn.addEventListener("click", () => {
      scenarioSelect.value = dp.scenario;
      transcriptInput.value = dp.text;
    });
    pills.append(btn);
  }
  transcriptLabel.append(pills);

  const resultContainer = el("div", "test-capture-result");
  resultContainer.style.display = "none";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "button button--secondary", "Close");
  cancelBtn.type = "button";
  cancelBtn.addEventListener("click", () => {
    backdrop.remove();
    onDone();
  });

  const submitBtn = el("button", "button button--primary", "Evaluate Drill");
  submitBtn.type = "submit";

  actions.append(cancelBtn, submitBtn);

  form.append(scenarioLabel, transcriptLabel, resultContainer, actions);
  modal.append(header, form);
  backdrop.append(modal);
  root.append(backdrop);

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const text = transcriptInput.value.trim();
    if (!text) return;

    submitBtn.disabled = true;
    setText(submitBtn, "Evaluating…");
    clear(resultContainer);
    resultContainer.style.display = "block";
    resultContainer.append(el("p", "hint", "Running training drill evaluation…"));

    try {
      const res = await runTrainingDrill({
        scenario_type: scenarioSelect.value as DrillRequest["scenario_type"],
        transcript: text,
      });

      clear(resultContainer);
      const resHeader = el("div", "test-capture-result__header");
      const passText = res.passed ? `✓ Drill Passed (${res.score}%)` : `⚠ Drill Needs Practice (${res.score}%)`;
      resHeader.append(
        el("strong", "", passText),
        el(
          "span",
          `badge ${res.passed ? "badge--confirmed" : "badge--review"}`,
          res.boundary_detected ? `Boundary: "${res.closing_phrase}"` : "No boundary detected",
        ),
      );
      resultContainer.append(resHeader);

      if (res.staff_trained_at) {
        resultContainer.append(
          el("p", "panel__hint", "Staff training milestone completed and verified for this shop."),
        );
      }

      // Checklist items
      if (res.checklist && res.checklist.length > 0) {
        const checkListEl = el("div", "review-diff");
        checkListEl.style.marginTop = "0.75rem";
        for (const item of res.checklist) {
          const itemRow = el("div", "review-diff__row");
          itemRow.innerHTML = `
            <div>
              <strong>${item.passed ? "✓" : "✗"} ${item.name}</strong>
              <div style="font-size: 0.85rem; color: ${item.passed ? "#28a745" : "#ba7d00"}; margin-top: 0.2rem;">
                ${item.detail}
              </div>
            </div>
          `;
          checkListEl.append(itemRow);
        }
        resultContainer.append(checkListEl);
      }

      // Coaching tips
      if (res.coaching_tips && res.coaching_tips.length > 0) {
        const tipsContainer = el("div", "drill-coaching-tips");
        tipsContainer.style.marginTop = "0.75rem";
        tipsContainer.style.padding = "0.75rem";
        tipsContainer.style.background = "#fff8e6";
        tipsContainer.style.border = "1px solid #ffd591";
        tipsContainer.style.borderRadius = "4px";
        const tipsTitle = el("strong", "", "Coaching Tips:");
        tipsContainer.append(tipsTitle);
        const ul = document.createElement("ul");
        ul.style.marginTop = "0.35rem";
        ul.style.marginBottom = "0";
        ul.style.paddingLeft = "1.2rem";
        for (const tip of res.coaching_tips) {
          const li = document.createElement("li");
          li.style.fontSize = "0.85rem";
          li.style.marginBottom = "0.2rem";
          li.textContent = tip;
          ul.append(li);
        }
        tipsContainer.append(ul);
        resultContainer.append(tipsContainer);
      }

      setText(submitBtn, "Practice Again");
      submitBtn.disabled = false;
      cancelBtn.textContent = "Done & Close";
    } catch (err: unknown) {
      clear(resultContainer);
      const msg = err instanceof Error ? err.message : String(err);
      resultContainer.append(el("p", "error", `Evaluation error: ${msg}`));
      submitBtn.disabled = false;
      setText(submitBtn, "Evaluate Drill");
    }
  });
}

function showRecordExpenseModal(_root: HTMLElement, onDone: () => void): void {
  const existing = document.querySelector(".modal-backdrop");
  if (existing) existing.remove();

  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--record-expense");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", "Record Operational Expense"),
    el("p", "modal__subtitle", "Track daily generator fuel, shop rent, transport, wages, or supplies."),
  );

  const form = el("form", "modal__form");

  // Category select
  const catGroup = el("div", "form-group");
  catGroup.append(el("label", "form-label", "Expense Category"));
  const catSelect = document.createElement("select");
  catSelect.className = "form-select";
  catSelect.setAttribute("data-hook", "expense-category-select");

  const categories: readonly ExpenseCategory[] = [
    "generator_fuel",
    "store_rent",
    "transport_logistics",
    "packaging_supplies",
    "staff_wages",
    "utilities",
    "repairs",
    "other",
  ];
  for (const c of categories) {
    const opt = document.createElement("option");
    opt.value = c;
    opt.textContent = formatExpenseCategory(c);
    catSelect.append(opt);
  }
  catGroup.append(catSelect);

  // Amount input
  const amountGroup = el("div", "form-group");
  amountGroup.append(el("label", "form-label", "Amount (Naira)"));
  const amountInput = document.createElement("input");
  amountInput.type = "number";
  amountInput.step = "any";
  amountInput.min = "0.01";
  amountInput.placeholder = "e.g. 5000";
  amountInput.className = "form-input";
  amountInput.setAttribute("data-hook", "expense-amount-input");
  amountInput.required = true;
  amountGroup.append(amountInput);

  // Description input
  const descGroup = el("div", "form-group");
  descGroup.append(el("label", "form-label", "Description"));
  const descInput = document.createElement("input");
  descInput.type = "text";
  descInput.placeholder = "e.g. 10 litres generator petrol for afternoon trading";
  descInput.maxLength = 500;
  descInput.className = "form-input";
  descInput.setAttribute("data-hook", "expense-desc-input");
  descInput.required = true;
  descGroup.append(descInput);

  // Receipt Reference input
  const receiptGroup = el("div", "form-group");
  receiptGroup.append(el("label", "form-label", "Receipt / Slip Ref (Optional)"));
  const receiptInput = document.createElement("input");
  receiptInput.type = "text";
  receiptInput.placeholder = "e.g. REC-9821";
  receiptInput.className = "form-input";
  receiptInput.setAttribute("data-hook", "expense-receipt-input");
  receiptGroup.append(receiptInput);

  // Error message
  const errorMsg = el("div", "modal__error form-error", "");
  errorMsg.setAttribute("data-hook", "expense-error");
  errorMsg.style.display = "none";

  // Actions
  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "btn btn--secondary", "Cancel");
  cancelBtn.type = "button";
  cancelBtn.setAttribute("data-hook", "expense-cancel-btn");
  cancelBtn.addEventListener("click", () => {
    backdrop.remove();
  });

  const submitBtn = el("button", "btn btn--primary", "Record Expense");
  submitBtn.type = "submit";
  submitBtn.setAttribute("data-hook", "expense-submit-btn");

  actions.append(cancelBtn, submitBtn);

  form.append(catGroup, amountGroup, descGroup, receiptGroup, errorMsg, actions);
  modal.append(header, form);
  backdrop.append(modal);
  document.body.append(backdrop);

  if (typeof amountInput.focus === "function") {
    amountInput.focus();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    errorMsg.style.display = "none";
    errorMsg.textContent = "";

    const val = parseFloat(amountInput.value);
    if (isNaN(val) || val <= 0) {
      errorMsg.textContent = "Please enter a valid amount greater than zero.";
      errorMsg.style.display = "block";
      return;
    }
    const minorAmount = Math.round(val * 100);
    const desc = descInput.value.trim();
    if (!desc) {
      errorMsg.textContent = "Please enter a description for the expense.";
      errorMsg.style.display = "block";
      return;
    }

    submitBtn.disabled = true;
    submitBtn.textContent = "Recording…";

    try {
      await createExpense({
        category: catSelect.value as ExpenseCategory,
        amount: { amount: minorAmount },
        description: desc,
        receipt_ref: receiptInput.value.trim() || undefined,
      });
      backdrop.remove();
      onDone();
    } catch (err) {
      submitBtn.disabled = false;
      submitBtn.textContent = "Record Expense";
      errorMsg.textContent = err instanceof Error ? err.message : "Failed to record expense";
      errorMsg.style.display = "block";
    }
  });
}


