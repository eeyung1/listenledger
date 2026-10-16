/**
 * F02 — desktop sidebar and shop/account top bar.
 *
 * These tests cover the navigation module's contract: which destinations it
 * publishes, that both responsive containers receive them, and that exactly one
 * link is marked current at a time. Layout and viewport behaviour are not
 * asserted here; F06 owns the 360/390/768/1440px checks.
 */

import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, parseHTMLFragment } from "./test_dom.mjs";
import { NAV_ITEMS, NAV_ORDER, renderNavigation, syncActiveNav } from "../dist/nav.js";

const INDEX_HTML = fs.readFileSync(new URL("../index.html", import.meta.url), "utf8");
const APP_SOURCE = fs.readFileSync(new URL("../src/app.ts", import.meta.url), "utf8");

/** The two surfaces index.html provides, as the shell markup declares them. */
const SHELL = `
<div class="app-shell">
  <aside class="app-sidebar" data-shell-chrome>
    <nav class="app-nav app-nav--sidebar" aria-label="Main" data-hook="nav-sidebar"></nav>
  </aside>
  <div class="app-shell__content">
    <header class="app-topbar" data-shell-chrome>
      <span class="app-topbar__shop" data-hook="account-shop-name"></span>
      <div class="app-topbar__account" data-hook="account-controls"></div>
    </header>
    <main id="app" class="app-main"></main>
    <nav class="app-nav app-nav--mobile" aria-label="Main" data-shell-chrome data-hook="nav-mobile"></nav>
  </div>
</div>`;

/** Routes the router actually serves, read from its ROUTES table. */
function registeredRoutes() {
  return Array.from(APP_SOURCE.matchAll(/path:\s*"(\/[^"]*)"/g)).map((m) => m[1]);
}

function linksIn(container) {
  return container.querySelectorAll("a[data-route]");
}

test("F02 Shell Navigation: sidebar and mobile bar", async (t) => {
  const { document } = setupDOMEnvironment();

  t.beforeEach(() => {
    document.body.replaceChildren();
    parseHTMLFragment(SHELL, document.body);
  });

  await t.test("1. Navigation names every route the router serves and nothing else", () => {
    const registered = registeredRoutes();
    assert.ok(registered.length > 0, "expected to read routes from src/app.ts");

    // A label for a route the router cannot serve is a dead navigation entry;
    // a route with no label is a page reachable only by typing the URL.
    assert.deepEqual([...NAV_ORDER].sort(), [...registered].sort());
    for (const path of NAV_ORDER) {
      assert.equal(typeof NAV_ITEMS[path]?.label, "string", `missing nav label for ${path}`);
      assert.ok(NAV_ITEMS[path].label.length > 0, `empty nav label for ${path}`);
    }
  });

  await t.test("2. Both responsive containers receive the same destinations in order", () => {
    renderNavigation(document);

    for (const hook of ["nav-sidebar", "nav-mobile"]) {
      const container = document.querySelector(`[data-hook="${hook}"]`);
      assert.ok(container, `index.html is missing the ${hook} container`);

      const links = linksIn(container);
      assert.equal(links.length, NAV_ORDER.length, `${hook} link count`);
      assert.deepEqual(
        links.map((a) => a.getAttribute("data-route")),
        [...NAV_ORDER],
        `${hook} order`,
      );
      assert.deepEqual(
        links.map((a) => a.textContent),
        NAV_ORDER.map((p) => NAV_ITEMS[p].label),
        `${hook} labels`,
      );
      assert.deepEqual(
        links.map((a) => a.getAttribute("href")),
        NAV_ORDER.map((p) => `#${p}`),
        `${hook} hrefs`,
      );
    }
  });

  await t.test("3. Exactly one link is current, and it is announced as a page", () => {
    renderNavigation(document);

    for (const path of NAV_ORDER) {
      syncActiveNav(document, path);

      for (const hook of ["nav-sidebar", "nav-mobile"]) {
        const container = document.querySelector(`[data-hook="${hook}"]`);

        const active = linksIn(container).filter((a) => a.classList.contains("is-active"));
        assert.equal(active.length, 1, `${hook} active count on ${path}`);
        assert.equal(active[0].getAttribute("data-route"), path);

        for (const link of linksIn(container)) {
          const isCurrent = link.getAttribute("data-route") === path;
          // Colour alone must not carry the current page: aria-current is what
          // a screen reader announces, and the class is only the styling.
          assert.equal(link.getAttribute("aria-current"), isCurrent ? "page" : null);
        }
      }
    }
  });

  await t.test("4. A surface that is absent is skipped rather than fatal", () => {
    document.body.replaceChildren();
    parseHTMLFragment(`<nav class="app-nav" data-hook="nav-sidebar"></nav>`, document.body);

    assert.doesNotThrow(() => renderNavigation(document));
    assert.equal(linksIn(document.querySelector('[data-hook="nav-sidebar"]')).length, NAV_ORDER.length);
    assert.equal(document.querySelector('[data-hook="nav-mobile"]'), null);
  });

  await t.test("5. index.html declares the shell the module fills", () => {
    for (const hook of ["nav-sidebar", "nav-mobile", "account-shop-name", "account-controls"]) {
      assert.ok(INDEX_HTML.includes(`data-hook="${hook}"`), `index.html is missing ${hook}`);
    }
    // The sign-in panel hides these; the account bar and both navs must carry it.
    assert.equal(INDEX_HTML.match(/data-shell-chrome/g)?.length, 3);
    assert.ok(!INDEX_HTML.includes("app-header"), "the pre-F02 header is still present");
  });
});
