/**
 * Navigation model and rendering.
 *
 * The desktop sidebar and the mobile bar offer the same destinations, so the
 * labels are declared here rather than written into index.html: a route is
 * registered once in the router and named once here, and every surface follows.
 *
 * NAV_ITEMS is exhaustive over RoutePath, so registering a route without
 * deciding what to call it in the navigation is a compile error rather than a
 * page the owner can only reach by typing the URL. Nothing here may name a
 * route the router does not serve — that is what keeps dead navigation entries
 * from shipping while the redesign is still adding pages.
 */

import type { RoutePath } from "./app.js";

export interface NavItem {
  /**
   * Visible link text. Deliberately not the document title: the redesign names
   * #/home "Home" while its document title stays "Overview".
   */
  readonly label: string;
}

export const NAV_ITEMS: Readonly<Record<RoutePath, NavItem>> = {
  "/home": { label: "Home" },
  "/transactions": { label: "Transactions" },
  "/inventory": { label: "Stock" },
  "/settings": { label: "Settings" },
};

/**
 * Display order. Written out rather than derived from NAV_ITEMS so the order a
 * shopkeeper reads is a decision, not an accident of declaration order.
 */
export const NAV_ORDER: readonly RoutePath[] = [
  "/home",
  "/transactions",
  "/inventory",
  "/settings",
];

/** The navigation containers index.html provides, one per responsive surface. */
const NAV_HOOKS: readonly string[] = ["nav-sidebar", "nav-mobile"];

/**
 * Counts the navigation may present as state.
 *
 * Undefined and zero both mean "nothing waiting": a badge reading 0 is noise,
 * and the owner learns nothing from it.
 */
export interface NavigationState {
  /** Captures awaiting a review decision, shown on Transactions. */
  readonly reviewCount?: number | undefined;
}

/** Keeps a three-digit queue from pushing the label out of the mobile bar. */
const MAX_BADGE_COUNT = 9;

function navLink(path: RoutePath, state: NavigationState): HTMLAnchorElement {
  const link = document.createElement("a");
  link.href = `#${path}`;
  // syncActiveNav matches on this attribute, so the router and the label stay
  // independent.
  link.setAttribute("data-route", path);
  link.textContent = NAV_ITEMS[path].label;

  const reviewCount = state.reviewCount ?? 0;
  if (path === "/transactions" && reviewCount > 0) {
    const badge = document.createElement("span");
    badge.className = "badge badge--review";
    badge.setAttribute("data-hook", "nav-review-count");
    badge.textContent = reviewCount > MAX_BADGE_COUNT ? `${MAX_BADGE_COUNT}+` : String(reviewCount);
    link.append(badge);
    // The number is a state, not decoration: it belongs in the link's name so a
    // screen reader announces the queue along with the destination.
    link.setAttribute(
      "aria-label",
      `${NAV_ITEMS[path].label}, ${reviewCount} awaiting review`,
    );
  }
  return link;
}

/**
 * Fills every navigation container the shell rendered.
 *
 * A missing container is skipped rather than thrown on: a page that omits a
 * surface should lose that navigation, not fail to start.
 */
export function renderNavigation(root: ParentNode, state: NavigationState = {}): void {
  for (const hook of NAV_HOOKS) {
    const container = root.querySelector<HTMLElement>(`[data-hook="${hook}"]`);
    if (container === null) continue;
    container.replaceChildren(...NAV_ORDER.map((path) => navLink(path, state)));
  }
}

/**
 * Marks the link for the route now on screen, on every surface.
 *
 * aria-current is set alongside the class because the active destination is a
 * state a screen reader has to announce, not only a colour.
 */
export function syncActiveNav(root: ParentNode, activePath: RoutePath): void {
  for (const link of root.querySelectorAll<HTMLAnchorElement>(".app-nav a[data-route]")) {
    const isActive = link.dataset.route === activePath;
    link.classList.toggle("is-active", isActive);
    if (isActive) {
      link.setAttribute("aria-current", "page");
    } else {
      link.removeAttribute("aria-current");
    }
  }
}
