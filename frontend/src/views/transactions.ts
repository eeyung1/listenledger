/**
 * Transactions & Review System (route: #/transactions).
 *
 * One view, two jobs. The ledger lists every movement; the review queue above
 * it holds the captures the validation engine was not confident about. The
 * distinction matters fiscally: a Review Required row has had no effect on
 * inventory or revenue, so it is shown as awaiting a decision rather than as a
 * recorded movement.
 *
 * The filters are applied client-side. The pilot's ledger is a few hundred rows
 * and the snapshot is fetched whole, so filtering here avoids a round trip per
 * keystroke; a shop with a real history needs the filters pushed into the
 * query, which is noted in TODO.md.
 */

import {
  MAX_TRANSACTION_LIMIT,
  confirmTransaction,
  createManualTransaction,
  fetchInventory,
  fetchTransactionDecision,
  fetchTransactions,
  generateManualRequestId,
  getExportTransactionsUrl,
  rejectTransaction,
  reverseTransaction,
  ALL_UNITS,
  type ManualTransactionInput,
  type Product,
  type ReviewCorrections,
  type ReviewDecision,
  type Transaction,
  type TransactionType,
  type Unit,
} from "../api.js";
import { enqueueManualTransaction, isNetworkError, setActiveQueueShop, syncOfflineQueue } from "../offline_queue.js";
import { getCachedCatalogue, setCachedCatalogue } from "../offline_catalogue.js";
import {
  applyFilteredTransactionToList,
  applyTransactionToList,
  cell,
  clear,
  el,
  formatConfidenceBreakdown,
  formatDate,
  formatDirection,
  formatMoney,
  formatQuantity,
  formatStatus,
  formatTime,
  hook,
  setText,
  sortTransactions,
} from "../format.js";
import { CONNECTION_STATUS_EVENT, getSocket, type ConnectionStatusDetail } from "../ws.js";
import { uiStateItem, uiStateRow } from "../ui_state.js";
import type { ViewCleanup } from "./index.js";

interface Filters {
  type: string;
  status: string;
  receipt_id: string;
  customer_id?: string;
}

/**
 * `has_audio` is computed by the backend when a legacy snippet exists. The
 * snippet itself is deliberately absent from ledger snapshots and WebSocket
 * events, so it is fetched from the shop-scoped detail endpoint only when the
 * owner asks to hear it.
 */
type AudioBearingTransaction = Transaction & { has_audio?: boolean };

function hasLegacyAudio(transaction: Transaction): boolean {
  return (transaction as AudioBearingTransaction).has_audio === true;
}

async function playLegacyAudio(shopId: string, transaction: Transaction): Promise<void> {
  try {
    const response = await fetch(
      `/api/transactions/${encodeURIComponent(transaction.id)}?shop_id=${encodeURIComponent(shopId)}`,
      { headers: { Accept: "application/json" } },
    );
    if (!response.ok) return;
    const detail = (await response.json()) as { audio_data?: string };
    if (!detail.audio_data) return;
    const player = document.createElement("audio");
    player.src = detail.audio_data;
    if (typeof player.play === "function") {
      try {
        await player.play();
      } catch {
        // Autoplay policy may require a further gesture; the fetch still warmed it.
      }
    }
  } catch {
    // Legacy playback is optional; a failed fetch must not break the ledger.
  }
}

function legacyAudioControl(shopId: string, transaction: Transaction): HTMLElement | null {
  if (!hasLegacyAudio(transaction)) return null;
  const wrapper = el("div", "review-card__audio");
  const label = el("span", "review-card__audio-label", "🎙 Spoken Counter Audio:");
  const playBtn = el("button", "button button--secondary button--sm button--audio-replay", "▶ Play audio");
  playBtn.type = "button";
  playBtn.title = "Load the legacy audio snippet recorded for this transaction";
  playBtn.addEventListener("click", () => {
    void playLegacyAudio(shopId, transaction);
  });
  wrapper.append(label, playBtn);
  return wrapper;
}

