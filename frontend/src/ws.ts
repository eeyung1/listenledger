/**
 * ListenLedger realtime client.
 *
 * Owns the single WebSocket to the backend orchestrator and republishes server
 * events as DOM CustomEvents, so view code never touches the socket: a view
 * subscribes to the event it cares about and re-renders.
 *
 * The event names and envelope here mirror backend/ws/events.go, which is the
 * authority for both. Adding an event type means adding it in both places.
 */

/** Must match ws.ProtocolVersion on the server. */
export const PROTOCOL_VERSION = 1;

export type EventType =
  | "transaction.created"
  | "transaction.updated"
  | "transaction.review_required"
  | "inventory.updated"
  | "product.created";

/** The envelope every server message uses, mirroring ws.Event. */
export interface ServerEvent<T = unknown> {
  readonly type: EventType;
  readonly v: number;
  readonly shop_id: string;
  readonly sent_at: string;
  readonly data: T;
}

/** Server event type to the DOM event a view listens for. */
export const DOM_EVENT_NAMES: Readonly<Record<EventType, string>> = {
  "transaction.created": "ll:transaction-received",
  "transaction.updated": "ll:transaction-updated",
  "transaction.review_required": "ll:transaction-review-required",
  "inventory.updated": "ll:inventory-updated",
  "product.created": "ll:product-created",
};

/** Dispatched whenever the connection state changes. */
export const CONNECTION_STATUS_EVENT = "ll:connection-status";

export type ConnectionStatus = "connecting" | "open" | "reconnecting" | "closed";

export interface ConnectionStatusDetail {
  readonly status: ConnectionStatus;
  /**
   * True when this status follows a gap in the stream. The view cannot know
   * what it missed, so it must refetch a snapshot instead of assuming it only
   * needs to apply future events.
   */
  readonly resyncRequired: boolean;
  /** Consecutive failed connection attempts; 0 once healthy. */
  readonly attempt: number;
}

const DEFAULT_BASE_BACKOFF_MS = 500;
const DEFAULT_MAX_BACKOFF_MS = 15_000;

/**
 * Randomisation applied to each backoff delay. Without it, every dashboard
 * disconnected by the same server restart would retry in lockstep and
 * stampede the backend exactly when it is least able to cope.
 */
const BACKOFF_JITTER = 0.2;

export interface SocketOptions {
  readonly shopId: string;
  /** Overrides the derived same-origin URL. */
  readonly url?: string;
  readonly baseBackoffMs?: number;
  readonly maxBackoffMs?: number;
}

export class ListenLedgerSocket {
  private readonly shopId: string;
  private readonly url: string;
  private readonly baseBackoffMs: number;
  private readonly maxBackoffMs: number;

  private socket: WebSocket | null = null;
  private reconnectTimer: number | null = null;
  private attempt = 0;
  private hasConnected = false;
  private closedByCaller = false;
  private currentStatus: ConnectionStatus = "closed";

  constructor(options: SocketOptions) {
    this.shopId = options.shopId;
    this.url = options.url ?? defaultSocketUrl(options.shopId);
    this.baseBackoffMs = options.baseBackoffMs ?? DEFAULT_BASE_BACKOFF_MS;
    this.maxBackoffMs = options.maxBackoffMs ?? DEFAULT_MAX_BACKOFF_MS;
  }

  get status(): ConnectionStatus {
    return this.currentStatus;
  }

  /** Opens the connection. Safe to call when already connecting or open. */
  connect(): void {
    if (this.socket !== null) return;
    this.closedByCaller = false;
    this.openSocket("connecting");
  }

  /** Closes the connection for good: no reconnect will be scheduled. */
  close(): void {
    this.closedByCaller = true;
    this.cancelReconnect();

    const socket = this.socket;
    this.socket = null;

    if (socket === null) {
      // Never opened, so no close event is coming to update the status.
      this.setStatus("closed", false);
      return;
    }
    socket.close(1000, "dashboard closed the connection");
  }

  private openSocket(status: ConnectionStatus): void {
    this.setStatus(status, false);

    let socket: WebSocket;
    try {
      socket = new WebSocket(this.url);
    } catch (error) {
      // Constructor throws on a malformed URL, which a retry will not fix,
      // but backing off is still the least-bad response.
      console.warn("ListenLedger: could not open realtime socket", error);
      this.attempt += 1;
      this.scheduleReconnect();
      return;
    }

    this.socket = socket;
    socket.addEventListener("open", () => this.handleOpen());
    socket.addEventListener("message", (event) => this.handleMessage(event));
    socket.addEventListener("close", (event) => this.handleClose(event));
    socket.addEventListener("error", () => {
      // The browser gives no detail here, and 'close' always follows, so the
      // close handler is where the real recovery logic lives.
      console.warn("ListenLedger: realtime socket errored");
    });
  }

