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
  getExportTransactionsUrl,
  rejectTransaction,
  reverseTransaction,
  type ManualTransactionInput,
  type ReviewCorrections,
  type ReviewDecision,
  type Transaction,
  type TransactionType,
  type Unit,
} from "../api.js";
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
import type { ViewCleanup } from "./index.js";

interface Filters {
  type: string;
  status: string;
  receipt_id: string;
  customer_id?: string;
}

export function mountTransactions(root: HTMLElement, shopId: string): ViewCleanup {
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
      reviewQueue.append(el("li", "review-list__empty", "Nothing waiting on you."));
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
      const row = el("tr", "table__empty");
      const cellElement = cell("", "No transactions match these filters.");
      cellElement.colSpan = 8;
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
        const row = el("tr", "table__empty");
        const cellElement = cell("", "Could not reach the server. Retrying on reconnect.");
        cellElement.className = "table__error";
        cellElement.colSpan = 8;
        row.append(cellElement);
        ledgerBody.append(row);
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
    for (const u of ["piece", "pack", "carton", "bottle", "crate", "tin"] as const) {
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

    form.append(help, transcriptInfo, nameLabel, skuLabel, unitLabel, priceLabel, qtyLabel);
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
    for (const u of ["piece", "pack", "carton", "bottle", "crate"] as const) {
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

    const errorMsg = el("div", "form-error");
    errorMsg.hidden = true;

    const footer = el("div", "modal-footer");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Confirm with Corrections");
    submitBtn.type = "submit";
    footer.append(cancelBtn, submitBtn);

    form.append(help, transcriptInfo, productLabel, qtyLabel, unitLabel, amountLabel, notesLabel, errorMsg, footer);
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

    void fetchInventory(shopId).then((snapshot) => {
      clear(productSelect);
      let foundExisting = false;
      for (const p of snapshot.products) {
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
        const corrections: ReviewCorrections = {
          product_id: selectedProductId,
          quantity: qty,
          unit: unitSelect.value as Unit,
          amount: {
            amount: amt,
            currency: transaction.amount.currency,
          },
          notes: notesInput.value.trim() || undefined,
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
  };

  const onManualClick = (): void => {
    showManualEntryModal();
  };

  window.addEventListener("ll:transaction-received", onTransactionReceived);
  window.addEventListener("ll:transaction-review-required", onTransactionReviewRequired);
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
    abortController.abort();
    window.removeEventListener("ll:transaction-received", onTransactionReceived);
    window.removeEventListener("ll:transaction-review-required", onTransactionReviewRequired);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    typeFilter?.removeEventListener("change", onTypeFilter);
    statusFilter?.removeEventListener("change", onStatusFilter);
    pagePrev?.removeEventListener("click", onPrevPage);
    pageNext?.removeEventListener("click", onNextPage);
    manualBtn?.removeEventListener("click", onManualClick);
  };
}
