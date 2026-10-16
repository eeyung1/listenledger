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

import { postCapture, type CaptureResult, type TransactionType } from "./api.js";

export interface QueuedSpokenCapture {
  readonly queue_id: string;
  readonly capture_id?: string | undefined;
  readonly shop_id?: string | undefined;
  readonly transcript: string;
  readonly type: TransactionType | "cancelled";
  readonly closing_phrase: string;
  readonly queued_at: string;
  retry_count: number;
  last_error?: string | undefined;
  status: "queued" | "syncing";
}

export type QueueStorageDriver = "indexeddb" | "localstorage" | "memory";

const DB_NAME = "listenledger_offline_db";
const STORE_NAME = "queued_captures";
const DB_VERSION = 1;
const LOCAL_STORAGE_KEY = "listenledger_offline_queue";

// In-memory fallback if both IndexedDB and localStorage are blocked
const memoryStore = new Map<string, QueuedSpokenCapture>();

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
  if (input.shop_id) {
    (item as { shop_id?: string }).shop_id = input.shop_id;
  }

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
 * Retrieves all currently queued captures ordered oldest to newest.
 */
export async function getQueuedCaptures(): Promise<QueuedSpokenCapture[]> {
  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      return await new Promise<QueuedSpokenCapture[]>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const store = tx.objectStore(STORE_NAME);
        const req = store.getAll();
        req.onsuccess = () => {
          const res = (req.result || []) as QueuedSpokenCapture[];
          // Sort FIFO by queued_at
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
 * Returns the count of pending captures currently queued offline.
 */
export async function getQueuedCount(): Promise<number> {
  const items = await getQueuedCaptures();
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
  const items = await getQueuedCaptures();
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
  const items = await getQueuedCaptures();
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
 * Clears all queued captures from offline storage.
 */
export async function clearQueue(): Promise<void> {
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

/**
 * Iterates through all queued spoken captures and replays them into /api/captures.
 * Successfully ingested captures are removed from the queue immediately.
 */
export async function syncOfflineQueue(
  onProgress?: SyncProgressCallback,
): Promise<{ synced: number; failed: number }> {
  if (isSyncing) {
    return { synced: 0, failed: 0 };
  }
  if (!isOnline()) {
    return { synced: 0, failed: 0 };
  }

  isSyncing = true;
  let synced = 0;
  let failed = 0;

  try {
    const items = await getQueuedCaptures();
    for (const item of items) {
      if (!isOnline()) {
        break;
      }

      try {
        await markCaptureSyncing(item.queue_id);
        const result = await postCapture({
          capture_id: item.queue_id,
          transcript: item.transcript,
          type: item.type,
          closing_phrase: item.closing_phrase,
          occurred_at: item.queued_at,
        });

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

  return { synced, failed };
}
