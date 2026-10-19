/**
 * Offline Spoken Capture Queue & Resilient IndexedDB Sync Module.
 *
 * In bustling open-air Nigerian market stalls, cellular network connections
 * drop frequently due to antenna congestion or intermittent data.
 *
 * This module ensures zero data loss by automatically persisting spoken counter
 * captures to browser IndexedDB (with localStorage and in-memory fallbacks)
 * whenever the device is offline or when capture POSTs encounter network failure.
 * When connectivity returns, the queue automatically replays and syncs captures
 * sequentially into the backend ledger.
 */

import {
  postCapture,
  createManualTransaction,
  type CaptureResult,
  type ManualTransactionInput,
  type TransactionType,
} from "./api.js";

/** What kind of entry this queue item replays to. Legacy entries are spoken captures. */
export type QueuedEntryKind = "spoken-capture" | "manual-transaction";

export interface QueuedSpokenCapture {
  readonly queue_id: string;
  readonly kind?: QueuedEntryKind | undefined;
  readonly capture_id?: string | undefined;
  readonly shop_id?: string | undefined;
  readonly transcript: string;
  readonly type: TransactionType | "cancelled";
  readonly closing_phrase: string;
  readonly queued_at: string;
  readonly audio_data?: string | undefined;
  /** Present only when `kind` is "manual-transaction" (P4.8 offline fallback). */
  readonly manual_payload?: ManualTransactionInput | undefined;
  retry_count: number;
  last_error?: string | undefined;
  status: "queued" | "syncing" | "quarantined";
}

export type QueueStorageDriver = "indexeddb" | "localstorage" | "memory";

const DB_NAME = "listenledger_offline_db";
const STORE_NAME = "queued_captures";
const DB_VERSION = 1;
const LOCAL_STORAGE_KEY = "listenledger_offline_queue";

// In-memory fallback if both IndexedDB and localStorage are blocked
const memoryStore = new Map<string, QueuedSpokenCapture>();

/** Currently active authenticated shop ID bound to the client queue session. */
let activeQueueShopId: string | null = null;

/** Sets the active authenticated shop for the offline queue. */
export function setActiveQueueShop(shopId: string | null): void {
  activeQueueShopId = shopId;
}

/** Returns the active authenticated shop ID, if set. */
export function getActiveQueueShop(): string | null {
  return activeQueueShopId;
}

/** Returns whether the browser is currently online. */
export function isOnline(): boolean {
  if (typeof navigator === "undefined") return true;
  return typeof navigator.onLine === "boolean" ? navigator.onLine : true;
}

/** Determines if an error represents a network dropout or offline condition. */
export function isNetworkError(error: unknown): boolean {
  if (!isOnline()) return true;
  if (error instanceof TypeError) return true; // Browser fetch failure
  if (error instanceof Error) {
    const msg = error.message.toLowerCase();
    return (
      msg.includes("failed to fetch") ||
      msg.includes("networkerror") ||
      msg.includes("network error") ||
      msg.includes("offline") ||
      msg.includes("load failed") ||
      msg.includes("econnrefused") ||
      msg.includes("abort") ||
      msg.includes("timeout")
    );
  }
  return false;
}

/** Generates a unique queue entry identifier with millisecond timestamp. */
export function generateQueueId(): string {
  const ts = Date.now().toString(36);
  const rand = Math.random().toString(36).substring(2, 8);
  return `qcap_${ts}_${rand}`;
}

// ---------------------------------------------------------------------------
// IndexedDB Driver
// ---------------------------------------------------------------------------

function isIndexedDBSupported(): boolean {
  return typeof window !== "undefined" && typeof window.indexedDB !== "undefined";
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (!isIndexedDBSupported()) {
      return reject(new Error("IndexedDB is not supported in this environment"));
    }
    const request = window.indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: "queue_id" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("Failed to open IndexedDB"));
  });
}

// ---------------------------------------------------------------------------
// LocalStorage Fallback Driver
// ---------------------------------------------------------------------------

