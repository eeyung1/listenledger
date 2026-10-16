/**
 * Display formatting and DOM helpers shared by the views.
 *
 * Two rules hold throughout:
 *
 *   - Money is never handled as a float except at the moment it is printed.
 *     Amounts arrive as integer minor units and are divided by 100 exactly
 *     once, inside formatMoney. Summing floats and then printing them is how a
 *     day's revenue ends up a kobo short of the ledger.
 *
 *   - Text from the server is never interpolated into markup. Product names and
 *     transcripts originate as speech, so they are attacker-influenced strings;
 *     every one of them reaches the DOM through textContent, which cannot open
 *     a tag. This is why the views build elements rather than HTML strings.
 */

import type { DailySummary, Expense, ExpenseCategory, Money, Product, Transaction, TransactionStatus, Unit } from "./api.js";

/** Minor units per major unit, for the two-decimal currencies in V1. */
const MINOR_UNITS_PER_MAJOR = 100;

/**
 * Formats an amount for display, e.g. NGN 4,000.00.
 *
 * The division by 100 is the only float arithmetic the dashboard performs on
 * money, and it happens once, on a value being rendered — never on a value
 * being accumulated.
 */
export function formatMoney(money: Money): string {
  const major = money.amount / MINOR_UNITS_PER_MAJOR;
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: money.currency,
      currencyDisplay: "code",
    }).format(major);
  } catch {
    // Intl throws on a currency code it does not know. An unrecognised code is
    // still worth showing — it is the code the ledger actually holds.
    return `${money.currency} ${major.toFixed(2)}`;
  }
}

/** Formats a recognition score as a whole percentage, e.g. 0.98 -> "98%". */
export function formatConfidence(score: number | undefined): string {
  if (score === undefined) {
    // "Not scored" and "scored zero" are different claims; the server omits
    // the former rather than sending 0, and this is where that survives.
    return "—";
  }
  return `${Math.round(score * 100)}%`;
}

/** Formats an ISO timestamp as a local wall-clock time, e.g. "14:32". */
export function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** Formats an ISO timestamp as a local calendar day, e.g. "12 Oct". */
export function formatDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

/** Human-readable labels for the unit vocabulary. */
const UNIT_LABELS: Readonly<Record<Unit, string>> = {
  piece: "piece",
  pack: "pack",
  carton: "carton",
  bottle: "bottle",
  crate: "crate",
  tin: "tin",
  bag: "bag",
  sachet: "sachet",
  roll: "roll",
  painter: "painter",
  mudu: "mudu",
  derica: "derica",
  rubber: "rubber",
};

export function formatUnit(unit: Unit, quantity: number): string {
  const label = UNIT_LABELS[unit] ?? unit;
  return quantity === 1 ? label : `${label}s`;
}

export function formatQuantity(quantity: number, unit: Unit): string {
  return `${quantity} ${formatUnit(unit, quantity)}`;
}

/** Human-readable labels for the lifecycle states. */
const STATUS_LABELS: Readonly<Record<TransactionStatus, string>> = {
  confirmed: "Confirmed",
  review_required: "Review required",
  rejected: "Rejected",
  cancelled: "Cancelled",
};

export function formatStatus(status: TransactionStatus): string {
  return STATUS_LABELS[status] ?? status;
}

/** Direction label: stock out or stock in. */
export function formatDirection(type: Transaction["type"]): string {
  return type === "restock" ? "Restock" : "Sale";
}

/**
 * Renders a capture's per-anchor scores as one line.
 *
 * The anchors are shown separately rather than as an average, because the
 * average is what hides which field the system actually doubted: blueprint §11
 * holds a capture whose product matched at 55% while its quantity matched at
 * 98%, and the owner needs to see that it was the product that failed.
 */
export function formatConfidenceBreakdown(transaction: Transaction): string {
  const { product, quantity, amount } = transaction.confidence;
  return [
    `product ${formatConfidence(product)}`,
    `quantity ${formatConfidence(quantity)}`,
    `amount ${formatConfidence(amount)}`,
  ].join(" · ");
}

/**
 * Reports whether a product has reached its reorder level.
 *
 * Mirrors models.Product.IsLowStock on the server, which is the authority for
 * the low_stock_count the inventory endpoint returns. A threshold of zero means
 * no alert is configured, so the product never reports as low.
 */
export function isLowStock(product: Product): boolean {
  const threshold = product.low_stock_threshold ?? 0;
  return threshold > 0 && product.quantity <= threshold;
}

