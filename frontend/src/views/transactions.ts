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
  confirmTransaction,
  createManualTransaction,
  fetchInventory,
  fetchTransactions,
  getExportTransactionsUrl,
  rejectTransaction,
  reverseTransaction,
  type ManualTransactionInput,
  type Transaction,
  type TransactionType,
  type Unit,
} from "../api.js";
import {
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
} from "../format.js";
import { CONNECTION_STATUS_EVENT, getSocket, type ConnectionStatusDetail } from "../ws.js";
import type { ViewCleanup } from "./index.js";

interface Filters {
  type: string;
  status: string;
}

export function mountTransactions(root: HTMLElement, shopId: string): ViewCleanup {
  let transactions: Transaction[] = [];
  let disposed = false;
  let pageOffset = 0;
  const pageSize = 50;
  let totalCount = 0;

  const filters: Filters = { type: "", status: "" };

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
    const confirmBtn = el("button", "button button--primary button--sm", "Confirm");
    const rejectBtn = el("button", "button button--secondary button--sm", "Reject");
    const errorMsg = el("span", "review-card__error");
    errorMsg.hidden = true;

    confirmBtn.addEventListener("click", async () => {
      confirmBtn.disabled = true;
      rejectBtn.disabled = true;
      confirmBtn.textContent = "Confirming…";
      errorMsg.hidden = true;
      try {
        const updated = await confirmTransaction(transaction.id);
        applyTransaction(updated, false);
      } catch (err: unknown) {
        confirmBtn.disabled = false;
        rejectBtn.disabled = false;
        confirmBtn.textContent = "Confirm";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Confirmation failed";
      }
    });

    rejectBtn.addEventListener("click", async () => {
      confirmBtn.disabled = true;
      rejectBtn.disabled = true;
      rejectBtn.textContent = "Rejecting…";
      errorMsg.hidden = true;
      try {
        const updated = await rejectTransaction(transaction.id);
        applyTransaction(updated, false);
      } catch (err: unknown) {
        confirmBtn.disabled = false;
        rejectBtn.disabled = false;
        rejectBtn.textContent = "Reject";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Rejection failed";
      }
    });

    actions.append(confirmBtn, rejectBtn, errorMsg);
    card.append(head, transcript, scores, actions);
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
    const product = cell("table__cell", transaction.product_name);
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

    const actionCell = cell("table__cell");
    if (transaction.status === "confirmed") {
      const voidBtn = el("button", "button button--secondary button--sm", "Void");
      voidBtn.type = "button";
      voidBtn.title = "Void/reverse confirmed transaction and restore inventory";
      voidBtn.addEventListener("click", () => {
        void handleVoidClick(transaction);
      });
      actionCell.append(voidBtn);
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
        setText(pageInfo, `Showing ${start}–${end} of ${totalCount}`);
      }
    }
  }

  function renderReviewQueue(): void {
    if (reviewQueue === null) return;
    clear(reviewQueue);

    const held = transactions.filter((transaction) => transaction.status === "review_required");
    setText(reviewCount, String(held.length));

    if (held.length === 0) {
      reviewQueue.append(el("li", "review-list__empty", "Nothing waiting on you."));
      return;
    }
    for (const transaction of held) {
      reviewQueue.append(reviewCard(transaction));
    }
  }

  function renderLedger(): void {
    if (ledgerBody === null) return;
    clear(ledgerBody);

    if (transactions.length === 0) {
      const row = el("tr", "table__empty");
      const cellElement = cell("", "No transactions match these filters.");
      cellElement.colSpan = 7;
      row.append(cellElement);
      ledgerBody.append(row);
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
      const snapshot = await fetchTransactions(shopId, pageSize, {
        offset: pageOffset,
        type: filters.type || undefined,
        status: filters.status || undefined,
      });
      if (disposed) return;
      transactions = [...snapshot.transactions];
      totalCount = snapshot.total ?? snapshot.count;
      if (typeof snapshot.last_seq === "number") {
        getSocket()?.syncSequence(snapshot.last_seq);
      }
      render();
    } catch (error) {
      if (disposed) return;
      console.error("ListenLedger: could not load transactions", error);
      if (ledgerBody !== null) {
        clear(ledgerBody);
        const row = el("tr", "table__empty");
        const cellElement = cell("", "Could not reach the server. Retrying on reconnect.");
        cellElement.colSpan = 8;
        row.append(cellElement);
        ledgerBody.append(row);
      }
    }
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
    for (const u of ["piece", "pack", "carton", "bottle", "crate"]) {
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

    form.append(help, typeLabel, productLabel, qtyLabel, unitLabel, amountLabel, notesLabel, statusMsg, actions);
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

    void fetchInventory(shopId).then((inv) => {
      productSelect.innerHTML = "";
      if (inv.products.length === 0) {
        productSelect.append(new Option("No products in catalogue", ""));
        return;
      }
      for (const p of inv.products) {
        productSelect.append(new Option(`${p.name} (${p.sku})`, p.id));
      }
      unitSelect.value = inv.products[0]?.base_unit ?? "pack";
    }).catch(() => {
      productSelect.innerHTML = "";
      productSelect.append(new Option("Failed to load products", ""));
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

      try {
        const input: ManualTransactionInput = {
          type: typeSelect.value as TransactionType,
          product_id: prodId,
          quantity: qty,
          unit: unitSelect.value as Unit,
        };
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
        submitBtn.disabled = false;
        submitBtn.textContent = "Record Movement";
        statusMsg.hidden = false;
        statusMsg.textContent = err instanceof Error ? err.message : "Failed to record manual transaction";
      }
    });
  }

  /** Inserts or replaces a transaction, keeping the list newest-first. */
  function applyTransaction(transaction: Transaction, isNew: boolean): void {
    const index = transactions.findIndex((existing) => existing.id === transaction.id);
    if (index === -1) {
      if (isNew) {
        transactions.unshift(transaction);
        totalCount++;
      } else {
        transactions.push(transaction);
      }
    } else {
      transactions[index] = transaction;
    }

    render();
  }

  const onTransaction = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    applyTransaction(detail.data, true);
  };

  const onTransactionUpdated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Transaction }>).detail;
    applyTransaction(detail.data, false);
  };

  const onTypeFilter = (): void => {
    filters.type = typeFilter?.value ?? "";
    pageOffset = 0;
    void refetch();
  };

  const onStatusFilter = (): void => {
    filters.status = statusFilter?.value ?? "";
    pageOffset = 0;
    void refetch();
  };

  const onPrevPage = (): void => {
    if (pageOffset > 0) {
      pageOffset = Math.max(0, pageOffset - pageSize);
      void refetch();
    }
  };

  const onNextPage = (): void => {
    if (pageOffset + pageSize < totalCount) {
      pageOffset += pageSize;
      void refetch();
    }
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    if (detail.resyncRequired) {
      void refetch();
    }
  };

  const onManualClick = (): void => {
    showManualEntryModal();
  };

  window.addEventListener("ll:transaction-received", onTransaction);
  window.addEventListener("ll:transaction-review-required", onTransaction);
  window.addEventListener("ll:transaction-updated", onTransactionUpdated);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  typeFilter?.addEventListener("change", onTypeFilter);
  statusFilter?.addEventListener("change", onStatusFilter);
  pagePrev?.addEventListener("click", onPrevPage);
  pageNext?.addEventListener("click", onNextPage);
  manualBtn?.addEventListener("click", onManualClick);

  void refetch();

  return () => {
    disposed = true;
    window.removeEventListener("ll:transaction-received", onTransaction);
    window.removeEventListener("ll:transaction-review-required", onTransaction);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    typeFilter?.removeEventListener("change", onTypeFilter);
    statusFilter?.removeEventListener("change", onStatusFilter);
    pagePrev?.removeEventListener("click", onPrevPage);
    pageNext?.removeEventListener("click", onNextPage);
    manualBtn?.removeEventListener("click", onManualClick);
  };
}