function isLocalStorageSupported(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage !== null;
  } catch {
    return false;
  }
}

function readLocalStorageQueue(): QueuedSpokenCapture[] {
  try {
    const raw = localStorage.getItem(LOCAL_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as QueuedSpokenCapture[]) : [];
  } catch {
    return [];
  }
}

function writeLocalStorageQueue(items: readonly QueuedSpokenCapture[]): void {
  try {
    localStorage.setItem(LOCAL_STORAGE_KEY, JSON.stringify(items));
  } catch {
    // quota exceeded or blocked
  }
}

// ---------------------------------------------------------------------------
// Queue Operations
// ---------------------------------------------------------------------------

/**
 * Enqueues a spoken capture into durable client storage.
 */
export async function enqueueCapture(
  input: Omit<QueuedSpokenCapture, "queue_id" | "queued_at" | "retry_count" | "status">,
): Promise<QueuedSpokenCapture> {
  const effectiveShopId = input.shop_id ?? activeQueueShopId ?? undefined;
  const item: QueuedSpokenCapture = {
    queue_id: generateQueueId(),
    transcript: input.transcript,
    type: input.type,
    closing_phrase: input.closing_phrase,
    queued_at: new Date().toISOString(),
    retry_count: 0,
    status: "queued",
  };
  if (input.capture_id) {
    (item as { capture_id?: string }).capture_id = input.capture_id;
  }
  if (effectiveShopId) {
    (item as { shop_id?: string }).shop_id = effectiveShopId;
  }
  // New queue entries never retain raw audio during the pilot.

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(item);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return item;
    } catch {
      // Fallback to localStorage on IndexedDB failure
    }
  }

  if (isLocalStorageSupported()) {
    const list = readLocalStorageQueue();
    list.push(item);
    writeLocalStorageQueue(list);
    return item;
  }

  memoryStore.set(item.queue_id, item);
  return item;
}

/**
 * Queues a manual counter entry (typed Ledger fallback) created while the
 * device is offline, so it replays into /api/transactions/manual when
 * connectivity returns. P4.8: manual entry must genuinely be an offline
 * fallback, not a form whose POST dies with a network error.
 */
export async function enqueueManualTransaction(
  shopId: string,
  input: ManualTransactionInput,
  productLabel?: string,
): Promise<QueuedSpokenCapture> {
  const item: QueuedSpokenCapture = {
    queue_id: generateQueueId(),
    kind: "manual-transaction",
    shop_id: shopId,
    transcript: `Manual ${input.type}: ${input.quantity} ${input.unit} ${productLabel ?? input.product_id}`,
    type: input.type,
    closing_phrase: "",
    manual_payload: { ...input },
    queued_at: new Date().toISOString(),
    retry_count: 0,
    status: "queued",
  };

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(item);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return item;
    } catch {
      // Fallback to localStorage on IndexedDB failure
    }
  }

  if (isLocalStorageSupported()) {
    const list = readLocalStorageQueue();
    list.push(item);
    writeLocalStorageQueue(list);
    return item;
  }

  memoryStore.set(item.queue_id, item);
  return item;
}

/**
 * Reads all entries directly from underlying storage without shop or quarantine filtering.
 */
export async function readAllRawCaptures(): Promise<QueuedSpokenCapture[]> {
  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      return await new Promise<QueuedSpokenCapture[]>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const store = tx.objectStore(STORE_NAME);
        const req = store.getAll();
        req.onsuccess = () => {
          const res = (req.result || []) as QueuedSpokenCapture[];
          res.sort((a, b) => a.queued_at.localeCompare(b.queued_at));
          resolve(res);
        };
        req.onerror = () => reject(req.error);
      });
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    const list = readLocalStorageQueue();
    list.sort((a, b) => a.queued_at.localeCompare(b.queued_at));
    return list;
  }

  const list = Array.from(memoryStore.values());
  list.sort((a, b) => a.queued_at.localeCompare(b.queued_at));
  return list;
}

/**
 * Retrieves queued captures ordered oldest to newest.
 * When a shopId is provided (or an active shop is set), only entries matching
 * that shop are returned. Quarantined entries are excluded from active reads.
 */
