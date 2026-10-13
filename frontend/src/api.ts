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
  readonly receipt_id?: string | undefined;
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
  readonly archived_at?: string | undefined;
}

export interface TransactionQuery {
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
  readonly type?: string | undefined;
  readonly status?: string | undefined;
  readonly product_id?: string | undefined;
  readonly receipt_id?: string | undefined;
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
  readonly last_seq?: number | undefined;
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
  readonly last_seq?: number | undefined;
  readonly generated_at: string;
}

export interface DailySummary {
  readonly shop_id: string;
  readonly currency: string;
  readonly today_sales_amount: Money;
  readonly today_sales_count: number;
  readonly pending_reviews: number;
  readonly low_stock_count: number;
  readonly last_seq?: number | undefined;
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

async function getJSON<T>(path: string, params: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const url = new URL(path, window.location.origin);
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value);
  }

  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const response = await fetch(url, reqInit);
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
  signal?: AbortSignal,
): Promise<TransactionsSnapshot> {
  const params: Record<string, string> = {
    shop_id: shopId,
    limit: String(limit),
  };
  if (query?.offset !== undefined) params["offset"] = String(query.offset);
  if (query?.type !== undefined && query.type !== "") params["type"] = query.type;
  if (query?.status !== undefined && query.status !== "") params["status"] = query.status;
  if (query?.product_id !== undefined && query.product_id !== "") params["product_id"] = query.product_id;
  if (query?.receipt_id !== undefined && query.receipt_id !== "") params["receipt_id"] = query.receipt_id;
  if (query?.from !== undefined && query.from !== "") params["from"] = query.from;
  if (query?.to !== undefined && query.to !== "") params["to"] = query.to;
  return getJSON<TransactionsSnapshot>("/api/transactions", params, signal);
}

/** Constructs the URL for downloading a CSV ledger export with current filters. */
export function getExportTransactionsUrl(shopId: string, query?: TransactionQuery): string {
  const params = new URLSearchParams({ shop_id: shopId, format: "csv" });
  if (query?.type !== undefined && query.type !== "") params.set("type", query.type);
  if (query?.status !== undefined && query.status !== "") params.set("status", query.status);
  if (query?.product_id !== undefined && query.product_id !== "") params.set("product_id", query.product_id);
  if (query?.receipt_id !== undefined && query.receipt_id !== "") params.set("receipt_id", query.receipt_id);
  if (query?.from !== undefined && query.from !== "") params.set("from", query.from);
  if (query?.to !== undefined && query.to !== "") params.set("to", query.to);
  return `/api/transactions/export?${params.toString()}`;
}

/** GET /api/inventory — the whole catalogue for a shop. */
export function fetchInventory(shopId: string, signal?: AbortSignal): Promise<InventorySnapshot> {
  return getJSON<InventorySnapshot>("/api/inventory", { shop_id: shopId }, signal);
}

/** GET /api/summary — authoritative daily aggregates for the shop. */
export function fetchSummary(shopId: string, signal?: AbortSignal): Promise<DailySummary> {
  return getJSON<DailySummary>("/api/summary", { shop_id: shopId }, signal);
}

export interface ReviewCorrections {
  readonly product_id?: string | undefined;
  readonly quantity?: number | undefined;
  readonly unit?: Unit | undefined;
  readonly amount?: Money | undefined;
  readonly notes?: string | undefined;
}

export type ReviewDecisionType = "confirmed" | "corrected_and_confirmed" | "rejected";

export interface ReviewValuesSnapshot {
  readonly product_id?: string;
  readonly product_name?: string;
  readonly quantity?: number;
  readonly unit?: Unit;
  readonly amount?: Money;
}

export interface ReviewDecision {
  readonly id: string;
  readonly shop_id: string;
  readonly transaction_id: string;
  readonly decision: ReviewDecisionType;
  readonly decided_by: string;
  readonly decided_at: string;
  readonly original_values: ReviewValuesSnapshot;
  readonly corrected_values?: ReviewValuesSnapshot;
  readonly notes?: string;
}

