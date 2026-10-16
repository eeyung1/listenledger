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

function navLink(path: RoutePath): HTMLAnchorElement {
  const link = document.createElement("a");
  link.href = `#${path}`;
  // syncActiveNav matches on this attribute, so the router and the label stay
  // independent.
  link.setAttribute("data-route", path);
  link.textContent = NAV_ITEMS[path].label;
  return link;
}

/**
 * Fills every navigation container the shell rendered.
 *
 * A missing container is skipped rather than thrown on: a page that omits a
 * surface should lose that navigation, not fail to start.
 */
export function renderNavigation(root: ParentNode): void {
  for (const hook of NAV_HOOKS) {
    const container = root.querySelector<HTMLElement>(`[data-hook="${hook}"]`);
    if (container === null) continue;
    container.replaceChildren(...NAV_ORDER.map(navLink));
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