/**
 * Reports whether a timestamp falls on the current local day.
 *
 * Local, not UTC: "sales today" means the trading day the owner is standing in,
 * and a shop in Lagos would otherwise see the figure reset mid-afternoon.
 */
export function isToday(iso: string): boolean {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return false;
  const now = new Date();
  return (
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  );
}

/**
 * Creates an element with optional class and text.
 *
 * The text arrives via textContent, so a product name containing markup is
 * rendered as the characters the owner actually said.
 */
export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const element = document.createElement(tag);
  if (className !== undefined && className !== "") {
    element.className = className;
  }
  if (text !== undefined) {
    element.textContent = text;
  }
  return element;
}

/** Creates a table cell with optional class and text. */
export function cell(className: string, text?: string): HTMLTableCellElement {
  const element = el("td", className, text);
  return element;
}

/**
 * Looks up a mount point inside a mounted view.
 *
 * Returns null rather than throwing when a hook is missing: a template that has
 * drifted from the code should degrade to a view that does not update, not a
 * view that fails to render at all.
 */
export function hook<T extends HTMLElement = HTMLElement>(
  root: ParentNode,
  name: string,
): T | null {
  return root.querySelector<T>(`[data-hook="${name}"]`);
}

/** Removes every child of a node. */
export function clear(node: Element | null): void {
  if (node !== null) node.replaceChildren();
}

/** Shows or hides an element via the `hidden` attribute. */
export function setHidden(element: Element | null, hidden: boolean): void {
  if (element === null) return;
  if (hidden) {
    element.setAttribute("hidden", "");
  } else {
    element.removeAttribute("hidden");
  }
}

/** Writes text into an element if both the element and the value exist. */
export function setText(element: Element | null, text: string): void {
  if (element !== null) element.textContent = text;
}

/** Formats an expense category into human-friendly description text. */
export function formatExpenseCategory(cat: ExpenseCategory): string {
  switch (cat) {
    case "generator_fuel":
      return "Generator Fuel & Oil";
    case "store_rent":
      return "Store Rent & Rates";
    case "transport_logistics":
      return "Transport & Haulage";
    case "packaging_supplies":
      return "Packaging & Bags";
    case "staff_wages":
      return "Staff Daily Wages";
    case "utilities":
      return "Power & Water Utilities";
    case "repairs":
      return "Repairs & Maintenance";
    case "other":
    default:
      return "Other Operational Cost";
  }
}

export interface ComputedKPIs {
  readonly revenue: Money;
  readonly salesCount: number;
  readonly expenses: Money;
  readonly expensesCount: number;
  readonly netProfit: Money;
  readonly reviewCount: number;
  readonly lowStockCount: number;
}

/**
 * Computes today's overview KPIs.
 * When daily summary is provided (server-authoritative), it takes precedence.
 * Otherwise, derives metrics from the locally held transactions, products, and expenses.
 */
export function computeKPIs(
  transactions: readonly Transaction[],
  products: readonly Product[],
  summary?: DailySummary,
  expenses?: readonly Expense[],
): ComputedKPIs {
  if (summary !== undefined && summary.today_sales_amount !== undefined) {
    const expensesMoney = summary.today_expenses_amount ?? { amount: 0, currency: summary.currency || "NGN" };
    const expensesCount = summary.today_expenses_count ?? 0;
    const netProfit = summary.today_net_profit ?? {
      amount: summary.today_sales_amount.amount - expensesMoney.amount,
      currency: summary.currency,
    };
    return {
      revenue: summary.today_sales_amount,
      salesCount: summary.today_sales_count,
      expenses: expensesMoney,
      expensesCount,
      netProfit,
      reviewCount: summary.pending_reviews,
      lowStockCount: summary.low_stock_count,
    };
  }

  const todaySales = transactions.filter(
    (t) => t.type === "sale" && t.status === "confirmed" && isToday(t.occurred_at),
  );
  const revenueAmount = todaySales.reduce((total, t) => total + t.amount.amount, 0);
  const currency = todaySales[0]?.amount.currency ?? "NGN";

  const todayExpenses = (expenses ?? []).filter((e) => isToday(e.occurred_at));
  const expensesAmount = todayExpenses.reduce((total, e) => total + e.amount.amount, 0);
  const netProfitAmount = revenueAmount - expensesAmount;

  const reviewCount = transactions.filter((t) => t.status === "review_required").length;
  const lowStockCount = products.filter(isLowStock).length;

  return {
    revenue: { amount: revenueAmount, currency },
    salesCount: todaySales.length,
    expenses: { amount: expensesAmount, currency },
    expensesCount: todayExpenses.length,
    netProfit: { amount: netProfitAmount, currency },
    reviewCount,
    lowStockCount,
  };
}

