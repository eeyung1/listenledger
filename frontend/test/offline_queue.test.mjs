import test from "node:test";
import assert from "node:assert/strict";
import { setupDOMEnvironment } from "./test_dom.mjs";
import {
  enqueueCapture,
  enqueueManualTransaction,
  getQueuedCaptures,
  getQueuedCount,
  removeQueuedCapture,
  markCaptureSyncing,
  markCaptureFailed,
  clearQueue,
  isOnline,
  isNetworkError,
  syncOfflineQueue,
  getActiveStorageDriver,
  generateQueueId,
} from "../dist/offline_queue.js";

test("Offline Spoken Capture Queue & Resilient Sync Engine Suite", async (t) => {
  setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.beforeEach(async () => {
    await clearQueue();
    globalThis.fetch = originalFetch;
    if (typeof navigator !== "undefined") {
      navigator.onLine = true;
    }
  });

  t.afterEach(async () => {
    await clearQueue();
    globalThis.fetch = originalFetch;
    if (typeof navigator !== "undefined") {
      navigator.onLine = true;
    }
  });

  await t.test("Pilot queue drops supplied audio while preserving the transcript", async () => {
    const input = { transcript: "Two Milo thank you", type: "sale", closing_phrase: "thank you", audio_data: "data:audio/webm;base64,private" };
    const item = await enqueueCapture(input);
    assert.equal(item.audio_data, undefined);
    const saved = await getQueuedCaptures();
    assert.equal(saved[0].audio_data, undefined);
    assert.equal(saved[0].transcript, input.transcript);
  });

  await t.test("1. Queue Id Generation & Online / Network Error Detection", () => {
    const id1 = generateQueueId();
    const id2 = generateQueueId();
    assert.ok(id1.startsWith("qcap_"));
    assert.notEqual(id1, id2);

    // Online check
    assert.equal(isOnline(), true);

    // Network error classification
    assert.equal(isNetworkError(new TypeError("Failed to fetch")), true);
    assert.equal(isNetworkError(new Error("NetworkError when attempting to fetch resource")), true);
    assert.equal(isNetworkError(new Error("The Internet connection appears to be offline.")), true);
    assert.equal(isNetworkError(new Error("ECONNREFUSED 127.0.0.1:8080")), true);
    assert.equal(isNetworkError(new Error("The user aborted a request.")), true);
    // Non-network error (e.g. validation error or 400 Bad Request)
    assert.equal(isNetworkError(new Error("Product not found in shop")), false);
  });

  await t.test("2. Enqueue, FIFO Ordering, Count, and Clear Operations", async () => {
    assert.equal(await getQueuedCount(), 0);

    const item1 = await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Two Milo four thousand naira thank you",
      type: "sale",
      closing_phrase: "thank you",
    });

    assert.ok(item1.queue_id.startsWith("qcap_"));
    assert.equal(item1.status, "queued");
    assert.equal(item1.retry_count, 0);
    assert.equal(await getQueuedCount(), 1);

    // Sleep briefly to ensure distinct millisecond timestamp
    await new Promise((r) => setTimeout(r, 10));

    const item2 = await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Three cartons of Peak Milk stock received",
      type: "restock",
      closing_phrase: "stock received",
    });

    assert.equal(await getQueuedCount(), 2);

    const list = await getQueuedCaptures();
    assert.equal(list.length, 2);
    // FIFO ordering: item1 before item2
    assert.equal(list[0].queue_id, item1.queue_id);
    assert.equal(list[0].transcript, "Two Milo four thousand naira thank you");
    assert.equal(list[1].queue_id, item2.queue_id);
    assert.equal(list[1].type, "restock");

    // Remove item1
    await removeQueuedCapture(item1.queue_id);
    assert.equal(await getQueuedCount(), 1);
    const remaining = await getQueuedCaptures();
    assert.equal(remaining[0].queue_id, item2.queue_id);

    // Clear queue
    await clearQueue();
    assert.equal(await getQueuedCount(), 0);
  });

  await t.test("3. Status Transitions: markCaptureSyncing and markCaptureFailed", async () => {
    const item = await enqueueCapture({
      shop_id: "shop_001",
      transcript: "One carton of Indomie thank you",
      type: "sale",
      closing_phrase: "thank you",
    });

    await markCaptureSyncing(item.queue_id);
    let current = (await getQueuedCaptures())[0];
    assert.equal(current.status, "syncing");

    await markCaptureFailed(item.queue_id, "Network timeout during upload");
    current = (await getQueuedCaptures())[0];
    assert.equal(current.status, "queued");
    assert.equal(current.retry_count, 1);
    assert.equal(current.last_error, "Network timeout during upload");
  });

  await t.test("4. syncOfflineQueue: Successfully replays captures and removes from queue", async () => {
    await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Two Milo four thousand naira thank you",
      type: "sale",
      closing_phrase: "thank you",
    });
    await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Three Peak Milk seven thousand naira thank you",
      type: "sale",
      closing_phrase: "thank you",
    });

    assert.equal(await getQueuedCount(), 2);

    const replayedPayloads = [];
    globalThis.fetch = async (url, init = {}) => {
      replayedPayloads.push(JSON.parse(String(init.body)));
      return {
        ok: true,
        status: 201,
        json: async () => ({
          capture_id: "cap_replayed",
          shop_id: "shop_001",
          type: "sale",
          transactions: [],
          requires_review: false,
        }),
      };
    };

    const syncedItems = [];
    const stats = await syncOfflineQueue((item, result) => {
      syncedItems.push({ item, result });
    });

    assert.equal(stats.synced, 2);
    assert.equal(stats.failed, 0);
    assert.equal(await getQueuedCount(), 0);
    assert.equal(replayedPayloads.length, 2);
    assert.equal(replayedPayloads[0].transcript, "Two Milo four thousand naira thank you");
    assert.equal(replayedPayloads[1].transcript, "Three Peak Milk seven thousand naira thank you");
    assert.equal(syncedItems.length, 2);
  });

  await t.test("5. syncOfflineQueue: Aborts on connection drop and preserves remaining queued items", async () => {
    await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Capture 1 thank you",
      type: "sale",
      closing_phrase: "thank you",
    });
    await enqueueCapture({
      shop_id: "shop_001",
      transcript: "Capture 2 thank you",
      type: "sale",
      closing_phrase: "thank you",
    });

    let attempt = 0;
    globalThis.fetch = async () => {
      attempt++;
      if (attempt === 1) {
        return {
          ok: true,
          status: 201,
          json: async () => ({
            capture_id: "cap_1",
            shop_id: "shop_001",
            type: "sale",
            transactions: [],
            requires_review: false,
          }),
        };
      }
      // Second item fails due to cellular drop
      throw new TypeError("Failed to fetch");
    };

    const stats = await syncOfflineQueue();
    assert.equal(stats.synced, 1);
    assert.equal(stats.failed, 1);

    // The second capture should remain in queue with retry_count 1
    assert.equal(await getQueuedCount(), 1);
    const remaining = (await getQueuedCaptures())[0];
    assert.equal(remaining.transcript, "Capture 2 thank you");
    assert.equal(remaining.retry_count, 1);
  });

  await t.test("6. P4.8 manual counter entry: queued on device offline, replayed into /api/transactions/manual when back", async () => {
    // 1. Enqueue a manual entry while the network is down.
    navigator.onLine = false;
    const queued = await enqueueManualTransaction(
      "shop_001",
      {
        type: "sale",
        product_id: "prod_milo",
        quantity: 2,
        unit: "carton",
        amount: { amount: 500000, currency: "NGN" },
        notes: "Typed while network down",
      },
      "Milo 400g",
    );
    assert.equal(queued.kind, "manual-transaction", "entry is tagged as a manual transaction");
    assert.equal(queued.transcript, "Manual sale: 2 carton Milo 400g", "readable paper-trail label");
    assert.equal(await getQueuedCount(), 1);

    // 2. Sync attempt with the network still effectively down: the manual POST
    //    fails with a network error and the entry must survive.
    navigator.onLine = true;
    globalThis.fetch = async (url) => {
      assert.ok(String(url).includes("/api/transactions/manual"), "manual entries go to the manual endpoint");
      throw new TypeError("Failed to fetch");
    };
    const failedStats = await syncOfflineQueue();
    assert.equal(failedStats.synced, 0);
    assert.equal(await getQueuedCount(), 1, "manual entry survives a failed sync");
    const [pending] = await getQueuedCaptures();
    assert.equal(pending.kind, "manual-transaction");
    assert.notEqual(pending.last_error, undefined);

    // 3. Network back: the entry replays exactly once into the manual endpoint
    //    and is removed from the queue.
    const posted = [];
    globalThis.fetch = async (url, init = {}) => {
      assert.ok(String(url).includes("/api/transactions/manual"), "manual entries go to the manual endpoint");
      posted.push(JSON.parse(String(init.body)));
      return {
        ok: true,
        status: 201,
        json: async () => ({ transaction: { id: "tx_manual_replay" } }),
      };
    };
    const progress = [];
    const stats = await syncOfflineQueue((item, result) => {
      progress.push({ item, result });
    });
    assert.equal(stats.synced, 1);
    assert.equal(stats.failed, 0);
    assert.equal(await getQueuedCount(), 0, "queue empty after replay");
    assert.deepEqual(posted, [
      {
        type: "sale",
        product_id: "prod_milo",
        quantity: 2,
        unit: "carton",
        amount: { amount: 500000, currency: "NGN" },
        notes: "Typed while network down",
      },
    ]);
    assert.equal(progress[0].result, undefined, "manual replays carry no CaptureResult");
    assert.equal(progress[0].item.kind, "manual-transaction");
  });
});

