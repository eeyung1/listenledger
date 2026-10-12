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
  fetchInventory,
  fetchTransactions,
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
import { CONNECTION_STATUS_EVENT, type ConnectionStatus, type ConnectionStatusDetail } from "../ws.js";
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
}

export function mountHome(root: HTMLElement, shopId: string): ViewCleanup {
  const state: HomeState = { transactions: [], products: [] };
  let disposed = false;

  const feedList = hook(root, "live-feed");
  const feedEmpty = hook(root, "live-feed-empty");
  const lowStockList = hook(root, "low-stock-list");
  const connectionLabel = hook(root, "connection-status");

  function renderKPIs(): void {
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

  function render(): void {
    renderKPIs();
    renderFeed();
    renderLowStock();
  }

  /** Fetches both snapshots and replaces the view's state wholesale. */
  async function refetch(): Promise<void> {
    try {
      const [transactions, inventory] = await Promise.all([
        fetchTransactions(shopId, MAX_TRANSACTION_LIMIT),
        fetchInventory(shopId),
      ]);
      if (disposed) return;

      state.transactions = [...transactions.transactions];
      state.products = [...inventory.products];
      render();
    } catch (error) {
      if (disposed) return;
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

  /** Inserts a transaction, keeping the feed newest-first. */
  function applyTransaction(transaction: Transaction, isNew: boolean): void {
    if (isNew) {
      state.transactions.unshift(transaction);
    } else {
      const index = state.transactions.findIndex((existing) => existing.id === transaction.id);
      if (index === -1) {
        state.transactions.unshift(transaction);
      } else {
        state.transactions[index] = transaction;
      }
    }

    // The KPIs depend on the set, so they are recomputed rather than adjusted
    // incrementally — a running total is one missed event away from drifting.
    render();
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
    window.removeEventListener("ll:transaction-received", onTransactionReceived);
    window.removeEventListener("ll:transaction-review-required", onTransactionReceived);
    window.removeEventListener("ll:transaction-updated", onTransactionUpdated);
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  };
}
