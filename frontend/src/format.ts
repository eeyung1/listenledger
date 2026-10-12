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

import type { Money, Product, Transaction, TransactionStatus, Unit } from "./api.js";

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
