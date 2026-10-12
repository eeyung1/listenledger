/**
 * ListenLedger owner dashboard — SPA shell and router.
 *
 * The dashboard is a single document. When the URL hash changes the router
 * swaps the matching view template into #app, per the blueprint's routing
 * model, so navigation never triggers a full page reload.
 *
 * The router deliberately owns no business data. Server-pushed WebSocket
 * events update the mounted view in place; see TODO.md.
 */

import { resolveShopId, startSocket } from "./ws.js";
import { startViews } from "./views/index.js";

/** Views that currently have a template under src/components/. */
export type RoutePath = "/home" | "/transactions" | "/inventory";

export interface RouteDefinition {
  readonly path: RoutePath;
  /** View heading and document title suffix. */
  readonly title: string;
  /** Template path, relative to the frontend/ web root. */
  readonly template: string;
}

/**
 * Web root for view templates. Single point of change if the build later
 * copies templates under dist/ rather than serving src/ directly.
 */
const COMPONENT_BASE = "src/components/";

export const ROUTES: readonly RouteDefinition[] = [
  { path: "/home", title: "Overview", template: `${COMPONENT_BASE}home.html` },
  { path: "/transactions", title: "Transactions", template: `${COMPONENT_BASE}transactions.html` },
  { path: "/inventory", title: "Inventory", template: `${COMPONENT_BASE}inventory.html` },
];

export const DEFAULT_ROUTE: RoutePath = "/home";

/**
 * Dispatched on the #app element after a view is mounted. View modules listen
 * for it to bind their own renderers, which keeps the router ignorant of who
 * is displaying what.
 */
export const VIEW_MOUNTED_EVENT = "ll:view-mounted";

export interface ViewMountedDetail {
  readonly route: RoutePath;
}

/** Token guarding against a slow template fetch overwriting a newer view. */
let renderToken = 0;

function appRoot(): HTMLElement {
  const el = document.getElementById("app");
  if (el === null) {
    throw new Error("ListenLedger: index.html is missing the #app mount point");
  }
  return el;
}

/** Reads the current route from the URL hash, falling back to the default. */
function routeFromHash(): string {
  const raw = window.location.hash.replace(/^#/, "").trim();
  return raw.length === 0 ? DEFAULT_ROUTE : raw;
}

function findRoute(path: string): RouteDefinition | undefined {
  return ROUTES.find((route) => route.path === path);
}

async function fetchTemplate(template: string): Promise<string> {
  const response = await fetch(template, { headers: { Accept: "text/html" } });
  if (!response.ok) {
    throw new Error(`could not load template ${template} (HTTP ${response.status})`);
  }
  return response.text();
}

function setActiveNav(path: RoutePath): void {
  for (const link of document.querySelectorAll<HTMLAnchorElement>(".app-nav a[data-route]")) {
    const isActive = link.dataset.route === path;
    link.classList.toggle("is-active", isActive);
    if (isActive) {
      link.setAttribute("aria-current", "page");
    } else {
      link.removeAttribute("aria-current");
    }
  }
}

function mount(route: RouteDefinition, html: string): void {
  const root = appRoot();
  root.innerHTML = html;
  document.title = `ListenLedger — ${route.title}`;
  setActiveNav(route.path);
  root.dispatchEvent(
    new CustomEvent<ViewMountedDetail>(VIEW_MOUNTED_EVENT, {
      detail: { route: route.path },
    }),
  );
}

function mountError(route: RouteDefinition, error: unknown): void {
  console.error(`ListenLedger: failed to load view ${route.path}`, error);
  appRoot().innerHTML = [
    `<section class="view view--error" role="alert">`,
    `<h1 class="view__title">${route.title}</h1>`,
    `<p>This view could not be loaded. Make sure the frontend is being served`,
    ` from its root directory, and that <code>npm run build</code> has run.</p>`,
    `</section>`,
  ].join("");
}

async function render(): Promise<void> {
  const route = findRoute(routeFromHash());
  if (route === undefined) {
    navigate(DEFAULT_ROUTE);
    return;
  }

  const token = ++renderToken;
  try {
    const html = await fetchTemplate(route.template);
    if (token !== renderToken) return; // superseded by a newer navigation
    mount(route, html);
  } catch (error) {
    if (token !== renderToken) return;
    mountError(route, error);
  }
}

/** Navigates to a view, driving the router through the URL hash. */
export function navigate(path: RoutePath): void {
  if (routeFromHash() === path) {
    void render();
    return;
  }
  window.location.hash = `#${path}`;
}

/** Binds the router to the URL and renders the initial view. */
export function startRouter(): void {
  window.addEventListener("hashchange", () => {
    void render();
  });
  void render();
}

// Views are bound before the first render so the listener is in place when the
// first ll:view-mounted is dispatched. render() awaits a template fetch, so the
// dispatch is always a later task than this call — but binding first means
// correctness does not depend on that timing.
startViews({ root: appRoot(), mountedEvent: VIEW_MOUNTED_EVENT, shopId: resolveShopId() });

startRouter();

// Started here rather than inside the router because the socket outlives any
// individual view: routes come and go, the connection stays. Views subscribe to
// its DOM events once they are mounted.
startSocket();
