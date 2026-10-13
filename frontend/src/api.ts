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

export interface TransactionQuery {
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
  readonly type?: string | undefined;
  readonly status?: string | undefined;
  readonly product_id?: string | undefined;
  readonly from?: string | undefined;
  readonly to?: string | undefined;
}

export interface TransactionsSnapshot {
  readonly shop_id: string;
  readonly transactions: readonly Transaction[];
  readonly count: number;
  readonly total?: number | undefined;
  readonly limit: number;
  readonly offset?: number | undefined;
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

export interface DailySummary {
  readonly shop_id: string;
  readonly currency: string;
  readonly today_sales_amount: Money;
  readonly today_sales_count: number;
  readonly pending_reviews: number;
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
  readonly serverMessage?: string | undefined;

  constructor(path: string, status: number, serverMessage?: string) {
    super(serverMessage ? `${path} answered HTTP ${status}: ${serverMessage}` : `${path} answered HTTP ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.serverMessage = serverMessage;
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

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Accept": "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: body !== undefined ? JSON.stringify(body) : JSON.stringify({}),
  });
  if (!response.ok) {
    let msg = "";
    try {
      const err = await response.json();
      msg = err.error || err.message;
    } catch {
      // ignore non-json errors
    }
    throw new ApiError(path, response.status, msg || undefined);
  }
  return (await response.json()) as T;
}

async function patchJSON<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "PATCH",
    headers: {
      "Content-Type": "application/json",
      "Accept": "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    let msg = "";
    try {
      const err = await response.json();
      msg = err.error || err.message;
    } catch {
      // ignore non-json errors
    }
    throw new ApiError(path, response.status, msg || undefined);
  }
  return (await response.json()) as T;
}

/** GET /api/transactions — the ledger snapshot, newest first. */
export function fetchTransactions(
  shopId: string,
  limit: number = MAX_TRANSACTION_LIMIT,
  query?: TransactionQuery,
): Promise<TransactionsSnapshot> {
  const params: Record<string, string> = {
    shop_id: shopId,
    limit: String(limit),
  };
  if (query?.offset !== undefined) params["offset"] = String(query.offset);
  if (query?.type !== undefined && query.type !== "") params["type"] = query.type;
  if (query?.status !== undefined && query.status !== "") params["status"] = query.status;
  if (query?.product_id !== undefined && query.product_id !== "") params["product_id"] = query.product_id;
  if (query?.from !== undefined && query.from !== "") params["from"] = query.from;
  if (query?.to !== undefined && query.to !== "") params["to"] = query.to;
  return getJSON<TransactionsSnapshot>("/api/transactions", params);
}

/** Constructs the URL for downloading a CSV ledger export with current filters. */
export function getExportTransactionsUrl(shopId: string, query?: TransactionQuery): string {
  const params = new URLSearchParams({ shop_id: shopId, format: "csv" });
  if (query?.type !== undefined && query.type !== "") params.set("type", query.type);
  if (query?.status !== undefined && query.status !== "") params.set("status", query.status);
  if (query?.product_id !== undefined && query.product_id !== "") params.set("product_id", query.product_id);
  if (query?.from !== undefined && query.from !== "") params.set("from", query.from);
  if (query?.to !== undefined && query.to !== "") params.set("to", query.to);
  return `/api/transactions/export?${params.toString()}`;
}

/** GET /api/inventory — the whole catalogue for a shop. */
export function fetchInventory(shopId: string): Promise<InventorySnapshot> {
  return getJSON<InventorySnapshot>("/api/inventory", { shop_id: shopId });
}

/** GET /api/summary — authoritative daily aggregates for the shop. */
export function fetchSummary(shopId: string): Promise<DailySummary> {
  return getJSON<DailySummary>("/api/summary", { shop_id: shopId });
}

export interface ReviewCorrections {
  product_id?: string;
  quantity?: number;
  unit?: Unit;
  amount?: Money;
}

/** POST /api/transactions/{id}/confirm — confirm a held review transaction and apply stock. */
export function confirmTransaction(
  id: string,
  corrections?: ReviewCorrections,
): Promise<Transaction> {
  return postJSON<Transaction>(`/api/transactions/${encodeURIComponent(id)}/confirm`, corrections);
}

/** POST /api/transactions/{id}/reject — discard a held review transaction. */
export function rejectTransaction(id: string): Promise<Transaction> {
  return postJSON<Transaction>(`/api/transactions/${encodeURIComponent(id)}/reject`);
}

export interface CreateProductInput {
  name: string;
  sku: string;
  aliases: string[];
  price: Money;
  quantity: number;
  base_unit: Unit;
  packaging?: PackagingRelation[];
  low_stock_threshold?: number;
}

/** POST /api/products — add a new product to the catalogue. */
export function createProduct(input: CreateProductInput): Promise<Product> {
  return postJSON<Product>("/api/products", input);
}

export interface UpdateProductInput {
  name?: string;
  sku?: string;
  aliases?: string[];
  price?: Money;
  base_unit?: Unit;
  packaging?: PackagingRelation[];
  low_stock_threshold?: number;
}

/** PATCH /api/products/{id} — update an existing catalogue entry. */
export function updateProduct(id: string, input: UpdateProductInput): Promise<Product> {
  return patchJSON<Product>(`/api/products/${encodeURIComponent(id)}`, input);
}

export interface Device {
  readonly id: string;
  readonly shop_id: string;
  readonly name: string;
  readonly paired_at: string;
  readonly revoked_at?: string | null | undefined;
}

export interface ShopSettings {
  readonly id: string;
  readonly owner_id: string;
  readonly name: string;
  readonly currency: string;
  readonly timezone: string;
  readonly confidence_threshold: number;
  readonly sale_phrase: string;
  readonly restock_phrase: string;
}

export interface SettingsResponse {
  readonly shop: ShopSettings;
  readonly devices: readonly Device[];
}

export interface UpdateSettingsInput {
  readonly name?: string | undefined;
  readonly currency?: string | undefined;
  readonly timezone?: string | undefined;
  readonly confidence_threshold?: number | undefined;
  readonly sale_phrase?: string | undefined;
  readonly restock_phrase?: string | undefined;
}

/** GET /api/settings — shop configuration and paired devices. */
export function fetchSettings(shopId: string): Promise<SettingsResponse> {
  return getJSON<SettingsResponse>("/api/settings", { shop_id: shopId });
}

/** PATCH /api/settings — update shop configuration and thresholds. */
export function updateSettings(input: UpdateSettingsInput): Promise<ShopSettings> {
  return patchJSON<ShopSettings>("/api/settings", input);
}

export interface PairDeviceResult {
  readonly device: Device;
  readonly token: string;
}

/** POST /api/devices — register a new edge capture device. */
export function pairDevice(name: string): Promise<PairDeviceResult> {
  return postJSON<PairDeviceResult>("/api/devices", { name });
}

/** POST /api/devices/{id}/revoke — revoke an edge capture device credential. */
export function revokeDevice(id: string): Promise<{ success: boolean }> {
  return postJSON<{ success: boolean }>(`/api/devices/${encodeURIComponent(id)}/revoke`);
}

export interface ImportCatalogueResult {
  readonly total: number;
  readonly imported: number;
  readonly updated: number;
  readonly failed: number;
  readonly errors: readonly string[];
}

/** URL for downloading CSV catalogue template. */
export function getCatalogueTemplateUrl(): string {
  return "/api/products/template";
}

/** POST /api/products/import — upload CSV or JSON catalogue. */
export async function importCatalogue(
  content: string,
  contentType: "text/csv" | "application/json" = "text/csv"
): Promise<ImportCatalogueResult> {
  const res = await fetch("/api/products/import", {
    method: "POST",
    headers: {
      "Content-Type": contentType,
      "X-ListenLedger-Request": "1",
    },
    body: content,
  });
  if (!res.ok) {
    const text = await res.text();
    let msg = `HTTP ${res.status}`;
    try {
      const parsed = JSON.parse(text);
      if (parsed.error) msg = parsed.error;
    } catch {
      if (text) msg = text;
    }
    throw new Error(msg);
  }
  return res.json() as Promise<ImportCatalogueResult>;
}