export function mountTransactions(root: HTMLElement, shopId: string): ViewCleanup {
  setActiveQueueShop(shopId);
  let transactions: Transaction[] = [];
  let heldTransactions: Transaction[] = [];
  let disposed = false;
  const abortController = new AbortController();
  const signal = abortController.signal;
  let pageOffset = 0;
  const pageSize = 50;
  let totalCount = 0;
  let pendingNewCount = 0;

  const filters: Filters = { type: "", status: "", receipt_id: "", customer_id: "" };

  const ledgerBody = hook(root, "ledger-body");
  const reviewQueue = hook(root, "review-queue");
  const reviewCount = hook(root, "review-queue-count");
  const typeFilter = hook<HTMLSelectElement>(root, "filter-type");
  const statusFilter = hook<HTMLSelectElement>(root, "filter-status");
  const exportLink = hook<HTMLAnchorElement>(root, "export-csv");
  const pagePrev = hook<HTMLButtonElement>(root, "page-prev");
  const pageNext = hook<HTMLButtonElement>(root, "page-next");
  const pageInfo = hook(root, "page-info");
  const manualBtn = hook<HTMLButtonElement>(root, "manual-entry");

  function reviewCard(transaction: Transaction): HTMLLIElement {
    const card = el("li", "review-card");

    const head = el("div", "review-card__head");
    head.append(
      el("span", "review-card__product", transaction.product_name),
      el(
        "span",
        "review-card__amount",
        `${formatQuantity(transaction.quantity, transaction.unit)} · ${formatMoney(transaction.amount)}`,
      ),
    );
    if (transaction.supplier_name) {
      const supp = el("span", "badge badge--accent", `Supplier: ${transaction.supplier_name}`);
      head.append(supp);
    }
    if (transaction.customer_name) {
      const cust = el("span", "badge badge--warning", `Tab: ${transaction.customer_name}`);
      head.append(cust);
    }
    if (transaction.receipt_id) {
      const rcpt = el("span", "badge badge--mono", `Receipt: ${transaction.receipt_id}`);
      head.append(rcpt);
    }

    // The transcript is what the owner adjudicates against, so it is shown
    // verbatim — it is the record of what was actually said.
    const transcript = el(
      "p",
      "review-card__transcript",
      transaction.transcript !== undefined && transaction.transcript !== ""
        ? `“${transaction.transcript}”`
        : "No transcript recorded.",
    );

    const scores = el("p", "review-card__scores", formatConfidenceBreakdown(transaction));
    scores.title = "Recognition confidence per anchor; the weakest one held this capture";

    const actions = el("div", "review-card__actions");
    let onboardBtn: HTMLButtonElement | null = null;
    const confirmBtn = el("button", "button button--primary button--sm", "Confirm");
    const editBtn = el("button", "button button--secondary button--sm", "Edit & Confirm");
    const rejectBtn = el("button", "button button--secondary button--sm", "Reject");
    const errorMsg = el("span", "review-card__error");
    errorMsg.hidden = true;

    if (!transaction.product_id) {
      onboardBtn = el("button", "button button--primary button--sm", "+ Add to Catalogue & Confirm");
      onboardBtn.addEventListener("click", () => {
        showOnboardProductModal(transaction);
      });
    }

    confirmBtn.addEventListener("click", async () => {
      confirmBtn.disabled = true;
      if (onboardBtn) onboardBtn.disabled = true;
      editBtn.disabled = true;
      rejectBtn.disabled = true;
      confirmBtn.textContent = "Confirming…";
      errorMsg.hidden = true;
      try {
        const updated = await confirmTransaction(transaction.id);
        removeHeldTransaction(updated.id);
        applyLedgerTransaction(updated, false);
      } catch (err: unknown) {
        confirmBtn.disabled = false;
        if (onboardBtn) onboardBtn.disabled = false;
        editBtn.disabled = false;
        rejectBtn.disabled = false;
        confirmBtn.textContent = "Confirm";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Confirmation failed";
      }
    });

    editBtn.addEventListener("click", () => {
      showEditConfirmModal(transaction);
    });

    rejectBtn.addEventListener("click", async () => {
      const notes = window.prompt(
        `Reject transaction ${transaction.id}?\nEnter optional audit notes (e.g. Unrecognized background chatter):`,
      );
      if (notes === null) return;
      confirmBtn.disabled = true;
      if (onboardBtn) onboardBtn.disabled = true;
      editBtn.disabled = true;
      rejectBtn.disabled = true;
      rejectBtn.textContent = "Rejecting…";
      errorMsg.hidden = true;
      try {
        const updated = await rejectTransaction(transaction.id, notes.trim() || undefined);
        removeHeldTransaction(updated.id);
        applyLedgerTransaction(updated, false);
      } catch (err: unknown) {
        confirmBtn.disabled = false;
        if (onboardBtn) onboardBtn.disabled = false;
        editBtn.disabled = false;
        rejectBtn.disabled = false;
        rejectBtn.textContent = "Reject";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Rejection failed";
      }
    });

    if (onboardBtn) {
      actions.append(onboardBtn, confirmBtn, editBtn, rejectBtn, errorMsg);
    } else {
      actions.append(confirmBtn, editBtn, rejectBtn, errorMsg);
    }

    const audioElement = legacyAudioControl(shopId, transaction);

    if (audioElement) {
      card.append(head, transcript, audioElement, scores, actions);
    } else {
      card.append(head, transcript, scores, actions);
    }
    return card;
  }

  async function handleVoidClick(transaction: Transaction): Promise<void> {
    const reason = window.prompt(
      `Void / reverse transaction ${transaction.id} (${transaction.product_name})?\nEnter cancellation reason:`,
      "Customer returned unopened item",
    );
    if (reason === null) return;
    const trimmed = reason.trim() || "Voided by shopkeeper";
    try {
      await reverseTransaction(transaction.id, trimmed);
      void refetch();
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Failed to void transaction");
    }
  }

  function ledgerRow(transaction: Transaction): HTMLTableRowElement {
    const row = el("tr", `table__row table__row--${transaction.status}`);

    const time = cell("table__cell", formatTime(transaction.occurred_at));
    time.title = new Date(transaction.occurred_at).toLocaleString();

    const direction = cell(`table__cell table__cell--${transaction.type}`, formatDirection(transaction.type));
    const product = cell("table__cell");
    product.append(document.createTextNode(transaction.product_name));
    if (transaction.supplier_name) {
      const suppBadge = el("span", "badge badge--accent", `From: ${transaction.supplier_name}`);
      suppBadge.style.marginLeft = "0.5rem";
      product.append(suppBadge);
    }
    if (transaction.customer_name) {
      const custBadge = el("span", "badge badge--warning", `Tab: ${transaction.customer_name}`);
      custBadge.title = `Customer Credit Tab: ${transaction.customer_name}${transaction.customer_id ? ` (${transaction.customer_id})` : ""}`;
      custBadge.style.marginLeft = "0.5rem";
      if (transaction.customer_id) {
        custBadge.style.cursor = "pointer";
        custBadge.addEventListener("click", () => {
          filters.customer_id = transaction.customer_id || "";
          pageOffset = 0;
          void refetch();
        });
      }
      product.append(custBadge);
    }
    if (transaction.receipt_id) {
      const rcptBadge = el("span", "badge badge--mono", "Receipt");
      rcptBadge.title = `Receipt: ${transaction.receipt_id} (click to filter)`;
      rcptBadge.style.cursor = "pointer";
      rcptBadge.style.marginLeft = "0.5rem";
      rcptBadge.addEventListener("click", () => {
        filters.receipt_id = transaction.receipt_id || "";
        pageOffset = 0;
        void refetch();
      });
      product.append(rcptBadge);
    }

    const quantity = cell("table__cell", formatQuantity(transaction.quantity, transaction.unit));
    const amount = cell("table__cell table__cell--amount", formatMoney(transaction.amount));

    const confidence = cell(
      "table__cell",
      `${Math.round(transaction.confidence.overall * 100)}%`,
    );
    confidence.title = formatConfidenceBreakdown(transaction);

    const status = el("span", `badge badge--${transaction.status}`, formatStatus(transaction.status));
    const statusCell = cell("table__cell");
    statusCell.append(status);

    const actionCell = cell("table__cell table__cell--actions");
    if (transaction.status === "confirmed") {
      const voidBtn = el("button", "button button--secondary button--sm", "Void");
      voidBtn.type = "button";
      voidBtn.title = "Void/reverse confirmed transaction and restore inventory";
      voidBtn.addEventListener("click", () => {
        void handleVoidClick(transaction);
      });
      actionCell.append(voidBtn);
    }

    const auditBtn = el("button", "button button--secondary button--sm", "Audit");
    auditBtn.type = "button";
    auditBtn.title = "View review decision audit history";
    auditBtn.addEventListener("click", () => {
      showAuditModal(transaction.id);
    });
    actionCell.append(auditBtn);

    if (hasLegacyAudio(transaction)) {
      const playBtn = el("button", "button button--secondary button--sm button--audio-replay", "▶ Audio");
      playBtn.type = "button";
      playBtn.title = "Listen to the legacy audio snippet recorded for this transaction";
      playBtn.addEventListener("click", () => {
        void playLegacyAudio(shopId, transaction);
      });
      actionCell.append(playBtn);
    }

    row.append(time, direction, product, quantity, amount, confidence, statusCell, actionCell);

    // The date is only interesting for a row that is not from today, so it is
    // carried in the title rather than as another column.
    row.title = `${formatDate(transaction.occurred_at)} ${formatTime(transaction.occurred_at)}`;
    return row;
  }

  function updateExportUrl(): void {
    if (exportLink !== null) {
      exportLink.href = getExportTransactionsUrl(shopId, {
        type: filters.type || undefined,
        status: filters.status || undefined,
        receipt_id: filters.receipt_id || undefined,
        customer_id: filters.customer_id || undefined,
      });
    }
  }

  function updatePaginationControls(): void {
    if (pagePrev !== null) {
      pagePrev.disabled = pageOffset <= 0;
    }
    if (pageNext !== null) {
      pageNext.disabled = pageOffset + transactions.length >= totalCount;
    }
    if (pageInfo !== null) {
      if (totalCount === 0) {
        setText(pageInfo, "Showing 0 of 0");
      } else {
        const start = pageOffset + 1;
        const end = pageOffset + transactions.length;
        if (pendingNewCount > 0 && pageOffset > 0) {
          setText(pageInfo, `Showing ${start}–${end} of ${totalCount} (${pendingNewCount} newer)`);
        } else {
          setText(pageInfo, `Showing ${start}–${end} of ${totalCount}`);
        }
      }
    }
  }

  function renderReviewQueue(): void {
    if (reviewQueue === null) return;
    clear(reviewQueue);

    setText(reviewCount, String(heldTransactions.length));

    if (heldTransactions.length === 0) {
      reviewQueue.append(
        uiStateItem("empty", {
          title: "Nothing waiting on you.",
          detail: "Low-confidence captures land here for a quick decision.",
        }),
      );
      return;
    }
    for (const transaction of heldTransactions) {
      reviewQueue.append(reviewCard(transaction));
    }
  }

  function removeHeldTransaction(id: string): void {
    const idx = heldTransactions.findIndex((t) => t.id === id);
    if (idx !== -1) {
      heldTransactions.splice(idx, 1);
      renderReviewQueue();
    }
  }

  function renderLedger(): void {
    if (ledgerBody === null) return;
    clear(ledgerBody);

    if (filters.customer_id) {
      const bannerRow = el("tr", "table__row");
      const bannerCell = cell("table__cell", `Filtered by Customer Tab: ${filters.customer_id} `);
      bannerCell.colSpan = 8;
      const clearBtn = el("button", "button button--secondary button--sm", "Clear Filter");
      clearBtn.style.marginLeft = "0.75rem";
      clearBtn.addEventListener("click", () => {
        filters.customer_id = "";
        pageOffset = 0;
        pendingNewCount = 0;
        void refetch();
      });
      bannerCell.append(clearBtn);
      bannerRow.append(bannerCell);
      ledgerBody.append(bannerRow);
    }

    if (filters.receipt_id) {
      const bannerRow = el("tr", "table__row");
      const bannerCell = cell("table__cell", `Filtered by Receipt: ${filters.receipt_id} `);
      bannerCell.colSpan = 8;
      const clearBtn = el("button", "button button--secondary button--sm", "Clear Filter");
      clearBtn.style.marginLeft = "0.75rem";
      clearBtn.addEventListener("click", () => {
        filters.receipt_id = "";
        pageOffset = 0;
        pendingNewCount = 0;
        void refetch();
      });
      bannerCell.append(clearBtn);
      bannerRow.append(bannerCell);
      ledgerBody.append(bannerRow);
    }

    if (pendingNewCount > 0 && pageOffset > 0) {
      const pendingRow = el("tr", "table__row");
      const pendingCell = cell("table__cell");
      pendingCell.colSpan = 8;
      const text = el(
        "span",
        "",
        `${pendingNewCount} newer transaction${pendingNewCount > 1 ? "s" : ""} received while browsing history. `,
      );
      const jumpBtn = el("button", "button button--primary button--sm", "Jump to Latest");
      jumpBtn.style.marginLeft = "0.75rem";
      jumpBtn.addEventListener("click", () => {
        pageOffset = 0;
        pendingNewCount = 0;
        void refetch();
      });
      pendingCell.append(text, jumpBtn);
      pendingRow.append(pendingCell);
      ledgerBody.append(pendingRow);
    }

    if (transactions.length === 0) {
      const filtered =
        filters.type !== "" ||
        filters.status !== "" ||
        filters.receipt_id !== "" ||
        (filters.customer_id ?? "") !== "";
      ledgerBody.append(
        uiStateRow(
          "empty",
          filtered
            ? {
                title: "No transactions match these filters.",
                detail: "Clear a filter to see more of the ledger.",
              }
            : {
                title: "No transactions recorded yet.",
                detail: "Sales and restocks appear here as they are captured.",
              },
          8,
        ),
      );
      return;
    }
    for (const transaction of transactions) {
      ledgerBody.append(ledgerRow(transaction));
    }
  }

  function render(): void {
    renderReviewQueue();
    renderLedger();
    updatePaginationControls();
  }

  async function refetch(): Promise<void> {
    try {
      updateExportUrl();
      const [ledgerSnapshot, reviewSnapshot] = await Promise.all([
        fetchTransactions(
          shopId,
          pageSize,
          {
            offset: pageOffset,
            type: filters.type || undefined,
            status: filters.status || undefined,
            receipt_id: filters.receipt_id || undefined,
            customer_id: filters.customer_id || undefined,
          },
          signal,
        ),
        fetchTransactions(
          shopId,
          50,
          {
            status: "review_required",
          },
          signal,
        ),
      ]);
      if (disposed || signal.aborted) return;
      transactions = [...ledgerSnapshot.transactions];
      sortTransactions(transactions);
      totalCount = ledgerSnapshot.total ?? ledgerSnapshot.count;

      heldTransactions = [...reviewSnapshot.transactions];
      sortTransactions(heldTransactions);
      pendingNewCount = 0;

      if (typeof ledgerSnapshot.last_seq === "number") {
        getSocket()?.syncSequence(ledgerSnapshot.last_seq);
      }
      render();
    } catch (error) {
      if (disposed || signal.aborted) return;
      console.error("ListenLedger: could not load transactions", error);
      if (ledgerBody !== null) {
        clear(ledgerBody);
        ledgerBody.append(
          uiStateRow(
            "error",
            {
              title: "Could not load the ledger.",
              detail: "We'll retry automatically when the connection returns.",
              actionLabel: "Retry",
              onAction: () => {
                void refetch();
              },
            },
            8,
          ),
        );
      }
      if (reviewQueue !== null) {
        clear(reviewQueue);
        reviewQueue.append(uiStateItem("error", { title: "Could not load the review queue." }));
      }
    }
  }

  function showOnboardProductModal(transaction: Transaction): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", "1-Tap Add Product to Catalogue & Confirm");
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");
    const help = el(
      "p",
      "modal-help",
      "Create this uncatalogued product in your shop catalogue and confirm the stock movement in one atomic step.",
    );

    const transcriptInfo = el(
      "div",
      "review-card__transcript",
      transaction.transcript ? `Spoken: “${transaction.transcript}”` : `Captured: ${transaction.product_name}`,
    );

    const nameLabel = el("label", "form-field", "Product Name *");
    const nameInput = el("input", "input") as HTMLInputElement;
    nameInput.type = "text";
    nameInput.required = true;
    nameInput.value = transaction.product_name || "";
    nameLabel.append(nameInput);

    const skuLabel = el("label", "form-field", "SKU *");
    const skuInput = el("input", "input") as HTMLInputElement;
    skuInput.type = "text";
    skuInput.required = true;
    const cleanName = (transaction.product_name || "PROD")
      .toUpperCase()
      .replace(/[^A-Z0-9]/g, "-")
      .replace(/-+/g, "-")
      .replace(/^-|-$/g, "")
      .slice(0, 15);
    skuInput.value = `${cleanName || "SKU"}-${Math.floor(1000 + Math.random() * 9000)}`;
    skuLabel.append(skuInput);

    const unitLabel = el("label", "form-field", "Base Unit *");
    const unitSelect = el("select", "input") as HTMLSelectElement;
    for (const u of ALL_UNITS) {
      const opt = new Option(u, u);
      if (transaction.unit === u) opt.selected = true;
      unitSelect.append(opt);
    }
    unitLabel.append(unitSelect);

    const priceLabel = el("label", "form-field", `Retail Selling Price in kobo (${transaction.amount.currency}) *`);
    const priceInput = el("input", "input") as HTMLInputElement;
    priceInput.type = "number";
    priceInput.min = "0";
    const unitCost =
      transaction.quantity > 0
        ? Math.round(transaction.amount.amount / transaction.quantity)
        : transaction.amount.amount;
    priceInput.value = String(unitCost > 0 ? Math.round(unitCost * 1.25) : 100000);
    priceLabel.append(priceInput);

    const qtyLabel = el("label", "form-field", "Restock Quantity *");
    const qtyInput = el("input", "input") as HTMLInputElement;
    qtyInput.type = "number";
    qtyInput.min = "1";
    qtyInput.value = String(transaction.quantity > 0 ? transaction.quantity : 1);
    qtyLabel.append(qtyInput);

    let supplierSection: HTMLElement | null = null;
    if (transaction.supplier_name) {
      supplierSection = el("p", "modal-help", `Spoken Supplier: ${transaction.supplier_name}`);
    }

    const formError = el("p", "form-error");
    formError.hidden = true;

    const actions = el("div", "modal-actions");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Add Product & Confirm Restock");
    submitBtn.type = "submit";
    actions.append(cancelBtn, submitBtn);

    const closeModal = () => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const name = nameInput.value.trim();
      const sku = skuInput.value.trim();
      const baseUnit = unitSelect.value as Unit;
      const priceAmount = Number(priceInput.value);
      const qty = Number(qtyInput.value);

      if (!name) {
        formError.textContent = "Product name is required.";
        formError.hidden = false;
        return;
      }
      if (!sku) {
        formError.textContent = "SKU is required.";
        formError.hidden = false;
        return;
      }

      submitBtn.disabled = true;
      cancelBtn.disabled = true;
      submitBtn.textContent = "Onboarding & Confirming…";
      formError.hidden = true;

      try {
        const updated = await confirmTransaction(transaction.id, {
          create_product: {
            name,
            sku,
            base_unit: baseUnit,
            price: {
              amount: priceAmount,
              currency: transaction.amount.currency,
            },
          },
          quantity: qty,
          unit: baseUnit,
          notes: `1-Tap product onboarding for ${name}`,
        });
        removeHeldTransaction(updated.id);
        applyLedgerTransaction(updated, false);
        closeModal();
      } catch (err: unknown) {
        submitBtn.disabled = false;
        cancelBtn.disabled = false;
        submitBtn.textContent = "Add Product & Confirm Restock";
        formError.hidden = false;
        formError.textContent = err instanceof Error ? err.message : "Onboarding failed";
      }
    });

    const audioContainer = legacyAudioControl(shopId, transaction);
    if (audioContainer) audioContainer.style.marginBottom = "0.75rem";

    if (audioContainer) {
      form.append(help, transcriptInfo, audioContainer, nameLabel, skuLabel, unitLabel, priceLabel, qtyLabel);
    } else {
      form.append(help, transcriptInfo, nameLabel, skuLabel, unitLabel, priceLabel, qtyLabel);
    }
    if (supplierSection) form.append(supplierSection);
    form.append(formError, actions);
    modal.append(header, form);
    overlay.append(modal);
    document.body.append(overlay);
    nameInput.focus();
  }

  function showEditConfirmModal(transaction: Transaction): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", `Edit & Confirm: ${transaction.id}`);
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");
    const help = el(
      "p",
      "modal-help",
      "Select the matched catalogue item, verify quantity and packaging unit, and record human audit notes before applying inventory movement.",
    );

    const transcriptInfo = el(
      "div",
      "review-card__transcript",
      transaction.transcript ? `Spoken: “${transaction.transcript}”` : "No transcript recorded.",
    );

    const productLabel = el("label", "form-field", "Catalogue Product *");
    const productSelect = el("select", "input") as HTMLSelectElement;
    productSelect.append(new Option("Loading products…", ""));
    productLabel.append(productSelect);

    const qtyLabel = el("label", "form-field", "Quantity *");
    const qtyInput = el("input", "input") as HTMLInputElement;
    qtyInput.type = "number";
    qtyInput.min = "1";
    qtyInput.value = String(transaction.quantity > 0 ? transaction.quantity : 1);
    qtyLabel.append(qtyInput);

    const unitLabel = el("label", "form-field", "Unit *");
    const unitSelect = el("select", "input") as HTMLSelectElement;
    for (const u of ALL_UNITS) {
      const opt = new Option(u, u);
      if (transaction.unit === u) opt.selected = true;
      unitSelect.append(opt);
    }
    unitLabel.append(unitSelect);

    const amountLabel = el("label", "form-field", `Total Amount in kobo (${transaction.amount.currency}) *`);
    const amountInput = el("input", "input") as HTMLInputElement;
    amountInput.type = "number";
    amountInput.min = "0";
    amountInput.value = String(transaction.amount.amount);
    amountLabel.append(amountInput);

    const notesLabel = el("label", "form-field", "Audit Notes");
    const notesInput = el("input", "input") as HTMLInputElement;
    notesInput.type = "text";
    notesInput.placeholder = "e.g. Verified with storekeeper; corrected pack to carton";
    notesLabel.append(notesInput);

    const aliasLabel = el("label", "form-field form-field--checkbox");
    const aliasCheckbox = el("input", "checkbox") as HTMLInputElement;
    aliasCheckbox.type = "checkbox";
    aliasCheckbox.id = "save-as-alias-checkbox";
    aliasCheckbox.checked = false;
    const aliasTextSpan = el(
      "span",
      "checkbox-label",
      transaction.product_name
        ? `Save “${transaction.product_name}” as alias for selected product`
        : "Save spoken product name as alias",
    );
    aliasLabel.append(aliasCheckbox, aliasTextSpan);

    const errorMsg = el("div", "form-error");
    errorMsg.hidden = true;

    const footer = el("div", "modal-footer");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Confirm with Corrections");
    submitBtn.type = "submit";
    footer.append(cancelBtn, submitBtn);

    const audioContainer = legacyAudioControl(shopId, transaction);
    if (audioContainer) audioContainer.style.marginBottom = "0.75rem";

    if (audioContainer) {
      form.append(help, transcriptInfo, audioContainer, productLabel, qtyLabel, unitLabel, amountLabel, aliasLabel, notesLabel, errorMsg, footer);
    } else {
      form.append(help, transcriptInfo, productLabel, qtyLabel, unitLabel, amountLabel, aliasLabel, notesLabel, errorMsg, footer);
    }
    modal.append(header, form);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    function populateCorrectionProducts(products: readonly Product[]): void {
      clear(productSelect);
      let foundExisting = false;
      for (const p of products) {
        if (!p.archived_at || p.id === transaction.product_id) {
          const opt = new Option(`${p.name} (${p.sku})`, p.id);
          if (p.id === transaction.product_id) {
            opt.selected = true;
            foundExisting = true;
          }
          productSelect.append(opt);
        }
      }
      if (!foundExisting && transaction.product_name) {
        const rawOpt = new Option(`[Unmatched] ${transaction.product_name}`, "");
        rawOpt.selected = true;
        productSelect.prepend(rawOpt);
      }
    }

    // Pre-populate immediately from local durable cache if available
    void getCachedCatalogue(shopId).then((cached) => {
      if (cached && cached.length > 0 && productSelect.options.length <= 1) {
        populateCorrectionProducts(cached);
      }
    });

    void fetchInventory(shopId).then((snapshot) => {
      void setCachedCatalogue(shopId, snapshot.products);
      populateCorrectionProducts(snapshot.products);
    }).catch(async () => {
      const fallback = await getCachedCatalogue(shopId);
      if (fallback && fallback.length > 0) {
        populateCorrectionProducts(fallback);
      }
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      errorMsg.hidden = true;
      const selectedProductId = productSelect.value;
      if (!selectedProductId) {
        errorMsg.hidden = false;
        errorMsg.textContent = "Please select a catalogue product to confirm";
        return;
      }
      const qty = parseInt(qtyInput.value, 10);
      if (isNaN(qty) || qty <= 0) {
        errorMsg.hidden = false;
        errorMsg.textContent = "Quantity must be greater than zero";
        return;
      }
      const amt = parseInt(amountInput.value, 10);
      if (isNaN(amt) || amt < 0) {
        errorMsg.hidden = false;
        errorMsg.textContent = "Amount must be a non-negative integer";
        return;
      }

      submitBtn.disabled = true;
      submitBtn.textContent = "Confirming…";

      try {
        const saveAsAlias = aliasCheckbox.checked;
        const corrections: ReviewCorrections = {
          product_id: selectedProductId,
          quantity: qty,
          unit: unitSelect.value as Unit,
          amount: {
            amount: amt,
            currency: transaction.amount.currency,
          },
          notes: notesInput.value.trim() || undefined,
          save_as_alias: saveAsAlias ? true : undefined,
          alias_text: saveAsAlias ? (transaction.product_name || undefined) : undefined,
        };
        const updated = await confirmTransaction(transaction.id, corrections);
        removeHeldTransaction(updated.id);
        applyLedgerTransaction(updated, false);
        closeModal();
        void refetch();
      } catch (err: unknown) {
        submitBtn.disabled = false;
        submitBtn.textContent = "Confirm with Corrections";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Confirmation failed";
      }
    });
  }

  function showAuditModal(transactionId: string): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", `Decision Audit: ${transactionId}`);
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const content = el("div", "modal-body");
    content.append(el("p", "modal-help", "Fetching review adjudication audit log…"));

    const footer = el("div", "modal-footer");
    const dismissBtn = el("button", "button button--secondary", "Close");
    dismissBtn.type = "button";
    footer.append(dismissBtn);

    modal.append(header, content, footer);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    dismissBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    void fetchTransactionDecision(transactionId)
      .then((dec: ReviewDecision) => {
        clear(content);
        const list = el("div", "audit-details");
        const dDecision = el("p", "", "Decision: ");
        dDecision.append(el("strong", "", dec.decision));
        const dBy = el(
          "p",
          "",
          `Decided by: ${dec.decided_by} at ${formatDate(dec.decided_at)} ${formatTime(dec.decided_at)}`,
        );
        const dNotes = el("p", "", `Notes: ${dec.notes || "None"}`);
        list.append(dDecision, dBy, dNotes);

        if (dec.decision === "corrected_and_confirmed" && dec.corrected_values) {
          const table = el("table", "table table--compact");
          const thead = el("thead");
          const trh = el("tr");
          trh.append(
            el("th", "", "Field"),
            el("th", "", "Original (Extracted)"),
            el("th", "", "Corrected (Owner)"),
          );
          thead.append(trh);

          const tbody = el("tbody");
          const orig = dec.original_values;
          const corr = dec.corrected_values;

          const addRow = (field: string, before: string, after: string): void => {
            const tr = el("tr");
            tr.append(
              el("td", "table__cell--mono", field),
              el("td", "", before),
              el("td", "table__cell--highlight", after),
            );
            tbody.append(tr);
          };

          addRow(
            "Product",
            orig.product_name || orig.product_id || "—",
            corr.product_name || corr.product_id || "—",
          );
          addRow("Quantity", String(orig.quantity ?? "—"), String(corr.quantity ?? "—"));
          addRow("Unit", String(orig.unit ?? "—"), String(corr.unit ?? "—"));
          if (orig.amount && corr.amount) {
            addRow("Amount", formatMoney(orig.amount), formatMoney(corr.amount));
          }

          table.append(thead, tbody);
          list.append(el("h4", "", "Value Adjustments"), table);
        }
        content.append(list);
      })
      .catch((err: unknown) => {
        clear(content);
        content.append(
          el(
            "p",
            "form-error",
            err instanceof Error
              ? err.message
              : "No decision audit log found for this transaction.",
          ),
        );
      });
  }

  function showManualEntryModal(): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", "Manual Counter Entry (Fallback)");
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");
    const help = el(
      "p",
      "modal-help",
      "Direct counter entry when ambient noise prevents speech capture. Atomically applies stock movements and records a confirmed ledger entry.",
    );

    const offlineNotice = el("div", "badge badge--offline-queue");
    offlineNotice.style.display = "none";

    const typeLabel = el("label", "form-field", "Direction");
    const typeSelect = el("select", "input") as HTMLSelectElement;
    typeSelect.append(
      new Option("Sale (Stock Out)", "sale"),
      new Option("Restock (Stock In)", "restock"),
    );
    typeLabel.append(typeSelect);

    const productLabel = el("label", "form-field", "Product");
    const productSelect = el("select", "input") as HTMLSelectElement;
    productSelect.append(new Option("Loading products…", ""));
    productLabel.append(productSelect);

    const qtyLabel = el("label", "form-field", "Quantity");
    const qtyInput = el("input", "input") as HTMLInputElement;
    qtyInput.type = "number";
    qtyInput.min = "1";
    qtyInput.value = "1";
    qtyLabel.append(qtyInput);

    const unitLabel = el("label", "form-field", "Unit");
    const unitSelect = el("select", "input") as HTMLSelectElement;
    for (const u of ALL_UNITS) {
      unitSelect.append(new Option(u, u));
    }
    unitLabel.append(unitSelect);

    const amountLabel = el("label", "form-field", "Total Amount in kobo (optional: defaults to unit price for sales)");
    const amountInput = el("input", "input") as HTMLInputElement;
    amountInput.type = "number";
    amountInput.placeholder = "e.g. 500000 for NGN 5,000";
    amountLabel.append(amountInput);

    const notesLabel = el("label", "form-field", "Reason / Notes");
    const notesInput = el("input", "input") as HTMLInputElement;
    notesInput.placeholder = "e.g. Customer spoke quietly / Generator noise";
    notesLabel.append(notesInput);

    const statusMsg = el("div", "status-badge status-badge--error");
    statusMsg.hidden = true;

    const actions = el("div", "modal-actions");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Record Movement");
    submitBtn.type = "submit";
    actions.append(cancelBtn, submitBtn);

    form.append(help, offlineNotice, typeLabel, productLabel, qtyLabel, unitLabel, amountLabel, notesLabel, statusMsg, actions);
    modal.append(header, form);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    function populateProducts(products: readonly Product[], isFromCache: boolean): void {
      productSelect.innerHTML = "";
      if (products.length === 0) {
        productSelect.append(new Option("No products in catalogue", ""));
        return;
      }
      for (const p of products) {
        productSelect.append(new Option(`${p.name} (${p.sku})`, p.id));
      }
      unitSelect.value = products[0]?.base_unit ?? "pack";
      if (isFromCache) {
        offlineNotice.style.display = "inline-flex";
        setText(offlineNotice, `📦 Offline Catalogue (${products.length} cached)`);
        offlineNotice.title = "Using offline cached catalogue. Movements will be queued and replayed upon reconnect.";
      } else {
        offlineNotice.style.display = "none";
      }
    }

    // Pre-populate immediately from local durable cache if available (instant offline availability)
    void getCachedCatalogue(shopId).then((cached) => {
      if (cached && cached.length > 0 && productSelect.options.length <= 1) {
        populateProducts(cached, true);
      }
    });

    void fetchInventory(shopId).then((inv) => {
      void setCachedCatalogue(shopId, inv.products);
      populateProducts(inv.products, false);
    }).catch(async () => {
      const fallback = await getCachedCatalogue(shopId);
      if (fallback && fallback.length > 0) {
        populateProducts(fallback, true);
      } else {
        productSelect.innerHTML = "";
        productSelect.append(new Option("Offline: no cached products (connect once to load)", ""));
        statusMsg.hidden = false;
        setText(statusMsg, "Offline: no cached catalogue available for this shop. Please connect to internet once to download products.");
      }
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const prodId = productSelect.value;
      if (!prodId) {
        statusMsg.hidden = false;
        statusMsg.textContent = "Please select a product";
        return;
      }
      const qty = parseInt(qtyInput.value, 10);
      if (isNaN(qty) || qty <= 0) {
        statusMsg.hidden = false;
        statusMsg.textContent = "Quantity must be a positive whole number";
        return;
      }

      submitBtn.disabled = true;
      submitBtn.textContent = "Recording…";
      statusMsg.hidden = true;

      const input: ManualTransactionInput = {
        // P4.8: mint the id once here. The same value is used for the online
        // POST and, if that fails at the transport, for the queued replay — so a
        // committed entry whose response was lost is deduplicated server-side.
        request_id: generateManualRequestId(),
        type: typeSelect.value as TransactionType,
        product_id: prodId,
        quantity: qty,
        unit: unitSelect.value as Unit,
      };
      try {
        const rawAmt = amountInput.value.trim() ? parseInt(amountInput.value.trim(), 10) : undefined;
        if (rawAmt !== undefined && !isNaN(rawAmt)) {
          input.amount = { amount: rawAmt, currency: "NGN" };
        }
        const notes = notesInput.value.trim();
        if (notes) {
          input.notes = notes;
        }
        await createManualTransaction(input);
        closeModal();
        void refetch();
      } catch (err: unknown) {
        if (isNetworkError(err)) {
          // P4.8: manual counter entry is the offline fallback. When the POST
          // cannot reach the server, queue it on-device; it replays into
          // /api/transactions/manual when the network returns.
          const selectedOpt =
            (productSelect.selectedIndex !== undefined && productSelect.selectedIndex >= 0
              ? productSelect.options[productSelect.selectedIndex]
              : undefined) ||
            Array.from(productSelect.options).find((o) => o.value === prodId);
          const optText = selectedOpt?.text ?? "";
          const productLabel = optText.replace(/\s*\([^)]*\)\s*$/, "").trim() || undefined;
          await enqueueManualTransaction(shopId, input, productLabel);
          submitBtn.disabled = false;
          submitBtn.textContent = "Record Movement";
          statusMsg.hidden = false;
          statusMsg.className = "status-badge status-badge--warning";
          statusMsg.textContent =
            "No connection — entry queued on this device. It syncs automatically when the network returns.";
          return;
        }
        submitBtn.disabled = false;
        submitBtn.textContent = "Record Movement";
        statusMsg.hidden = false;
        statusMsg.textContent = err instanceof Error ? err.message : "Failed to record manual transaction";
      }
    });
  }

  /** Inserts, updates, or filters transactions for the active ledger view. */
  function applyLedgerTransaction(transaction: Transaction, isNew: boolean): void {
    const res = applyFilteredTransactionToList(
      transactions,
      transaction,
      isNew,
      filters,
      pageOffset,
      pageSize,
    );
    totalCount += res.totalIncrement;
    if (totalCount < 0) totalCount = 0;
    if (pageOffset > 0 && isNew && res.totalIncrement > 0) {
      pendingNewCount += 1;
    }
    renderLedger();
    updatePaginationControls();
  }

  const onTransactionReceived = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    applyLedgerTransaction(detail.data, true);
  };

  const onTransactionReviewRequired = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    const txn = detail.data;
    applyTransactionToList(heldTransactions, txn, true, MAX_TRANSACTION_LIMIT);
    renderReviewQueue();
    applyLedgerTransaction(txn, true);
  };

  const onTransactionUpdated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    const txn = detail.data;
    if (txn.status !== "review_required") {
      removeHeldTransaction(txn.id);
    } else {
      applyTransactionToList(heldTransactions, txn, false, MAX_TRANSACTION_LIMIT);
      renderReviewQueue();
    }
    applyLedgerTransaction(txn, false);
  };

  const onTypeFilter = (): void => {
    filters.type = typeFilter?.value ?? "";
    pageOffset = 0;
    pendingNewCount = 0;
    void refetch();
  };

  const onStatusFilter = (): void => {
    filters.status = statusFilter?.value ?? "";
    pageOffset = 0;
    pendingNewCount = 0;
    void refetch();
  };

  const onPrevPage = (): void => {
    if (pageOffset > 0) {
      pageOffset = Math.max(0, pageOffset - pageSize);
      if (pageOffset === 0) pendingNewCount = 0;
      void refetch();
    }
  };

  const onNextPage = (): void => {
    if (pageOffset + transactions.length < totalCount) {
      pageOffset += pageSize;
      void refetch();
    }
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    if (detail.resyncRequired) {
      void refetch();
    }
    // A socket reconnect usually means the network is back — replay anything
    // queued offline (spoken captures and manual entries alike) for this shop.
    if (detail.status === "open") {
      void syncOfflineQueue({ shopId, getActiveShopId: () => (disposed ? null : shopId) });
    }
  };

  const onOnlineBack = (): void => {
    void syncOfflineQueue({ shopId, getActiveShopId: () => (disposed ? null : shopId) });
  };

  const onManualClick = (): void => {
    showManualEntryModal();
  };

  window.addEventListener("ll:transaction-received", onTransactionReceived);
  window.addEventListener("ll:transaction-review-required", onTransactionReviewRequired);
  window.addEventListener("ll:transaction-updated", onTransactionUpdated);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  window.addEventListener("online", onOnlineBack);
  typeFilter?.addEventListener("change", onTypeFilter);
  statusFilter?.addEventListener("change", onStatusFilter);
  pagePrev?.addEventListener("click", onPrevPage);
  pageNext?.addEventListener("click", onNextPage);
  manualBtn?.addEventListener("click", onManualClick);

  void refetch();

  return () => {
    disposed = true;
    setActiveQueueShop(null);
    abortController.abort();
    window.removeEventListener("ll:transaction-received", onTransactionReceived);
    window.removeEventListener("ll:transaction-review-required", onTransactionReviewRequired);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    window.removeEventListener("online", onOnlineBack);
    typeFilter?.removeEventListener("change", onTypeFilter);
    statusFilter?.removeEventListener("change", onStatusFilter);
    pagePrev?.removeEventListener("click", onPrevPage);
    pageNext?.removeEventListener("click", onNextPage);
    manualBtn?.removeEventListener("click", onManualClick);
  };
}