export async function getQueuedCaptures(shopId?: string): Promise<QueuedSpokenCapture[]> {
  const all = await readAllRawCaptures();
  const targetShop = shopId ?? (activeQueueShopId || undefined);
  if (targetShop !== undefined) {
    return all.filter((item) => item.shop_id === targetShop && item.status !== "quarantined");
  }
  return all.filter((item) => item.status !== "quarantined");
}

/**
 * Returns the count of pending captures currently queued offline for the given shop
 * (or the active authenticated shop).
 */
export async function getQueuedCount(shopId?: string): Promise<number> {
  const items = await getQueuedCaptures(shopId);
  return items.length;
}

/**
 * Removes a successfully synced capture from the offline queue.
 */
export async function removeQueuedCapture(queue_id: string): Promise<void> {
  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.delete(queue_id);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return;
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    const list = readLocalStorageQueue().filter((item) => item.queue_id !== queue_id);
    writeLocalStorageQueue(list);
    return;
  }

  memoryStore.delete(queue_id);
}

/**
 * Updates an item's status to syncing.
 */
export async function markCaptureSyncing(queue_id: string): Promise<void> {
  const items = await readAllRawCaptures();
  const target = items.find((i) => i.queue_id === queue_id);
  if (!target) return;
  target.status = "syncing";

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(target);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return;
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    writeLocalStorageQueue(items);
    return;
  }

  memoryStore.set(queue_id, target);
}

/**
 * Records a sync failure and increments the retry count.
 */
export async function markCaptureFailed(queue_id: string, errorMsg: string): Promise<void> {
  const items = await readAllRawCaptures();
  const target = items.find((i) => i.queue_id === queue_id);
  if (!target) return;
  target.status = "queued";
  target.retry_count++;
  target.last_error = errorMsg;

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(target);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return;
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    writeLocalStorageQueue(items);
    return;
  }

  memoryStore.set(queue_id, target);
}

/**
 * Quarantines an invalid or unscoped capture so it is never replayed into
 * whatever authenticated session happens to be active.
 */
export async function markCaptureQuarantined(queue_id: string, reason: string): Promise<void> {
  const items = await readAllRawCaptures();
  const target = items.find((i) => i.queue_id === queue_id);
  if (!target) return;
  target.status = "quarantined";
  target.last_error = reason;

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(target);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return;
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    writeLocalStorageQueue(items);
    return;
  }

  memoryStore.set(queue_id, target);
}

/**
 * Clears queued captures from offline storage.
 * When shopId is provided, only captures belonging to that shop are removed,
 * preserving entries belonging to other shops.
 * When shopId is omitted, all captures across all shops are removed.
 */
export async function clearQueue(shopId?: string): Promise<void> {
  if (shopId) {
    const all = await readAllRawCaptures();
    const toRemove = all.filter((item) => item.shop_id === shopId);
    for (const item of toRemove) {
      await removeQueuedCapture(item.queue_id);
    }
    return;
  }

  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.clear();
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
      return;
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    localStorage.removeItem(LOCAL_STORAGE_KEY);
    return;
  }

  memoryStore.clear();
}

/** Detects which active storage driver is currently serving the queue. */
export function getActiveStorageDriver(): QueueStorageDriver {
  if (isIndexedDBSupported()) return "indexeddb";
  if (isLocalStorageSupported()) return "localstorage";
  return "memory";
}

// ---------------------------------------------------------------------------
// Auto-Sync Engine
// ---------------------------------------------------------------------------

let isSyncing = false;

export interface SyncProgressCallback {
  (item: QueuedSpokenCapture, result?: CaptureResult, error?: Error): void;
}

export interface SyncOptions {
  readonly shopId?: string | undefined;
  readonly getActiveShopId?: (() => string | null) | undefined;
  readonly onProgress?: SyncProgressCallback | undefined;
}

export type SyncArg = string | SyncProgressCallback | SyncOptions;

