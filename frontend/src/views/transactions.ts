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
  fetchTransactions,
  getExportTransactionsUrl,
  rejectTransaction,
  type Transaction,
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
import { CONNECTION_STATUS_EVENT, type ConnectionStatusDetail } from "../ws.js";
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

    row.append(time, direction, product, quantity, amount, confidence, statusCell);

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
      render();
    } catch (error) {
      if (disposed) return;
      console.error("ListenLedger: could not load transactions", error);
      if (ledgerBody !== null) {
        clear(ledgerBody);
        const row = el("tr", "table__empty");
        const cellElement = cell("", "Could not reach the server. Retrying on reconnect.");
        cellElement.colSpan = 7;
        row.append(cellElement);
        ledgerBody.append(row);
      }
    }
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

  window.addEventListener("ll:transaction-received", onTransaction);
  window.addEventListener("ll:transaction-review-required", onTransaction);
  window.addEventListener("ll:transaction-updated", onTransactionUpdated);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  typeFilter?.addEventListener("change", onTypeFilter);
  statusFilter?.addEventListener("change", onStatusFilter);
  pagePrev?.addEventListener("click", onPrevPage);
  pageNext?.addEventListener("click", onNextPage);

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
  };
}