  private handleOpen(): void {
    const wasReconnect = this.hasConnected;
    this.hasConnected = true;
    this.attempt = 0;

    console.info(`ListenLedger: realtime connected (${this.url})`);
    // On a first connect the view has no state to reconcile; after a
    // reconnect it may have missed events and must resync.
    this.setStatus("open", wasReconnect);
  }

  private handleMessage(message: MessageEvent<unknown>): void {
    if (typeof message.data !== "string") {
      console.warn("ListenLedger: ignoring non-text frame");
      return;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(message.data);
    } catch {
      // One malformed frame is not a reason to drop a working connection.
      console.warn("ListenLedger: ignoring malformed event", message.data);
      return;
    }

    if (!isServerEvent(parsed)) {
      console.warn("ListenLedger: ignoring unrecognised event", parsed);
      return;
    }

    if (parsed.v !== PROTOCOL_VERSION) {
      console.warn(
        `ListenLedger: ignoring event protocol v${parsed.v}; this client speaks v${PROTOCOL_VERSION}`,
      );
      return;
    }

    if (!isKnownEventType(parsed.type)) {
      // Newer server, older dashboard. Ignoring unknown types is what lets
      // the two be deployed independently.
      console.info(`ListenLedger: ignoring unknown event type "${parsed.type}"`);
      return;
    }

    if (parsed.shop_id !== this.shopId) {
      // The hub partitions by shop, so this means a server-side routing bug.
      // Drop it rather than render another business's sales figures.
      console.warn(
        `ListenLedger: dropping event for shop "${parsed.shop_id}", expected "${this.shopId}"`,
      );
      return;
    }

    window.dispatchEvent(new CustomEvent(DOM_EVENT_NAMES[parsed.type], { detail: parsed }));
  }

  private handleClose(event: CloseEvent): void {
    this.socket = null;

    if (this.closedByCaller) {
      this.setStatus("closed", false);
      return;
    }

    this.attempt += 1;
    console.warn(`ListenLedger: realtime disconnected (code ${event.code}); retrying`);
    this.setStatus("reconnecting", false);
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.closedByCaller || this.reconnectTimer !== null) return;

    const exponential = Math.min(
      this.baseBackoffMs * 2 ** Math.max(0, this.attempt - 1),
      this.maxBackoffMs,
    );
    const jitter = exponential * BACKOFF_JITTER * (Math.random() * 2 - 1);
    const delay = Math.max(0, Math.round(exponential + jitter));

    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      if (this.closedByCaller) return;
      this.openSocket("reconnecting");
    }, delay);
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer === null) return;
    window.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private setStatus(status: ConnectionStatus, resyncRequired: boolean): void {
    this.currentStatus = status;
    window.dispatchEvent(
      new CustomEvent<ConnectionStatusDetail>(CONNECTION_STATUS_EVENT, {
        detail: { status, resyncRequired, attempt: this.attempt },
      }),
    );
  }
}

/** Derives the same-origin WebSocket URL, matching the backend's /ws route. */
function defaultSocketUrl(shopId: string): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = new URL(`${scheme}//${window.location.host}/ws`);
  url.searchParams.set("shop_id", shopId);
  return url.toString();
}

function isServerEvent(value: unknown): value is ServerEvent {
  if (typeof value !== "object" || value === null) return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.type === "string" &&
    typeof candidate.v === "number" &&
    typeof candidate.shop_id === "string"
  );
}

function isKnownEventType(value: string): value is EventType {
  return Object.prototype.hasOwnProperty.call(DOM_EVENT_NAMES, value);
}

let activeSocket: ListenLedgerSocket | null = null;

/**
 * Starts the shared realtime connection, or returns the existing one.
 *
 * One socket per dashboard: views attach and detach as the user navigates,
 * but the connection outlives them all.
 */
export function startSocket(shopId: string): ListenLedgerSocket {
  if (activeSocket !== null) return activeSocket;
  activeSocket = new ListenLedgerSocket({ shopId });
  activeSocket.connect();
  return activeSocket;
}

export function getSocket(): ListenLedgerSocket | null {
  return activeSocket;
}

declare global {
  interface WindowEventMap {
    "ll:transaction-received": CustomEvent<ServerEvent>;
    "ll:transaction-updated": CustomEvent<ServerEvent>;
    "ll:transaction-review-required": CustomEvent<ServerEvent>;
    "ll:inventory-updated": CustomEvent<ServerEvent>;
    "ll:product-created": CustomEvent<ServerEvent>;
    "ll:connection-status": CustomEvent<ConnectionStatusDetail>;
  }
}

