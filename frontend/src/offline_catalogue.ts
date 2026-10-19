/**
 * Offline Catalogue Caching & Storage Module.
 *
 * In busy Nigerian open-air market stalls, cellular network dropouts occur frequently.
 * To enable manual counter entry (the offline fallback for spoken captures)
 * when connectivity is absent, the product catalogue for the active authenticated
 * shop is persisted in durable browser storage (IndexedDB, localStorage, and in-memory).
 *
 * This allows cashiers to open the manual entry form, select products, and record
 * movements even when completely disconnected from the network.
 */

import type { Product } from "./api.js";

export interface CachedCatalogueRecord {
  readonly shop_id: string;
  readonly cached_at: string;
  readonly products: readonly Product[];
}

export type CatalogueStorageDriver = "indexeddb" | "localstorage" | "memory";

const DB_NAME = "listenledger_catalogue_db";
const STORE_NAME = "catalogues";
const DB_VERSION = 1;
const LOCAL_STORAGE_PREFIX = "listenledger_catalogue_";

// In-memory fallback
const memoryCatalogueStore = new Map<string, CachedCatalogueRecord>();

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
        db.createObjectStore(STORE_NAME, { keyPath: "shop_id" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("Failed to open catalogue IndexedDB"));
  });
}

function isLocalStorageSupported(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage !== null;
  } catch {
    return false;
  }
}

function readLocalStorageRecord(shopId: string): CachedCatalogueRecord | null {
  try {
    const raw = localStorage.getItem(`${LOCAL_STORAGE_PREFIX}${shopId}`);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && Array.isArray(parsed.products)) {
      return parsed as CachedCatalogueRecord;
    }
    return null;
  } catch {
    return null;
  }
}

function writeLocalStorageRecord(record: CachedCatalogueRecord): void {
  try {
    localStorage.setItem(`${LOCAL_STORAGE_PREFIX}${record.shop_id}`, JSON.stringify(record));
  } catch {
    // quota exceeded or storage blocked
  }
}

function removeLocalStorageRecord(shopId: string): void {
  try {
    localStorage.removeItem(`${LOCAL_STORAGE_PREFIX}${shopId}`);
  } catch {
    // blocked
  }
}

/**
 * Persists the product catalogue for the given shop into durable client storage.
 */
export async function setCachedCatalogue(
  shopId: string,
  products: readonly Product[],
): Promise<CachedCatalogueRecord> {
  const record: CachedCatalogueRecord = {
    shop_id: shopId,
    cached_at: new Date().toISOString(),
    products: [...products],
  };

  // Always sync into memory store
  memoryCatalogueStore.set(shopId, record);

  // Sync to localStorage
  if (isLocalStorageSupported()) {
    writeLocalStorageRecord(record);
  }

  // Sync to IndexedDB if supported
  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const req = store.put(record);
        req.onsuccess = () => resolve();
        req.onerror = () => reject(req.error);
      });
    } catch {
      // Fallback already written to localStorage and memory
    }
  }

  return record;
}

/**
 * Retrieves the full cached catalogue record for a shop, including timestamp.
 */
export async function getCachedCatalogueRecord(
  shopId: string,
): Promise<CachedCatalogueRecord | null> {
  if (isIndexedDBSupported()) {
    try {
      const db = await openDB();
      const record = await new Promise<CachedCatalogueRecord | undefined>((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const store = tx.objectStore(STORE_NAME);
        const req = store.get(shopId);
        req.onsuccess = () => resolve(req.result as CachedCatalogueRecord | undefined);
        req.onerror = () => reject(req.error);
      });
      if (record && Array.isArray(record.products)) {
        return record;
      }
    } catch {
      // Fallback
    }
  }

  if (isLocalStorageSupported()) {
    const record = readLocalStorageRecord(shopId);
    if (record) return record;
  }

  return memoryCatalogueStore.get(shopId) ?? null;
}

/**
 * Retrieves cached products for the specified shop.
 * Returns an empty array if no cached catalogue is available for this shop.
 */
export async function getCachedCatalogue(shopId: string): Promise<Product[]> {
  const record = await getCachedCatalogueRecord(shopId);
  return record ? [...record.products] : [];
}

/**
 * Clears cached catalogue records from durable storage.
 * If shopId is supplied, only that shop's cache is purged.
 * If omitted, all cached catalogues are purged.
 */
export async function clearCachedCatalogue(shopId?: string): Promise<void> {
  if (shopId) {
    memoryCatalogueStore.delete(shopId);
    if (isLocalStorageSupported()) {
      removeLocalStorageRecord(shopId);
    }
    if (isIndexedDBSupported()) {
      try {
        const db = await openDB();
        await new Promise<void>((resolve, reject) => {
          const tx = db.transaction(STORE_NAME, "readwrite");
          const store = tx.objectStore(STORE_NAME);
          const req = store.delete(shopId);
          req.onsuccess = () => resolve();
          req.onerror = () => reject(req.error);
        });
      } catch {
        // Fallback
      }
    }
    return;
  }

  memoryCatalogueStore.clear();

  if (isLocalStorageSupported()) {
    try {
      const keysToRemove: string[] = [];
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key && key.startsWith(LOCAL_STORAGE_PREFIX)) {
          keysToRemove.push(key);
        }
      }
      for (const k of keysToRemove) {
        localStorage.removeItem(k);
      }
    } catch {
      // blocked
    }
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
    } catch {
      // Fallback
    }
  }
}

/**
 * Detects the active driver serving catalogue caching.
 */
export function getActiveCatalogueDriver(): CatalogueStorageDriver {
  if (isIndexedDBSupported()) return "indexeddb";
  if (isLocalStorageSupported()) return "localstorage";
  return "memory";
}
