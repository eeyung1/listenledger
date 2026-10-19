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

export type Unit =
  | "piece"
  | "pack"
  | "carton"
  | "bottle"
  | "crate"
  | "tin"
  | "bag"
  | "sachet"
  | "roll"
  | "painter"
  | "mudu"
  | "derica"
  | "rubber";

export const ALL_UNITS: readonly Unit[] = [
  "piece",
  "pack",
  "carton",
  "bottle",
  "crate",
  "tin",
  "bag",
  "sachet",
  "roll",
  "painter",
  "mudu",
  "derica",
  "rubber",
];

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
  readonly supplier_name?: string | undefined;
  readonly customer_id?: string | undefined;
  readonly customer_name?: string | undefined;
  readonly audio_data?: string | undefined;
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
  readonly customer_id?: string | undefined;
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
  readonly total?: number | undefined;
  readonly low_stock_count: number;
  readonly last_seq?: number | undefined;
  readonly generated_at: string;
}

export interface DailySummary {
  readonly shop_id: string;
  readonly currency: string;
  readonly today_sales_amount: Money;
  readonly today_sales_count: number;
  readonly today_expenses_amount?: Money | undefined;
  readonly today_expenses_count?: number | undefined;
  readonly today_net_profit?: Money | undefined;
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

async function getJSON<T>(path: string, params?: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const url = new URL(path, window.location.origin);
  if (params) {
    for (const [key, value] of Object.entries(params)) {
      url.searchParams.set(key, value);
    }
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

async function postJSON<T>(path: string, body?: unknown, extraHeaders?: Record<string, string>): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Accept": "application/json",
      "X-ListenLedger-Request": "1",
      ...extraHeaders,
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

async function putJSON<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "PUT",
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

export interface InventoryQuery {
  readonly q?: string | undefined;
  readonly status?: "active" | "archived" | "all" | undefined;
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
}

/** GET /api/inventory — query the catalogue for a shop with optional search, status filtering, and pagination. */
export function fetchInventory(
  shopId: string,
  queryOrSignal?: InventoryQuery | AbortSignal,
  maybeSignal?: AbortSignal,
): Promise<InventorySnapshot> {
  let query: InventoryQuery | undefined;
  let signal: AbortSignal | undefined;
  if (queryOrSignal instanceof AbortSignal) {
    signal = queryOrSignal;
  } else {
    query = queryOrSignal;
    signal = maybeSignal;
  }

  const params: Record<string, string> = { shop_id: shopId };
  if (query?.q !== undefined && query.q !== "") params["q"] = query.q;
  if (query?.status !== undefined && query.status !== "all") params["status"] = query.status;
  if (query?.limit !== undefined && query.limit > 0) params["limit"] = String(query.limit);
  if (query?.offset !== undefined && query.offset >= 0) params["offset"] = String(query.offset);

  return getJSON<InventorySnapshot>("/api/inventory", params, signal);
}

/** GET /api/summary — authoritative daily aggregates for the shop. */
export function fetchSummary(shopId: string, signal?: AbortSignal): Promise<DailySummary> {
  return getJSON<DailySummary>("/api/summary", { shop_id: shopId }, signal);
}

export interface CreateProductOnboard {
  readonly name: string;
  readonly sku?: string | undefined;
  readonly aliases?: readonly string[] | undefined;
  readonly price: Money;
  readonly base_unit: Unit;
  readonly packaging?: readonly PackagingRelation[] | undefined;
  readonly low_stock_threshold?: number | undefined;
}

export interface ReviewCorrections {
  readonly product_id?: string | undefined;
  readonly quantity?: number | undefined;
  readonly unit?: Unit | undefined;
  readonly amount?: Money | undefined;
  readonly customer_id?: string | undefined;
  readonly customer_name?: string | undefined;
  readonly notes?: string | undefined;
  readonly create_product?: CreateProductOnboard | undefined;
}

export type ReviewDecisionType = "confirmed" | "corrected_and_confirmed" | "rejected";

export interface ReviewValuesSnapshot {
  readonly product_id?: string;
  readonly product_name?: string;
  readonly quantity?: number;
  readonly unit?: Unit;
  readonly amount?: Money;
  readonly customer_id?: string | undefined;
  readonly customer_name?: string | undefined;
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
  readonly recognition_language: string;
  readonly staff_trained_at?: string | null | undefined;
}

export interface TargetGates {
  readonly min_full_transaction_accuracy: number;
  readonly min_sku_accuracy: number;
  readonly min_quantity_accuracy: number;
  readonly min_unit_accuracy: number;
  readonly min_amount_accuracy: number;
  readonly max_false_capture_rate: number;
  readonly max_missed_transaction_rate: number;
  readonly max_correction_rate: number;
  readonly min_protocol_compliance_rate: number;
  readonly max_p95_latency_ms: number;
  readonly agreed_at?: string | undefined;
  readonly agreed_by?: string | undefined;
}

export interface TargetGatesResponse {
  readonly gates: TargetGates;
  readonly is_agreed: boolean;
}

/** GET /api/settings/target-gates — fetch agreed or proposed target release gates. */
export function fetchTargetGates(signal?: AbortSignal): Promise<TargetGatesResponse> {
  return getJSON<TargetGatesResponse>("/api/settings/target-gates", undefined, signal);
}

/** PUT /api/settings/target-gates — update and agree pilot target release gates. */
export async function updateTargetGates(body: Partial<TargetGates>): Promise<TargetGatesResponse> {
  const reqInit: RequestInit = {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      "X-ListenLedger-Request": "1",
    },
    body: JSON.stringify(body),
  };
  const res = await fetch("/api/settings/target-gates", reqInit);
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(`/api/settings/target-gates (${text || res.statusText})`, res.status);
  }
  return res.json() as Promise<TargetGatesResponse>;
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
  readonly recognition_language?: string | undefined;
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
  /**
   * Stable, browser-minted id for one manual entry (P4.8). Generated once
   * before the first POST and reused for every retry — including the offline
   * queue replay — so a request that committed but whose response was lost is
   * replayed instead of recorded twice.
   */
  request_id: string;
  type: TransactionType;
  product_id: string;
  quantity: number;
  unit: Unit;
  amount?: Money | undefined;
  notes?: string | undefined;
}

/**
 * Mints a manual request id in the browser (P4.8), mirroring `generateCaptureId`
 * for the spoken path. The value must be created once per entry and carried
 * through the queue; generating it at POST time would defeat the retry safety.
 */
export function generateManualRequestId(now: number = Date.now()): string {
  const cryptoObj = typeof crypto !== "undefined" ? crypto : undefined;
  if (typeof cryptoObj?.randomUUID === "function") {
    try {
      return `man_${cryptoObj.randomUUID()}`;
    } catch {
      // fall through to the deterministic fallback
    }
  }
  const rand = Math.random().toString(36).slice(2, 10);
  return `man_${now.toString(36)}_${rand}`;
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
      // Sent both in the body and as the P4.3-style header so either can serve
      // as the server-side idempotency key for a retry.
      "Idempotency-Key": input.request_id,
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
  readonly staff_training: OnboardingStep;
  readonly target_gates: OnboardingStep;
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

export interface DrillCheckItem {
  readonly name: string;
  readonly passed: boolean;
  readonly detail: string;
}

export interface DrillRequest {
  readonly scenario_type: "clean_sale" | "restock" | "non_commercial_chatter" | "spoken_correction" | "custom";
  readonly transcript: string;
}

export interface DrillResponse {
  readonly passed: boolean;
  readonly score: number;
  readonly scenario_type: string;
  readonly boundary_detected: boolean;
  readonly closing_phrase: string;
  readonly recognized_item: string;
  readonly checklist: readonly DrillCheckItem[];
  readonly coaching_tips: readonly string[];
  readonly staff_trained_at?: string | undefined;
}

/** POST /api/onboarding/drill — run interactive cashier training drill. */
export async function runTrainingDrill(body: DrillRequest): Promise<DrillResponse> {
  return postJSON<DrillResponse>("/api/onboarding/drill", body);
}

export interface IndependentRecordInput {
  readonly occurred_at: string;
  readonly sku_or_name: string;
  readonly type: "sale" | "restock";
  readonly quantity: number;
  readonly unit: string;
  readonly amount: number; // in minor units
}

export interface PhysicalStockCountInput {
  readonly sku_or_name: string;
  readonly physical_count: number;
}

export interface ReconcileRequest {
  readonly period_start: string;
  readonly period_end: string;
  readonly independent_source: string;
  readonly independent_records: readonly IndependentRecordInput[];
  readonly physical_stock_counts: readonly PhysicalStockCountInput[];
  readonly notes?: string | undefined;
  readonly conducted_by?: string | undefined;
}

export interface PilotDiscrepancy {
  readonly id: string;
  readonly reconciliation_id: string;
  readonly shop_id: string;
  readonly discrepancy_type: "missed_capture" | "false_capture" | "price_mismatch" | "quantity_mismatch" | "stock_variance";
  readonly product_id?: string | undefined;
  readonly product_name: string;
  readonly expected_quantity: number;
  readonly actual_quantity: number;
  readonly expected_amount: number;
  readonly actual_amount: number;
  readonly transaction_id?: string | undefined;
  readonly investigation_status: "unexplained" | "investigated" | "resolved";
  readonly root_cause: string;
  readonly investigation_notes: string;
  readonly investigated_at?: string | undefined;
  readonly investigated_by?: string | undefined;
}

export interface PilotReconciliation {
  readonly id: string;
  readonly shop_id: string;
  readonly status: "in_progress" | "completed" | "discrepant" | "reconciled";
  readonly period_start: string;
  readonly period_end: string;
  readonly independent_source: string;
  readonly total_independent_records: number;
  readonly total_captured_transactions: number;
  readonly matched_records: number;
  readonly missed_captures: number;
  readonly false_captures: number;
  readonly price_discrepancies: number;
  readonly quantity_discrepancies: number;
  readonly total_independent_revenue: number;
  readonly total_captured_revenue: number;
  readonly revenue_discrepancy: number;
  readonly stock_discrepancy_count: number;
  readonly notes: string;
  readonly conducted_by: string;
  readonly created_at: string;
  readonly discrepancies?: readonly PilotDiscrepancy[] | undefined;
}

export interface GateCheckResult {
  readonly metric: string;
  readonly target: string;
  readonly achieved: number;
  readonly passed: boolean;
}

export interface ReconcileResponse {
  readonly reconciliation: PilotReconciliation;
  readonly gate_results: readonly GateCheckResult[];
  readonly all_gates_passed: boolean;
}

export interface ShopCohort {
  readonly shop_id: string;
  readonly cohort_phase: "phase1_controlled" | "phase2_dual_shop" | "phase3_expanded_5" | "phase4_scaled_15";
  readonly stage_status: "active" | "qualified" | "blocked";
  readonly qualified_at?: string | undefined;
  readonly notes: string;
}

export interface ReadinessCheckItem {
  readonly requirement: string;
  readonly satisfied: boolean;
  readonly details: string;
}

export interface CohortReadinessResponse {
  readonly shop_id: string;
  readonly current_phase: string;
  readonly next_phase: string;
  readonly readiness_status: "qualified" | "blocked";
  readonly checks: readonly ReadinessCheckItem[];
  readonly all_checks_satisfied: boolean;
}

/** POST /api/pilot/reconcile — execute dual-entry reconciliation against independent records. */
export async function reconcilePilot(body: ReconcileRequest): Promise<ReconcileResponse> {
  return postJSON<ReconcileResponse>("/api/pilot/reconcile", body);
}

/** GET /api/pilot/reconciliations — list historical trial reconciliation sessions. */
export async function fetchPilotReconciliations(signal?: AbortSignal): Promise<{ reconciliations: readonly PilotReconciliation[] }> {
  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch("/api/pilot/reconciliations", reqInit);
  if (!res.ok) {
    throw new ApiError("/api/pilot/reconciliations", res.status);
  }
  return res.json() as Promise<{ reconciliations: readonly PilotReconciliation[] }>;
}

/** GET /api/pilot/reconciliations/{id} — get detailed reconciliation session with discrepancies. */
export async function fetchPilotReconciliationDetail(id: string, signal?: AbortSignal): Promise<PilotReconciliation> {
  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch(`/api/pilot/reconciliations/${id}`, reqInit);
  if (!res.ok) {
    throw new ApiError(`/api/pilot/reconciliations/${id}`, res.status);
  }
  return res.json() as Promise<PilotReconciliation>;
}

/** POST /api/pilot/discrepancies/{id}/investigate — record investigation root cause. */
export async function investigatePilotDiscrepancy(
  discrepancyId: string,
  body: { root_cause: string; investigation_notes: string; investigated_by?: string },
): Promise<void> {
  await postJSON<{ discrepancy_id: string }>(`/api/pilot/discrepancies/${discrepancyId}/investigate`, body);
}

/** GET /api/pilot/cohort — fetch current shop cohort expansion phase. */
export async function fetchPilotCohort(signal?: AbortSignal): Promise<ShopCohort> {
  const reqInit: RequestInit = { headers: { Accept: "application/json" } };
  if (signal !== undefined) reqInit.signal = signal;
  const res = await fetch("/api/pilot/cohort", reqInit);
  if (!res.ok) {
    throw new ApiError("/api/pilot/cohort", res.status);
  }
  return res.json() as Promise<ShopCohort>;
}

/** POST /api/pilot/cohort/evaluate-readiness — evaluate readiness gates to advance cohort stage. */
export async function evaluateCohortReadiness(promote: boolean = false): Promise<CohortReadinessResponse> {
  return postJSON<CohortReadinessResponse>("/api/pilot/cohort/evaluate-readiness", { promote });
}

export type ExpenseCategory =
  | "generator_fuel"
  | "store_rent"
  | "transport_logistics"
  | "packaging_supplies"
  | "staff_wages"
  | "utilities"
  | "repairs"
  | "other";

export interface Expense {
  readonly id: string;
  readonly shop_id: string;
  readonly category: ExpenseCategory;
  readonly amount: Money;
  readonly description: string;
  readonly receipt_ref?: string | undefined;
  readonly occurred_at: string;
  readonly created_by: string;
  readonly created_at: string;
}

export interface ExpenseQuery {
  readonly category?: ExpenseCategory | undefined;
  readonly from?: string | undefined;
  readonly to?: string | undefined;
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
}

export interface ExpensesSnapshot {
  readonly shop_id: string;
  readonly expenses: readonly Expense[];
  readonly count: number;
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
  readonly generated_at: string;
}

export interface CreateExpenseRequest {
  readonly category: ExpenseCategory;
  readonly amount: { readonly amount: number; readonly currency?: string };
  readonly description: string;
  readonly receipt_ref?: string | undefined;
  readonly occurred_at?: string | undefined;
}

/** GET /api/expenses — fetch filtered, paginated expense list */
export async function fetchExpenses(query?: ExpenseQuery, signal?: AbortSignal): Promise<ExpensesSnapshot> {
  const params: Record<string, string> = {};
  if (query?.category) params.category = query.category;
  if (query?.from) params.from = query.from;
  if (query?.to) params.to = query.to;
  if (query?.limit !== undefined) params.limit = String(query.limit);
  if (query?.offset !== undefined) params.offset = String(query.offset);

  return getJSON<ExpensesSnapshot>("/api/expenses", params, signal);
}

/** POST /api/expenses — record a new operating expense */
export async function createExpense(data: CreateExpenseRequest): Promise<Expense> {
  return postJSON<Expense>("/api/expenses", data);
}

/** DELETE /api/expenses/{id} — delete an operational expense record */
export async function deleteExpense(id: string): Promise<{ readonly deleted: boolean; readonly id: string }> {
  const res = await fetch(`/api/expenses/${id}`, {
    method: "DELETE",
    headers: {
      "Content-Type": "application/json",
      "X-ListenLedger-Request": "1",
    },
  });
  if (!res.ok) {
    throw new ApiError(`/api/expenses/${id}`, res.status);
  }
  return res.json() as Promise<{ readonly deleted: boolean; readonly id: string }>;
}

export type CustomerDebtType = "credit_sale" | "repayment" | "forgiveness";

export interface Customer {
  readonly id: string;
  readonly shop_id: string;
  readonly name: string;
  readonly phone: string;
  readonly notes: string;
  readonly outstanding_balance: Money;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface CustomerDebt {
  readonly id: string;
  readonly shop_id: string;
  readonly customer_id: string;
  readonly type: CustomerDebtType;
  readonly amount: Money;
  readonly balance_after: Money;
  readonly description: string;
  readonly receipt_ref?: string | undefined;
  readonly occurred_at: string;
  readonly created_by: string;
  readonly created_at: string;
}

export interface CustomerQuery {
  readonly search?: string | undefined;
  readonly has_debt?: boolean | undefined;
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
}

export interface CustomersSnapshot {
  readonly shop_id: string;
  readonly customers: readonly Customer[];
  readonly count: number;
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
  readonly generated_at: string;
}

export interface CustomerDebtsSnapshot {
  readonly shop_id: string;
  readonly customer_id: string;
  readonly debts: readonly CustomerDebt[];
  readonly count: number;
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
  readonly generated_at: string;
}

export interface ShopCreditSummary {
  readonly shop_id: string;
  readonly currency: string;
  readonly total_customers_with_debt: number;
  readonly total_outstanding_debt: Money;
  readonly today_credit_sales_amount: Money;
  readonly today_repayments_amount: Money;
}

export interface CreateCustomerRequest {
  readonly name: string;
  readonly phone?: string | undefined;
  readonly notes?: string | undefined;
  readonly initial_debt?: { readonly amount: number; readonly currency?: string } | undefined;
  readonly initial_debt_description?: string | undefined;
}

export interface UpdateCustomerRequest {
  readonly name: string;
  readonly phone?: string | undefined;
  readonly notes?: string | undefined;
}

export interface RecordCustomerDebtRequest {
  readonly type: CustomerDebtType;
  readonly amount: { readonly amount: number; readonly currency?: string };
  readonly description: string;
  readonly receipt_ref?: string | undefined;
  readonly occurred_at?: string | undefined;
}

export interface RecordCustomerDebtResponse {
  readonly debt: CustomerDebt;
  readonly customer: Customer;
}

/** GET /api/customers — fetch filtered, paginated customer list */
export async function fetchCustomers(query?: CustomerQuery, signal?: AbortSignal): Promise<CustomersSnapshot> {
  const params: Record<string, string> = {};
  if (query?.search) params.search = query.search;
  if (query?.has_debt !== undefined) params.has_debt = query.has_debt ? "true" : "false";
  if (query?.limit !== undefined) params.limit = String(query.limit);
  if (query?.offset !== undefined) params.offset = String(query.offset);

  return getJSON<CustomersSnapshot>("/api/customers", params, signal);
}

/** POST /api/customers — register a new customer with optional initial credit */
export async function createCustomer(data: CreateCustomerRequest): Promise<Customer> {
  return postJSON<Customer>("/api/customers", data);
}

/** GET /api/customers/{id} — fetch customer profile */
export async function fetchCustomer(id: string, signal?: AbortSignal): Promise<Customer> {
  return getJSON<Customer>(`/api/customers/${encodeURIComponent(id)}`, undefined, signal);
}

/** PUT /api/customers/{id} — update customer profile */
export async function updateCustomer(id: string, data: UpdateCustomerRequest): Promise<Customer> {
  return putJSON<Customer>(`/api/customers/${encodeURIComponent(id)}`, data);
}

/** GET /api/customers/{id}/debts — fetch customer credit & repayment ledger */
export async function fetchCustomerDebts(
  customerId: string,
  query?: { type?: CustomerDebtType; limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<CustomerDebtsSnapshot> {
  const params: Record<string, string> = {};
  if (query?.type) params.type = query.type;
  if (query?.limit !== undefined) params.limit = String(query.limit);
  if (query?.offset !== undefined) params.offset = String(query.offset);

  return getJSON<CustomerDebtsSnapshot>(`/api/customers/${encodeURIComponent(customerId)}/debts`, params, signal);
}

/** POST /api/customers/{id}/debts — record credit sale, repayment, or debt forgiveness */
export async function recordCustomerDebt(
  customerId: string,
  data: RecordCustomerDebtRequest,
): Promise<RecordCustomerDebtResponse> {
  return postJSON<RecordCustomerDebtResponse>(`/api/customers/${encodeURIComponent(customerId)}/debts`, data);
}

/** GET /api/customers/credit-summary — fetch shop credit & debt totals */
export async function fetchShopCreditSummary(signal?: AbortSignal): Promise<ShopCreditSummary> {
  return getJSON<ShopCreditSummary>("/api/customers/credit-summary", undefined, signal);
}

export interface CaptureLineInput {
  readonly product_id?: string;
  readonly product_name: string;
  readonly quantity: number;
  readonly unit: Unit;
  readonly amount?: Money;
  readonly confidence?: Confidence;
}

export interface CaptureInput {
  readonly capture_id?: string;
  readonly device_id?: string;
  readonly type?: TransactionType | "cancelled";
  readonly transcript: string;
  readonly closing_phrase?: string;
  readonly supplier_name?: string;
  readonly occurred_at?: string;
  readonly extracted_lines?: readonly CaptureLineInput[];
  readonly audio_data?: string | undefined;
}

export interface CaptureResult {
  readonly capture_id: string;
  readonly receipt_id?: string;
  readonly shop_id: string;
  readonly type: TransactionType;
  readonly transactions: readonly Transaction[];
  readonly requires_review: boolean;
}

/** POST /api/captures — edge and mobile counter capture intake endpoint. */
export async function postCapture(input: CaptureInput): Promise<CaptureResult> {
  // Strip snippets from stale callers and queued captures as well as live input.
  const { audio_data: _discardedAudio, ...textCapture } = input;
  // P4.3: replay safety. A capture id minted in the browser at detection time is
  // sent both as the body's capture_id and as the Idempotency-Key header, so a
  // retried POST resolves to the same server-side idempotency keys.
  return postJSON<CaptureResult>("/api/captures", textCapture, {
    ...(textCapture.capture_id ? { "Idempotency-Key": textCapture.capture_id } : {}),
  });
}


