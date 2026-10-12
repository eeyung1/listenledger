/**
 * Typed REST client for the orchestrator's snapshot endpoints.
 *
 * These types mirror backend/models and backend/api. They are the contract
 * between the two halves of the system, so a change to a Go JSON tag is a
 * change here: the compiler is the only thing that catches the dashboard
 * reading a field the server stopped sending.
 *
 * Everything here is read-only. Writes go through the edge capture path; the
 * dashboard observes them over the WebSocket rather than issuing them itself.
 */

/** An amount in the *minor* unit of its currency: NGN 4,000 is 400000. */
export interface Money {
  readonly amount: number;
  readonly currency: string;
}

export type TransactionType = "sale" | "restock";

export type TransactionStatus = "confirmed" | "review_required" | "rejected" | "cancelled";

export type Unit = "piece" | "pack" | "carton" | "bottle" | "crate";

/**
 * Per-anchor recognition scores, as fractions between 0 and 1.
 *
 * A missing per-anchor score means "not scored", which is not the same as
 * "scored zero" — the server omits it rather than sending 0, and the review
 * queue renders it as an em dash rather than as a confident zero.
 */
export interface Confidence {
  readonly overall: number;
  readonly product?: number;
  readonly quantity?: number;
  readonly amount?: number;
}

/** Containment rule: one `from` unit contains `factor` of the `to` unit. */
export interface PackagingRelation {
  readonly from: Unit;
  readonly to: Unit;
  readonly factor: number;
}

export interface Transaction {
  readonly id: string;
  readonly shop_id: string;
  readonly type: TransactionType;
  /** Absent when the capture did not resolve against the catalogue. */
  readonly product_id?: string;
  readonly product_name: string;
  readonly quantity: number;
  readonly unit: Unit;
  readonly amount: Money;
  readonly confidence: Confidence;
  readonly status: TransactionStatus;
  readonly occurred_at: string;
  readonly created_at: string;
  readonly transcript?: string;
}

export interface Product {
  readonly id: string;
  readonly shop_id: string;
  readonly name: string;
  readonly sku: string;
  readonly aliases: readonly string[];
  readonly price: Money;
  readonly quantity: number;
  readonly base_unit: Unit;
  readonly packaging?: readonly PackagingRelation[];
  readonly low_stock_threshold?: number;
}

export interface TransactionsSnapshot {
  readonly shop_id: string;
  readonly transactions: readonly Transaction[];
  readonly count: number;
  readonly limit: number;
  /**
   * The instant the snapshot was taken. A client resyncing after a stream gap
   * uses it to discard events older than this: they are already reflected in
   * the snapshot, and applying them again would double-count.
   */
  readonly generated_at: string;
}

export interface InventorySnapshot {
  readonly shop_id: string;
  readonly products: readonly Product[];
  readonly count: number;
  readonly low_stock_count: number;
  readonly generated_at: string;
}

/**
 * The largest page the server will return; `limit` is clamped to this.
 *
 * The overview asks for the maximum page because it derives its figures from
 * the rows it receives, and a smaller page would quietly under-report a busy
 * day. The real fix is a server-side aggregate endpoint; until one exists this
 * is the bound on how far those figures can be trusted.
 */
export const MAX_TRANSACTION_LIMIT = 500;

/** Raised when the server answers with a non-2xx status. */
export class ApiError extends Error {
  readonly status: number;

  constructor(path: string, status: number) {
    super(`${path} answered HTTP ${status}`);
    this.name = "ApiError";
    this.status = status;
  }
}

async function getJSON<T>(path: string, params: Record<string, string>): Promise<T> {
  const url = new URL(path, window.location.origin);
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value);
  }

  const response = await fetch(url, { headers: { Accept: "application/json" } });
  if (!response.ok) {
    throw new ApiError(path, response.status);
  }
  return (await response.json()) as T;
}

/** GET /api/transactions — the ledger snapshot, newest first. */
export function fetchTransactions(
  shopId: string,
  limit: number = MAX_TRANSACTION_LIMIT,
): Promise<TransactionsSnapshot> {
  return getJSON<TransactionsSnapshot>("/api/transactions", {
    shop_id: shopId,
    limit: String(limit),
  });
}

/** GET /api/inventory — the whole catalogue for a shop. */
export function fetchInventory(shopId: string): Promise<InventorySnapshot> {
  return getJSON<InventorySnapshot>("/api/inventory", { shop_id: shopId });
}
