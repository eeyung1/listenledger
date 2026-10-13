/**
 * View registry.
 *
 * The router swaps a template into #app and dispatches ll:view-mounted; this
 * module listens for that event and hands control to the view owning the route.
 * The router therefore knows nothing about what any view renders, and a view
 * can be added or replaced without touching it.
 *
 * One view is mounted at a time. When the route changes the previous view's
 * cleanup runs first, because the listeners each view registers are on `window`
 * — which outlives the template swap. Without that, a view the user has already
 * navigated away from would keep reacting to every event, and its stale
 * closures would write into detached nodes.
 */

import type { RoutePath } from "../app.js";
import { mountHome } from "./home.js";
import { mountInventory } from "./inventory.js";
import { mountTransactions } from "./transactions.js";
import { mountSettings } from "./settings.js";

/** Releases everything a view bound while it was mounted. */
export type ViewCleanup = () => void;

/** A view: renders itself into the template the router just mounted. */
export type ViewModule = (root: HTMLElement, shopId: string) => ViewCleanup;

const VIEWS: Readonly<Record<RoutePath, ViewModule>> = {
  "/home": mountHome,
  "/transactions": mountTransactions,
  "/inventory": mountInventory,
  "/settings": mountSettings,
};

export interface StartViewsOptions {
  /** The persistent #app element the router renders into. */
  readonly root: HTMLElement;
  /** Event name dispatched after each mount, owned by the router. */
  readonly mountedEvent: string;
  readonly shopId: string;
}

/**
 * Binds views to the router's mount event. Call once, before the first render.
 *
 * The event name is passed in rather than imported so this module does not
 * depend on the router that already depends on it.
 */
export function startViews(options: StartViewsOptions): void {
  let cleanup: ViewCleanup | null = null;

  options.root.addEventListener(options.mountedEvent, (event) => {
    if (cleanup !== null) {
      cleanup();
      cleanup = null;
    }

    const detail = (event as CustomEvent<{ route?: RoutePath }>).detail;
    const route = detail?.route;
    if (route === undefined) return;

    // Annotated as possibly undefined: the record is exhaustive over the routes
    // the router knows, but the guard keeps this honest if the router ever
    // dispatches a route this registry has not been taught.
    const view: ViewModule | undefined = VIEWS[route];
    if (view === undefined) return;

    cleanup = view(options.root, options.shopId);
  });
}