/** POST /api/transactions/{id}/confirm — confirm a held review transaction and apply stock. */
export function confirmTransaction(
  id: string,
  corrections?: ReviewCorrections,
): Promise<Transaction> {
  return postJSON<Transaction>(`/api/transactions/${encodeURIComponent(id)}/confirm`, corrections || {});
}

/** POST /api/transactions/{id}/reject — discard a held review transaction. */
export function rejectTransaction(id: string, notes?: string): Promise<Transaction> {
  return postJSON<Transaction>(`/api/transactions/${encodeURIComponent(id)}/reject`, { notes: notes || "" });
}

/** GET /api/transactions/{id}/decision — fetch review adjudication record. */
export async function fetchTransactionDecision(id: string): Promise<ReviewDecision> {
  const res = await fetch(`/api/transactions/${encodeURIComponent(id)}/decision`, {
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    throw new ApiError(`/api/transactions/${id}/decision`, res.status);
  }
  return res.json() as Promise<ReviewDecision>;
}

export interface ReceiptGroup {
  readonly receipt_id: string;
  readonly shop_id: string;
  readonly transactions: readonly Transaction[];
  readonly total_count: number;
  readonly confirmed_count: number;
  readonly review_count: number;
  readonly rejected_count: number;
  readonly total_amount: Money;
}

/** GET /api/receipts/{receipt_id} */
export async function fetchReceipt(receiptId: string): Promise<ReceiptGroup> {
  const res = await fetch(`/api/receipts/${encodeURIComponent(receiptId)}`, {
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    throw new ApiError(`/api/receipts/${receiptId}`, res.status);
  }
  return res.json() as Promise<ReceiptGroup>;
}

/** POST /api/receipts/{receipt_id}/confirm */
export function confirmReceipt(receiptId: string, notes?: string): Promise<ReceiptGroup> {
  return postJSON<ReceiptGroup>(`/api/receipts/${encodeURIComponent(receiptId)}/confirm`, { notes: notes || "" });
}

/** POST /api/receipts/{receipt_id}/reject */
export function rejectReceipt(receiptId: string, notes?: string): Promise<ReceiptGroup> {
  return postJSON<ReceiptGroup>(`/api/receipts/${encodeURIComponent(receiptId)}/reject`, { notes: notes || "" });
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
export function fetchSettings(shopId: string, signal?: AbortSignal): Promise<SettingsResponse> {
  return getJSON<SettingsResponse>("/api/settings", { shop_id: shopId }, signal);
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

export interface ManualTransactionInput {
  type: TransactionType;
  product_id: string;
  quantity: number;
  unit: Unit;
  amount?: Money | undefined;
  notes?: string | undefined;
}

/** POST /api/transactions/manual — record a manual sale or restock fallback. */
export async function createManualTransaction(
  input: ManualTransactionInput,
): Promise<{ transaction: Transaction; product?: Product }> {
  const res = await fetch("/api/transactions/manual", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: JSON.stringify(input),
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
  return res.json() as Promise<{ transaction: Transaction; product?: Product }>;
}

/** POST /api/transactions/{id}/reverse — void an errant confirmed transaction. */
export async function reverseTransaction(
  transactionId: string,
  reason?: string,
): Promise<{ transaction: Transaction; reversal: Transaction; product?: Product }> {
  const res = await fetch(`/api/transactions/${encodeURIComponent(transactionId)}/reverse`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: JSON.stringify({ reason }),
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
  return res.json() as Promise<{ transaction: Transaction; reversal: Transaction; product?: Product }>;
}

export interface ProductMovement {
  readonly transaction_id: string;
  readonly shop_id: string;
  readonly product_id: string;
  readonly product_name: string;
  readonly type: TransactionType;
  readonly status: TransactionStatus;
  readonly quantity: number;
  readonly unit: Unit;
  readonly delta: number;
  readonly created_at: string;
  readonly transcript?: string;
}

export interface ProductMovementsResult {
  readonly shop_id: string;
  readonly product_id: string;
  readonly movements: readonly ProductMovement[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

/** GET /api/products/{id}/movements — query stock movement history. */
export async function fetchProductMovements(
  productId: string,
  limit = 50,
  offset = 0,
  signal?: AbortSignal,
): Promise<ProductMovementsResult> {
  const url = new URL(`/api/products/${encodeURIComponent(productId)}/movements`, window.location.origin);
  url.searchParams.set("limit", String(limit));
  url.searchParams.set("offset", String(offset));

  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch(url.toString(), reqInit);
  if (!res.ok) {
    throw new ApiError(url.pathname, res.status);
  }
  return res.json() as Promise<ProductMovementsResult>;
}

/** POST /api/products/{id}/adjust — physical stock count correction with an audit trail. */
export async function adjustProductStock(
  productId: string,
  quantity: number,
  reason: string,
): Promise<{ product: Product; transaction: Transaction }> {
  const res = await fetch(`/api/products/${encodeURIComponent(productId)}/adjust`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: JSON.stringify({ quantity, reason }),
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
  return res.json() as Promise<{ product: Product; transaction: Transaction }>;
}

/** POST /api/products/{id}/archive — safely soft-archive product from active speech matching. */
export function archiveProduct(id: string): Promise<Product> {
  return postJSON<Product>(`/api/products/${encodeURIComponent(id)}/archive`);
}

/** POST /api/products/{id}/unarchive — restore product to active speech recognition. */
export function unarchiveProduct(id: string): Promise<Product> {
  return postJSON<Product>(`/api/products/${encodeURIComponent(id)}/unarchive`);
}

export interface RotateDeviceResult {
  readonly id: string;
  readonly shop_id: string;
  readonly name: string;
  readonly token: string;
  readonly rotated_at: string;
}

/** POST /api/devices/{id}/rotate — rotate paired device credential. */
export function rotateDeviceToken(deviceId: string): Promise<RotateDeviceResult> {
  return postJSON<RotateDeviceResult>(`/api/devices/${encodeURIComponent(deviceId)}/rotate`);
}

export interface OnboardingStep {
  readonly completed: boolean;
  readonly title: string;
  readonly description: string;
  readonly count?: number;
}

export interface OnboardingSteps {
  readonly profile: OnboardingStep;
  readonly catalogue: OnboardingStep;
  readonly devices: OnboardingStep;
  readonly test_capture: OnboardingStep;
}

export interface OnboardingStatus {
  readonly shop_id: string;
  readonly steps: OnboardingSteps;
  readonly completed_steps: number;
  readonly total_steps: number;
  readonly is_complete: boolean;
}

export interface TestCaptureRequest {
  readonly transcript: string;
  readonly type?: "sale" | "restock";
}

export interface TestCaptureLine {
  readonly product_name: string;
  readonly matched_by: string;
  readonly quantity: number;
  readonly unit: Unit;
  readonly amount: Money;
  readonly confidence: Confidence;
  readonly status: TransactionStatus;
  readonly review_reasons: readonly string[];
}

export interface TestCaptureResponse {
  readonly status: string;
  readonly recognized: boolean;
  readonly type: TransactionType;
  readonly closing_phrase: string;
  readonly boundary_detected: boolean;
  readonly lines: readonly TestCaptureLine[];
  readonly summary: string;
  readonly transaction_id?: string;
}

/** GET /api/onboarding/status — fetch shop setup checklist progress. */
export async function fetchOnboardingStatus(signal?: AbortSignal): Promise<OnboardingStatus> {
  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch("/api/onboarding/status", reqInit);
  if (!res.ok) {
    throw new ApiError("/api/onboarding/status", res.status);
  }
  return res.json() as Promise<OnboardingStatus>;
}

/** GET /api/devices — query paired edge capture devices. */
export async function fetchDevices(signal?: AbortSignal): Promise<{ devices: readonly Device[] }> {
  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch("/api/devices", reqInit);
  if (!res.ok) {
    throw new ApiError("/api/devices", res.status);
  }
  return res.json() as Promise<{ devices: readonly Device[] }>;
}

/** POST /api/onboarding/test-capture — interactive test capture verification. */
export async function runTestCapture(body: TestCaptureRequest): Promise<TestCaptureResponse> {
  return postJSON<TestCaptureResponse>("/api/onboarding/test-capture", body);
}