export interface SyncStats {
  synced: number;
  failed: number;
  skipped: number;
  quarantined: number;
}

/**
 * Iterates through queued captures and replays them into the ledger.
 * - Bound to the active authenticated shop: only replays entries matching the active shop.
 * - Preserves entries belonging to other shops in storage without replaying or deleting them.
 * - Quarantines legacy unscoped entries (missing shop_id) so they cannot be posted into whatever session is active.
 * - Rechecks identity across in-flight sync so account switch or logout halts replay immediately.
 */
export async function syncOfflineQueue(
  arg1?: SyncArg,
  arg2?: SyncProgressCallback,
): Promise<SyncStats> {
  if (isSyncing) {
    return { synced: 0, failed: 0, skipped: 0, quarantined: 0 };
  }
  if (!isOnline()) {
    return { synced: 0, failed: 0, skipped: 0, quarantined: 0 };
  }

  let targetShopId: string | undefined;
  let getActiveShopId: (() => string | null) | undefined;
  let onProgress: SyncProgressCallback | undefined;

  if (typeof arg1 === "string") {
    targetShopId = arg1;
    onProgress = arg2;
  } else if (typeof arg1 === "function") {
    targetShopId = activeQueueShopId ?? undefined;
    onProgress = arg1;
  } else if (arg1 && typeof arg1 === "object") {
    targetShopId = arg1.shopId ?? (activeQueueShopId || undefined);
    getActiveShopId = arg1.getActiveShopId;
    onProgress = arg1.onProgress ?? arg2;
  } else {
    targetShopId = activeQueueShopId ?? undefined;
    onProgress = arg2;
  }

  isSyncing = true;
  let synced = 0;
  let failed = 0;
  let skipped = 0;
  let quarantined = 0;

  try {
    const items = await readAllRawCaptures();
    for (const item of items) {
      if (!isOnline()) {
        break;
      }

      // Recheck active identity in-flight to prevent replay if session changed or logged out
      if (targetShopId !== undefined) {
        const currentActive = getActiveShopId ? getActiveShopId() : activeQueueShopId;
        if (currentActive !== targetShopId) {
          // Account switched or user logged out mid-sync; abort immediately
          break;
        }
      }

      // Legacy unscoped entry: missing shop_id cannot be safely attributed to active session
      if (!item.shop_id) {
        await markCaptureQuarantined(
          item.queue_id,
          "Quarantined: legacy unscoped queue entry cannot be safely attributed to active shop",
        );
        quarantined++;
        continue;
      }

      // If a target shop is defined and this item belongs to a different shop, skip and preserve
      if (targetShopId !== undefined && item.shop_id !== targetShopId) {
        skipped++;
        continue;
      }

      // Ignore already quarantined items
      if (item.status === "quarantined") {
        continue;
      }

      try {
        await markCaptureSyncing(item.queue_id);
        let result: CaptureResult | undefined;
        if (item.kind === "manual-transaction" && item.manual_payload) {
          // P4.8: manual counter entries replay into the manual-transaction
          // endpoint, not /api/captures. The stored payload keeps the entry's
          // original request_id, so the server can replay a committed entry
          // whose response was lost rather than recording it twice.
          await createManualTransaction({ ...item.manual_payload });
        } else {
          result = await postCapture({
            capture_id: item.capture_id || item.queue_id,
            transcript: item.transcript,
            type: item.type,
            closing_phrase: item.closing_phrase,
            occurred_at: item.queued_at,
          });
        }

        await removeQueuedCapture(item.queue_id);
        synced++;
        onProgress?.(item, result, undefined);
      } catch (err: unknown) {
        failed++;
        const errorObj = err instanceof Error ? err : new Error(String(err));
        await markCaptureFailed(item.queue_id, errorObj.message);
        onProgress?.(item, undefined, errorObj);

        // If failure was network-related, abort subsequent attempts until connection returns
        if (isNetworkError(err)) {
          break;
        }
      }
    }
  } finally {
    isSyncing = false;
  }

  return { synced, failed, skipped, quarantined };
}

