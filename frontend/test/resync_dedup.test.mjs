import test from "node:test";
import assert from "node:assert/strict";
import { applyTransactionToList, sortTransactions } from "../dist/format.js";
import {
  ListenLedgerSocket,
  CONNECTION_STATUS_EVENT,
  DOM_EVENT_NAMES,
  PROTOCOL_VERSION,
} from "../dist/ws.js";

test("Connection Resync & Deduplication Suite", async (t) => {
  // Ensure globalThis.window exists for CustomEvent dispatching
  if (typeof globalThis.window === "undefined") {
    globalThis.window = new EventTarget();
  }

  await t.test("sortTransactions orders newest first, tie-breaking by id desc", () => {
    const list = [
      { id: "txn_001", occurred_at: "2026-10-14T10:00:00Z" },
      { id: "txn_003", occurred_at: "2026-10-14T12:00:00Z" },
      { id: "txn_002", occurred_at: "2026-10-14T12:00:00Z" }, // same time as 003, tie-breaker id desc: 003 then 002
      { id: "txn_000", occurred_at: "2026-10-14T08:00:00Z" },
    ];

    sortTransactions(list);

    assert.equal(list[0].id, "txn_003");
    assert.equal(list[1].id, "txn_002");
    assert.equal(list[2].id, "txn_001");
    assert.equal(list[3].id, "txn_000");
  });

  await t.test("applyTransactionToList deduplicates existing transactions and preserves list bound", () => {
    const list = [
      { id: "txn_001", occurred_at: "2026-10-14T10:00:00Z", status: "review_required" },
    ];

    // 1. Re-applying the same transaction (e.g. from reconnect snapshot or updated status)
    const updatedTxn = { id: "txn_001", occurred_at: "2026-10-14T10:00:00Z", status: "confirmed" };
    const res1 = applyTransactionToList(list, updatedTxn, true);

    assert.equal(res1.isDuplicate, true);
    assert.equal(res1.totalIncrement, 0);
    assert.equal(list.length, 1);
    assert.equal(list[0].status, "confirmed");

    // 2. Applying a genuinely new transaction
    const newTxn = { id: "txn_002", occurred_at: "2026-10-14T11:00:00Z", status: "confirmed" };
    const res2 = applyTransactionToList(list, newTxn, true);

    assert.equal(res2.isDuplicate, false);
    assert.equal(res2.totalIncrement, 1);
    assert.equal(list.length, 2);
    assert.equal(list[0].id, "txn_002"); // newest first

    // 3. Applying with a bound limit (e.g. max 2 items)
    const thirdTxn = { id: "txn_003", occurred_at: "2026-10-14T12:00:00Z", status: "confirmed" };
    applyTransactionToList(list, thirdTxn, true, 2);

    assert.equal(list.length, 2);
    assert.equal(list[0].id, "txn_003");
    assert.equal(list[1].id, "txn_002");
    assert.equal(list.find((t) => t.id === "txn_001"), undefined); // oldest dropped past bound
  });

  await t.test("ListenLedgerSocket sequence synchronization and monotonic advance", () => {
    const socket = new ListenLedgerSocket({
      shopId: "shop_test_1",
      url: "ws://127.0.0.1:8080/ws?shop_id=shop_test_1",
    });

    assert.equal(socket.lastSequence, null);

    // Initial snapshot sync
    socket.syncSequence(42);
    assert.equal(socket.lastSequence, 42);

    // Out-of-order or older snapshot sync must not rewind cursor
    socket.syncSequence(30);
    assert.equal(socket.lastSequence, 42);

    // Later snapshot advances
    socket.syncSequence(50);
    assert.equal(socket.lastSequence, 50);
  });

  await t.test("ListenLedgerSocket gap detection triggers resyncRequired", () => {
    const socket = new ListenLedgerSocket({
      shopId: "shop_test_1",
      url: "ws://127.0.0.1:8080/ws?shop_id=shop_test_1",
    });

    socket.syncSequence(10);

    const statusEvents = [];
    const onStatus = (e) => {
      statusEvents.push(e.detail);
    };
    globalThis.window.addEventListener(CONNECTION_STATUS_EVENT, onStatus);

    try {
      // Receive next consecutive sequence (11): NO GAP
      socket["handleMessage"]({
        data: JSON.stringify({
          v: PROTOCOL_VERSION,
          type: "transaction.created",
          seq: 11,
          shop_id: "shop_test_1",
          sent_at: new Date().toISOString(),
          data: { id: "txn_11" },
        }),
      });

      assert.equal(socket.lastSequence, 11);
      assert.equal(statusEvents.length, 0);

      // Receive gapped sequence (14, missed 12 and 13): GAP DETECTED
      socket["handleMessage"]({
        data: JSON.stringify({
          v: PROTOCOL_VERSION,
          type: "transaction.created",
          seq: 14,
          shop_id: "shop_test_1",
          sent_at: new Date().toISOString(),
          data: { id: "txn_14" },
        }),
      });

      assert.equal(socket.lastSequence, 14);
      assert.equal(statusEvents.length, 1);
      assert.equal(statusEvents[0].status, "open");
      assert.equal(statusEvents[0].resyncRequired, true);
    } finally {
      globalThis.window.removeEventListener(CONNECTION_STATUS_EVENT, onStatus);
    }
  });

  await t.test("ListenLedgerSocket drops events for other shops (cross-shop isolation)", () => {
    const socket = new ListenLedgerSocket({
      shopId: "shop_test_1",
      url: "ws://127.0.0.1:8080/ws?shop_id=shop_test_1",
    });

    let receivedForeignEvent = false;
    const onTxn = () => {
      receivedForeignEvent = true;
    };
    globalThis.window.addEventListener(DOM_EVENT_NAMES["transaction.created"], onTxn);

    try {
      socket["handleMessage"]({
        data: JSON.stringify({
          v: PROTOCOL_VERSION,
          type: "transaction.created",
          seq: 1,
          shop_id: "other_shop_99", // Mismatched tenant shop
          sent_at: new Date().toISOString(),
          data: { id: "txn_leaked" },
        }),
      });

      assert.equal(receivedForeignEvent, false);
      assert.equal(socket.lastSequence, null); // sequence not updated for dropped event
    } finally {
      globalThis.window.removeEventListener(DOM_EVENT_NAMES["transaction.created"], onTxn);
    }
  });
});
