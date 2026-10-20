/**
 * The pending-review count in the navigation.
 *
 * The ledger already shows how many captures are awaiting a decision, but only
 * while the owner is on Transactions. A held capture has had no effect on stock
 * or revenue, so leaving it invisible from Home, Stock or Settings is how it
 * quietly rots: the owner needs to see the queue from wherever they are.
 *
 * This module owns the count itself - one read at startup, then refreshes on
 * the socket events that can change it - and hands the number to a caller that
 * knows how to draw it. Keeping the drawing out of here is what lets the update
 * logic be tested without booting the router.
 */

import { fetchTransactions } from "./api.js";
import { CONNECTION_STATUS_EVENT, DOM_EVENT_NAMES, type ConnectionStatusDetail } from "./ws.js";

export interface ReviewBadgeOptions {
  readonly shopId: string;
  /**
   * Called with the count whenever it changes, including the first successful
   * read. Not called when a read returns the number already on screen, so
   * redrawing the navigation is not paid for on every event.
   */
  readonly onChange: (count: number) => void;
}

export interface ReviewBadge {
  /** Reads the count now. Safe to call while a read is already in flight. */
  refresh(): Promise<void>;
  /** Detaches the listeners; the last count is left as it was drawn. */
  stop(): void;
}

export function startReviewBadge({ shopId, onChange }: ReviewBadgeOptions): ReviewBadge {
  // -1 rather than 0: the first read must paint even when nothing is waiting,
  // because the caller may be re-rendering a navigation it inherited.
  let shown = -1;
  let inFlight: Promise<void> | null = null;
  let stopped = false;

  const refresh = (): Promise<void> => {
    if (inFlight !== null) return inFlight;
    inFlight = (async () => {
      try {
        // One row is enough: the endpoint's total is the queue size, and the
        // navigation should not pay for a page of movements to learn a number.
        const snapshot = await fetchTransactions(shopId, 1, { status: "review_required" });
        if (stopped) return;
        const count = snapshot.total ?? snapshot.count;
        if (count !== shown) {
          shown = count;
          onChange(count);
        }
      } catch {
        // A count that cannot be read does not deserve an error banner over the
        // page the owner is working in. Keep the last accurate number: a stale
        // count is a better hint than a disappearance.
      } finally {
        inFlight = null;
      }
    })();
    return inFlight;
  };

  const refreshSoon = (): void => {
    void refresh();
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    // A resync means events were missed, so the count may have moved without
    // this tab hearing about it.
    if (detail.resyncRequired) void refresh();
  };

  window.addEventListener(DOM_EVENT_NAMES["transaction.review_required"], refreshSoon);
  window.addEventListener(DOM_EVENT_NAMES["transaction.updated"], refreshSoon);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  void refresh();

  return {
    refresh,
    stop: (): void => {
      stopped = true;
      window.removeEventListener(DOM_EVENT_NAMES["transaction.review_required"], refreshSoon);
      window.removeEventListener(DOM_EVENT_NAMES["transaction.updated"], refreshSoon);
      window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    },
  };
}
