/**
 * Shared presentation for the non-happy-path UI states (F05).
 *
 * Every view shows the same handful of states — loading, empty, a failed load,
 * a failed save and a dropped connection — but before F05 each view invented
 * its own markup, wording and recovery, so the same failure looked different on
 * every page. This module is the single definition of how those states look, so
 * a view asks for "an empty state with a Retry action" instead of hand-building
 * one. It renders no business data and holds no state of its own.
 *
 * The disconnected banner also lives here: it is the one state that belongs to
 * the whole shell rather than a single view, so it is installed once and reads
 * the realtime client's connection events.
 */

export type UiStateKind = "loading" | "empty" | "error" | "offline";

export interface UiStateOptions {
  /** One short line naming the state; rendered as the heading. */
  readonly title: string;
  /** Optional second line explaining what the owner can do about it. */
  readonly detail?: string;
  /** Optional recovery action label, e.g. "Retry". */
  readonly actionLabel?: string;
  /** Invoked on click. Required whenever actionLabel is set. */
  readonly onAction?: () => void;
}

const ICONS: Readonly<Record<UiStateKind, string>> = {
  loading: "⏳",
  empty: "○",
  error: "⚠",
  offline: "📶",
};

/** Builds the state block into a caller-provided host element. */
function build(kind: UiStateKind, options: UiStateOptions, host: HTMLElement): void {
  host.classList.add("ui-state", `ui-state--${kind}`);
  host.setAttribute("data-state", kind);
  // A failed load and a dropped connection interrupt the owner; an empty or
  // loading state is merely informational, so only the former are alerts.
  host.setAttribute("role", kind === "error" || kind === "offline" ? "alert" : "status");

  const icon = document.createElement("span");
  icon.className = "ui-state__icon";
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = ICONS[kind];

  const title = document.createElement("p");
  title.className = "ui-state__title";
  title.textContent = options.title;

  host.append(icon, title);

  if (options.detail !== undefined && options.detail !== "") {
    const detail = document.createElement("p");
    detail.className = "ui-state__detail";
    detail.textContent = options.detail;
    host.append(detail);
  }

  if (options.actionLabel !== undefined && options.onAction !== undefined) {
    const action = document.createElement("button");
    action.type = "button";
    action.className = "button button--secondary button--sm ui-state__action";
    action.textContent = options.actionLabel;
    action.addEventListener("click", () => options.onAction?.());
    host.append(action);
  }
}

/** A block state for a container region. */
export function uiState(kind: UiStateKind, options: UiStateOptions): HTMLDivElement {
  const box = document.createElement("div");
  build(kind, options, box);
  return box;
}

/** The same block as a list item, for <ul>-based regions. */
export function uiStateItem(kind: UiStateKind, options: UiStateOptions): HTMLLIElement {
  const item = document.createElement("li");
  build(kind, options, item);
  return item;
}

/**
 * The same block as a full-width table row, for the ledger and catalogue
 * bodies whose other children are <tr> elements.
 */
export function uiStateRow(
  kind: UiStateKind,
  options: UiStateOptions,
  colSpan: number,
): HTMLTableRowElement {
  const row = document.createElement("tr");
  row.className = "ui-state-row";
  const cell = document.createElement("td");
  cell.className = "ui-state-cell";
  cell.colSpan = colSpan;
  cell.append(uiState(kind, options));
  row.append(cell);
  return row;
}

export type FormStatusKind = "info" | "success" | "error";

/**
 * Standardises an inline form status line: a save in progress, a save that
 * worked, and a validation or save failure must be visually distinct, so call
 * sites choose a kind instead of setting a bare string.
 */
export function setFormStatus(
  element: HTMLElement | null,
  kind: FormStatusKind,
  text: string,
): void {
  if (element === null) return;
  element.className = `form__status form__status--${kind}`;
  element.textContent = text;
  element.hidden = false;
}

export type ConnectionState = "connecting" | "open" | "reconnecting" | "closed";

const CONNECTION_LABELS: Readonly<Record<ConnectionState, string>> = {
  connecting: "Connecting…",
  open: "Live",
  reconnecting: "Reconnecting…",
  closed: "Offline",
};

/** Human-readable label for a realtime connection status. */
export function connectionStatusLabel(status: string): string {
  return (CONNECTION_LABELS as Readonly<Record<string, string>>)[status] ?? status;
}

export interface ConnectionStatusLike {
  readonly status: string;
}

export interface ConnectionBannerOptions {
  /** Window event name the realtime client dispatches on every status change. */
  readonly eventName: string;
  /** Invoked when the owner asks to retry the connection immediately. */
  readonly onRetry: () => void;
}

const BANNER_MESSAGES: Readonly<Record<"offline" | "reconnecting" | "closed", string>> = {
  offline:
    "You're offline — new captures are saved on this device and sync when the connection returns.",
  reconnecting: "Connection lost — reconnecting now. Queued captures sync automatically.",
  closed: "Disconnected — tap Retry to reconnect.",
};

/**
 * Installs the shell's disconnected banner and returns a cleanup function.
 *
 * The banner appears only when the shop is genuinely disconnected: the browser
 * is offline, or the realtime socket has dropped and is backing off. The first
 * `connecting` state at boot is deliberately not surfaced — showing "Connection
 * lost" before a connection was ever made would train the owner to ignore it.
 */
export function installConnectionBanner(
  doc: Document,
  options: ConnectionBannerOptions,
): () => void {
  const banner = doc.createElement("div");
  banner.className = "app-banner";
  banner.setAttribute("role", "status");
  banner.setAttribute("aria-live", "polite");
  banner.hidden = true;

  const message = doc.createElement("p");
  message.className = "app-banner__message";

  const retry = doc.createElement("button");
  retry.type = "button";
  retry.className = "button button--secondary button--sm app-banner__retry";
  retry.textContent = "Retry now";
  retry.addEventListener("click", () => options.onRetry());

  banner.append(message, retry);
  doc.body.append(banner);

  let lastStatus = "connecting";

  const render = (): void => {
    const offline = typeof navigator !== "undefined" && navigator.onLine === false;
    let kind: "offline" | "reconnecting" | "closed" | null = null;
    if (offline) {
      kind = "offline";
    } else if (lastStatus === "reconnecting") {
      kind = "reconnecting";
    } else if (lastStatus === "closed") {
      kind = "closed";
    }

    if (kind === null) {
      banner.hidden = true;
      return;
    }
    banner.hidden = false;
    banner.className = `app-banner app-banner--${kind}`;
    banner.setAttribute("data-state", kind);
    message.textContent = BANNER_MESSAGES[kind];
  };

  const onStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusLike>).detail;
    if (detail !== undefined && typeof detail.status === "string") {
      lastStatus = detail.status;
    }
    render();
  };

  window.addEventListener(options.eventName, onStatus);
  window.addEventListener("online", render);
  window.addEventListener("offline", render);
  render();

  return () => {
    window.removeEventListener(options.eventName, onStatus);
    window.removeEventListener("online", render);
    window.removeEventListener("offline", render);
    if (typeof banner.remove === "function") banner.remove();
  };
}