/**
 * Sorts transactions deterministically: newest first by occurred_at, with id as tie-breaker.
 */
export function sortTransactions(list: Transaction[]): void {
  list.sort((a, b) => {
    const timeDiff = new Date(b.occurred_at).getTime() - new Date(a.occurred_at).getTime();
    if (timeDiff !== 0) return timeDiff;
    return b.id.localeCompare(a.id);
  });
}

export interface ApplyTransactionResult {
  readonly isDuplicate: boolean;
  readonly totalIncrement: number;
}

/**
 * Inserts or updates a transaction in an existing list, deduplicating by ID,
 * sorting deterministically, and enforcing an upper bound.
 */
export function applyTransactionToList(
  list: Transaction[],
  transaction: Transaction,
  isNew: boolean,
  limit?: number,
): ApplyTransactionResult {
  const index = list.findIndex((existing) => existing.id === transaction.id);
  let isDuplicate = false;
  let totalIncrement = 0;

  if (index === -1) {
    list.push(transaction);
    if (isNew) {
      totalIncrement = 1;
    }
  } else {
    list[index] = transaction;
    isDuplicate = true;
  }

  sortTransactions(list);
  if (limit !== undefined && list.length > limit) {
    list.length = limit;
  }

  return { isDuplicate, totalIncrement };
}

export interface TransactionFilters {
  readonly type?: string;
  readonly status?: string;
  readonly receipt_id?: string;
  readonly from?: string;
  readonly to?: string;
}

/**
 * Checks whether a transaction satisfies active client-side filters.
 * An empty string or undefined filter matches all values.
 */
export function matchesTransactionFilters(
  transaction: Transaction,
  filters: TransactionFilters,
): boolean {
  if (filters.type !== undefined && filters.type !== "" && transaction.type !== filters.type) {
    return false;
  }
  if (filters.status !== undefined && filters.status !== "" && transaction.status !== filters.status) {
    return false;
  }
  if (filters.receipt_id !== undefined && filters.receipt_id !== "") {
    if (transaction.receipt_id !== filters.receipt_id) {
      return false;
    }
  }
  if (filters.from !== undefined && filters.from !== "") {
    const fromTime = new Date(filters.from).getTime();
    if (!Number.isNaN(fromTime) && new Date(transaction.occurred_at).getTime() < fromTime) {
      return false;
    }
  }
  if (filters.to !== undefined && filters.to !== "") {
    const toTime = new Date(filters.to).getTime();
    if (!Number.isNaN(toTime) && new Date(transaction.occurred_at).getTime() > toTime) {
      return false;
    }
  }
  return true;
}

export interface FilteredApplyResult {
  readonly action: "inserted" | "updated" | "removed" | "ignored";
  readonly totalIncrement: number;
}

/**
 * Inserts, updates, or removes a transaction in an existing list according to active
 * filter conditions and pagination offset:
 * - If an existing transaction is updated and still matches filters, it is updated in-place.
 * - If an existing transaction is updated but no longer matches filters (e.g. reviewed/confirmed),
 *   it is removed from the filtered list and decrements the filter total.
 * - If a new transaction does not match active filters, it is ignored (0 total increment).
 * - If a new transaction matches active filters:
 *   - On pageOffset === 0 (newest page): it is inserted, deterministically sorted, and clamped to limit.
 *   - On pageOffset > 0 (subsequent pages): it increments totalIncrement by 1 without disrupting the current page slice.
 */
export function applyFilteredTransactionToList(
  list: Transaction[],
  transaction: Transaction,
  isNew: boolean,
  filters: TransactionFilters,
  pageOffset: number = 0,
  limit?: number,
): FilteredApplyResult {
  const matches = matchesTransactionFilters(transaction, filters);
  const index = list.findIndex((existing) => existing.id === transaction.id);

  if (index !== -1) {
    if (matches) {
      list[index] = transaction;
      sortTransactions(list);
      return { action: "updated", totalIncrement: 0 };
    } else {
      list.splice(index, 1);
      return { action: "removed", totalIncrement: -1 };
    }
  }

  if (!matches) {
    return { action: "ignored", totalIncrement: 0 };
  }

  // Matches active filters but user is looking at older paginated history
  if (pageOffset > 0) {
    return { action: "ignored", totalIncrement: isNew ? 1 : 0 };
  }

  // Matches active filters on the newest page
  list.push(transaction);
  sortTransactions(list);
  if (limit !== undefined && list.length > limit) {
    list.length = limit;
  }
  return { action: "inserted", totalIncrement: isNew ? 1 : 0 };
}
