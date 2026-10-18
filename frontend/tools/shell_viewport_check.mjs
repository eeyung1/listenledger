#!/usr/bin/env node
/**
 * F06 — shell viewport, overflow and keyboard verification.
 *
 * Serves the built frontend with a stubbed API, drives a real headless
 * Chrome/Chromium over the DevTools Protocol at the four supported widths, and
 * reports:
 *
 *   - page-level horizontal overflow (document scroll width vs the viewport),
 *     plus the offending elements when there is any;
 *   - keyboard reachability of every navigation destination exposed at that
 *     width, using real Tab / Enter key events rather than synthetic ones;
 *   - the active destination's aria-current marker;
 *   - uncaught page errors and console errors per route.
 *
 * It is deliberately dependency-free (Node's built-in fetch/WebSocket plus an
 * installed browser) and is NOT wired into `npm test`, because CI has no
 * browser. Screenshots are written for human review; they are evidence, not
 * assertions.
 *
 * Usage:
 *   node tools/shell_viewport_check.mjs [--chrome PATH] [--shots DIR] [--headed]
 *
 * Exit code is non-zero when a check fails, so it can gate a release step.
 */

import { createServer } from "node:http";
import { readFile, mkdir, writeFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const FRONTEND_ROOT = path.resolve(fileURLToPath(new URL("..", import.meta.url)));

/** Widths the redesign must support (F06: 360, 390, 768, 1440). */
const WIDTHS = [360, 390, 768, 1440];
/** Every route the router currently serves; F04 adds more later. */
const ROUTES = ["/home", "/transactions", "/inventory", "/settings"];
const VIEWPORT_HEIGHT = 900;
/** A mobile-width run is the one where the bottom bar is the navigation. */
const MOBILE_MAX = 767;

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".ico": "image/x-icon",
  ".webmanifest": "application/manifest+json",
};

const args = process.argv.slice(2);
function argValue(flag) {
  const i = args.indexOf(flag);
  return i === -1 ? undefined : args[i + 1];
}
const SHOTS_DIR = path.resolve(argValue("--shots") ?? path.join(tmpdir(), "ll-f06-shots"));
const HEADED = args.includes("--headed");

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/* ── Stub data ─────────────────────────────────────────────────────────────
 * Deliberately verbose in a couple of places (a long product name, a large
 * amount) so the layout is tested against realistic worst cases rather than
 * the shortest strings the API could return.
 * ------------------------------------------------------------------------ */
const MONEY = (amount) => ({ amount, currency: "NGN" });
const NOW = "2026-10-18T15:00:00Z";

function product(i, name) {
  return {
    id: `prd_${i}`,
    shop_id: "shop_f06",
    sku: `SKU-${1000 + i}`,
    name,
    aliases: [name.split(" ")[0].toLowerCase()],
    price: MONEY(185000 + i * 2500),
    quantity: i % 5,
    base_unit: "pack",
    low_stock_threshold: 6,
    archived_at: null,
  };
}

function transaction(i) {
  const review = i % 3 === 0;
  return {
    id: `txn_${i}`,
    shop_id: "shop_f06",
    type: i % 4 === 0 ? "restock" : "sale",
    product_id: `prd_${i}`,
    product_name:
      i === 1
        ? "Golden Penny Semovita Refill 1kg Family Pack (promotional)"
        : ["Milo 500g", "Peak Milk 400g", "Indomie Carton", "Sugar 1kg"][i % 4],
    quantity: 1 + (i % 5),
    unit: "pack",
    amount: MONEY(250000 + i * 137500),
    confidence: review ? 0.62 : 0.94,
    status: review ? "review_required" : "confirmed",
    occurred_at: NOW,
    created_at: NOW,
    transcript: "two cartons of milo five hundred naira each",
    receipt_id: `rcpt_${i}`,
  };
}

function customer(i) {
  return {
    id: `cus_${i}`,
    shop_id: "shop_f06",
    name: ["Ada Obi", "Chinedu Eze", "Ngozi Bello", "Musa Ibrahim", "Blessing Ade", "Tunde Okafor"][i % 6],
    phone: `080${30000000 + i}`,
    notes: i === 2 ? "Pays on market days. Prefers SMS reminders." : "",
    outstanding_balance: MONEY(i * 450000),
    created_at: NOW,
    updated_at: NOW,
  };
}

function expense(i) {
  return {
    id: `exp_${i}`,
    shop_id: "shop_f06",
    category: "transport",
    amount: MONEY(120000 + i * 30000),
    description: i === 0 ? "Keke to market and back with restock" : "Generator fuel",
    receipt_ref: `RC-${i}`,
    occurred_at: NOW,
    created_by: "owner@shop_f06",
    created_at: NOW,
  };
}

const GATES = {
  min_full_transaction_accuracy: 0.9,
  min_sku_accuracy: 0.95,
  min_quantity_accuracy: 0.95,
  min_unit_accuracy: 0.95,
  min_amount_accuracy: 0.95,
  max_false_capture_rate: 0.05,
  max_missed_transaction_rate: 0.05,
  max_correction_rate: 0.1,
  min_protocol_compliance_rate: 0.95,
  max_p95_latency_ms: 1500,
  agreed_at: NOW,
  agreed_by: "owner@shop_f06",
};

const STEPS = {
  profile: { completed: true, title: "Shop details", description: "Name, currency and timezone" },
  catalogue: { completed: true, title: "Catalogue", description: "Add your products and prices", count: 8 },
  devices: { completed: false, title: "Microphone", description: "Test capture on this device" },
  staff_training: { completed: false, title: "Staff training", description: "Walk the team through the phrases" },
  target_gates: { completed: false, title: "Accuracy gates", description: "Agree the pilot targets" },
  test_capture: { completed: false, title: "First capture", description: "Record a real sale" },
};

const API = {
  "/api/auth/session": () => ({
    owner_id: "own_f06",
    email: "owner@shop_f06",
    shop_id: "shop_f06",
    shop_name: "Iya Basira Provisions",
    currency: "NGN",
    timezone: "Africa/Lagos",
  }),
  "/api/summary": () => ({
    shop_id: "shop_f06",
    currency: "NGN",
    today_sales_amount: MONEY(4825000),
    today_sales_count: 17,
    today_expenses_amount: MONEY(240000),
    today_expenses_count: 2,
    today_net_profit: MONEY(4585000),
    pending_reviews: 3,
    low_stock_count: 4,
    last_seq: 12,
    generated_at: NOW,
  }),
  "/api/inventory": () => ({
    shop_id: "shop_f06",
    products: [
      product(1, "Golden Penny Semovita Refill 1kg Family Pack (promotional)"),
      product(2, "Milo 500g"),
      product(3, "Peak Milk 400g"),
      product(4, "Indomie Chicken Carton"),
      product(5, "Sugar 1kg"),
      product(6, "Royco Cubes"),
      product(7, "Peak Milk Sachet"),
      product(8, "Detergent Bar"),
    ],
    count: 8,
    total: 8,
    low_stock_count: 4,
    last_seq: 12,
    generated_at: NOW,
  }),
  "/api/transactions": () => ({
    shop_id: "shop_f06",
    transactions: Array.from({ length: 9 }, (_, i) => transaction(i + 1)),
    count: 9,
    total: 9,
    limit: 25,
    offset: 0,
    last_seq: 12,
    generated_at: NOW,
  }),
  "/api/customers": () => ({
    shop_id: "shop_f06",
    customers: Array.from({ length: 6 }, (_, i) => customer(i)),
    count: 6,
    total: 6,
    limit: 25,
    offset: 0,
    generated_at: NOW,
  }),
  "/api/customers/credit-summary": () => ({
    shop_id: "shop_f06",
    currency: "NGN",
    total_customers_with_debt: 4,
    total_outstanding_debt: MONEY(1350000),
    today_credit_sales_amount: MONEY(500000),
    today_repayments_amount: MONEY(150000),
  }),
  "/api/expenses": () => ({
    shop_id: "shop_f06",
    expenses: Array.from({ length: 4 }, (_, i) => expense(i)),
    count: 4,
    total: 4,
    limit: 25,
    offset: 0,
    generated_at: NOW,
  }),
  "/api/settings": () => ({
    shop: {
      id: "shop_f06",
      owner_id: "own_f06",
      name: "Iya Basira Provisions",
      currency: "NGN",
      timezone: "Africa/Lagos",
      confidence_threshold: 0.75,
      sale_phrase: "I have sold",
      restock_phrase: "I have restocked",
      staff_trained_at: null,
      audio_retention_opt_in: false,
    },
    devices: [],
  }),
  "/api/settings/target-gates": () => ({ gates: GATES, is_agreed: true }),
  "/api/devices": () => ({ devices: [] }),
  "/api/onboarding/status": () => ({
    shop_id: "shop_f06",
    steps: STEPS,
    completed_steps: 2,
    total_steps: 6,
    is_complete: false,
  }),
  "/api/pilot/reconciliations": () => ({ reconciliations: [] }),
  "/api/pilot/cohort": () => ({
    shop_id: "shop_f06",
    cohort_phase: "phase1_controlled",
    stage_status: "active",
    notes: "F06 viewport check fixture",
  }),
  "/api/pilot/cohort/evaluate-readiness": () => ({
    shop_id: "shop_f06",
    current_phase: "phase1_controlled",
    next_phase: "phase2_dual_shop",
    readiness_status: "blocked",
    checks: [],
    all_checks_satisfied: false,
  }),
  "/api/captures": () => ({ captures: [], count: 0, generated_at: NOW }),
};

/* ── Local server: static frontend + stub API + a WebSocket that stays open ─ */
async function startServer() {
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1");
    const pathname = url.pathname;

    if (API[pathname]) {
      res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      res.end(JSON.stringify(API[pathname]()));
      return;
    }
    if (pathname.startsWith("/api/")) {
      process.stderr.write(`[stub] no fixture for ${pathname}\n`);
      res.writeHead(404, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: `no stub for ${pathname}` }));
      return;
    }

    if (pathname === "/favicon.ico") {
      // Browsers ask for this unprompted; index.html declares no icon, so 204
      // keeps the console clean instead of reporting a meaningless 404.
      res.writeHead(204).end();
      return;
    }
    const relative = pathname === "/" ? "index.html" : pathname.replace(/^\/+/, "");
    const file = path.join(FRONTEND_ROOT, relative);
    if (!file.startsWith(FRONTEND_ROOT)) {
      res.writeHead(403).end("forbidden");
      return;
    }
    try {
      const body = await readFile(file);
      res.writeHead(200, { "content-type": MIME[path.extname(file)] ?? "application/octet-stream" });
      res.end(body);
    } catch {
      res.writeHead(404).end("not found");
    }
  });

  // The app opens ws://host/ws. Holding the upgrade open keeps the socket in
  // the "open" state so the F05 disconnected banner stays out of the way; the
  // transport itself is not what F06 measures.
  server.on("upgrade", (req, socket) => {
    const key = req.headers["sec-websocket-key"];
    if (!key) {
      socket.destroy();
      return;
    }
    const accept = createHash("sha1")
      .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
      .digest("base64");
    socket.write(
      "HTTP/1.1 101 Switching Protocols\r\n" +
        "Upgrade: websocket\r\n" +
        "Connection: Upgrade\r\n" +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
  });

  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  return { server, origin: `http://127.0.0.1:${port}` };
}

/* ── Chrome + CDP ──────────────────────────────────────────────────────── */
function findChrome() {
  const explicit = argValue("--chrome") ?? process.env.CHROME_PATH;
  const candidates = [
    explicit,
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
    "/snap/bin/chromium",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  ].filter(Boolean);
  for (const candidate of candidates) if (existsSync(candidate)) return candidate;
  throw new Error("No Chrome/Chromium found. Pass --chrome PATH or set CHROME_PATH.");
}

async function launchChrome(chromePath, userDataDir) {
  const child = spawn(
    chromePath,
    [
      HEADED ? "--headless=new" : "--headless=new",
      "--disable-gpu",
      "--no-sandbox",
      "--disable-dev-shm-usage",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-extensions",
      "--remote-debugging-port=0",
      `--user-data-dir=${userDataDir}`,
      "about:blank",
    ],
    { stdio: "ignore" },
  );

  const portFile = path.join(userDataDir, "DevToolsActivePort");
  for (let i = 0; i < 100; i += 1) {
    if (existsSync(portFile)) {
      const [port] = (await readFile(portFile, "utf8")).split("\n");
      if (port) return { child, port: Number(port) };
    }
    await sleep(100);
  }
  child.kill("SIGKILL");
  throw new Error("Chrome did not open a DevTools port");
}

async function pageTargetUrl(port) {
  for (let i = 0; i < 100; i += 1) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/json/list`);
      const list = await res.json();
      const page = list.find((t) => t.type === "page" && t.webSocketDebuggerUrl);
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      /* not up yet */
    }
    await sleep(100);
  }
  throw new Error("Chrome exposed no page target");
}

class CDP {
  constructor(ws) {
    this.ws = ws;
    this.nextId = 0;
    this.pending = new Map();
    this.events = new Map();
    ws.addEventListener("message", (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id !== undefined) {
        const entry = this.pending.get(msg.id);
        if (!entry) return;
        this.pending.delete(msg.id);
        if (msg.error) entry.reject(new Error(`${entry.method}: ${msg.error.message}`));
        else entry.resolve(msg.result);
        return;
      }
      for (const handler of this.events.get(msg.method) ?? []) handler(msg.params);
    });
  }

  static async connect(url) {
    const ws = new WebSocket(url);
    await new Promise((resolve, reject) => {
      ws.addEventListener("open", resolve, { once: true });
      ws.addEventListener("error", () => reject(new Error("CDP socket failed")), { once: true });
    });
    return new CDP(ws);
  }

  on(method, handler) {
    if (!this.events.has(method)) this.events.set(method, []);
    this.events.get(method).push(handler);
  }

  send(method, params = {}) {
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, method });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  async eval(expression) {
    const result = await this.send("Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.exception?.description ?? "page threw");
    }
    return result.result.value;
  }

  close() {
    try {
      this.ws.close();
    } catch {
      /* already closed */
    }
  }
}

/* ── Checks ────────────────────────────────────────────────────────────── */
const OVERFLOW_PROBE = `(() => {
  const vw = document.documentElement.clientWidth;
  const vwInner = window.innerWidth;
  const offenders = [];
  for (const el of document.querySelectorAll("body *")) {
    const style = getComputedStyle(el);
    if (style.position === "fixed") continue;
    if (style.display === "none" || style.visibility === "hidden") continue;
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    if (rect.right <= vw + 1) continue;
    // Contained by a scrollable ancestor that itself fits: not page overflow.
    let contained = false;
    for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
      const ax = getComputedStyle(a).overflowX;
      if (ax === "auto" || ax === "scroll" || ax === "hidden") {
        if (a.getBoundingClientRect().right <= vw + 1) { contained = true; }
        break;
      }
    }
    if (contained) continue;
    offenders.push({
      tag: el.tagName.toLowerCase(),
      cls: String(el.className || "").slice(0, 70),
      right: Math.round(rect.right),
      width: Math.round(rect.width),
    });
    if (offenders.length >= 12) break;
  }
  return {
    vw,
    vwInner,
    scrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
    overflow: document.documentElement.scrollWidth - vw,
    offenders,
  };
})()`;

const FOCUS_PROBE = `(() => {
  const a = document.activeElement;
  if (!a || a === document.body) return null;
  const rect = a.getBoundingClientRect();
  const style = getComputedStyle(a);
  return {
    tag: a.tagName.toLowerCase(),
    route: a.getAttribute("data-route"),
    label: (a.getAttribute("aria-label") || a.textContent || "").trim().slice(0, 48),
    visible: rect.width > 0 && rect.height > 0,
    outlineStyle: style.outlineStyle,
    outlineWidth: parseFloat(style.outlineWidth) || 0,
  };
})()`;

async function waitForReady(cdp) {
  for (let i = 0; i < 60; i += 1) {
    const ready = await cdp.eval(
      `document.readyState === "complete" && !!document.querySelector("#app") && document.querySelector("#app").children.length > 0`,
    );
    if (ready) {
      await sleep(500); // let the view's fetches land
      return;
    }
    await sleep(100);
  }
  throw new Error("view did not render");
}

async function pressKey(cdp, { key, code, vk }) {
  await cdp.send("Input.dispatchKeyEvent", {
    type: "rawKeyDown",
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
    key,
    code,
  });
  await cdp.send("Input.dispatchKeyEvent", {
    type: "keyUp",
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
    key,
    code,
  });
}

async function keyboardWalk(cdp, limit) {
  await cdp.eval(`document.activeElement && document.activeElement.blur && document.activeElement.blur(); void 0`);
  const stops = [];
  for (let i = 0; i < limit; i += 1) {
    await pressKey(cdp, { key: "Tab", code: "Tab", vk: 9 });
    await sleep(30);
    const focus = await cdp.eval(FOCUS_PROBE);
    if (focus) stops.push(focus);
  }
  return stops;
}

/* ── Report ────────────────────────────────────────────────────────────── */
function formatOverflow(o) {
  if (o.overflow <= 0) return "ok";
  const worst = o.offenders
    .slice(0, 3)
    .map((x) => `${x.tag}.${x.cls}[right=${x.right}]`)
    .join(", ");
  return `overflow ${o.overflow}px${worst ? ` — ${worst}` : ""}`;
}

async function main() {
  const failures = [];
  const { server, origin } = await startServer();
  const chromePath = findChrome();
  const userDataDir = path.join(tmpdir(), `ll-f06-chrome-${process.pid}`);
  await mkdir(userDataDir, { recursive: true });
  await mkdir(SHOTS_DIR, { recursive: true });

  const { child, port } = await launchChrome(chromePath, userDataDir);
  const cdp = await CDP.connect(await pageTargetUrl(port));

  const pageErrors = [];
  const consoleErrors = [];
  cdp.on("Runtime.exceptionThrown", (p) =>
    pageErrors.push(p.exceptionDetails?.exception?.description ?? "unknown page error"),
  );
  cdp.on("Runtime.consoleAPICalled", (p) => {
    if (p.type === "error") {
      consoleErrors.push((p.args ?? []).map((a) => a.value ?? a.description ?? "").join(" ").slice(0, 160));
    }
  });
  cdp.on("Log.entryAdded", (p) => {
    if (p.entry?.level === "error") consoleErrors.push(String(p.entry.text).slice(0, 160));
  });

  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");
  await cdp.send("Log.enable");

  const report = { origin, chrome: chromePath, widths: {}, keyboard: {}, console: {} };

  for (const width of WIDTHS) {
    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width,
      height: VIEWPORT_HEIGHT,
      deviceScaleFactor: 1,
      mobile: width <= MOBILE_MAX,
    });
    report.widths[width] = {};

    for (const route of ROUTES) {
      await cdp.send("Page.navigate", { url: `${origin}/#${route}` });
      await waitForReady(cdp);

      const overflow = await cdp.eval(OVERFLOW_PROBE);
      report.widths[width][route] = overflow;
      if (overflow.overflow > 0) {
        failures.push(`${width}px ${route}: page-level horizontal overflow (${formatOverflow(overflow)})`);
      }

      const shot = await cdp.send("Page.captureScreenshot", {
        format: "png",
        captureBeyondViewport: false,
      });
      await writeFile(
        path.join(SHOTS_DIR, `${width}-${route.replace(/^\//, "") || "root"}.png`),
        Buffer.from(shot.data, "base64"),
      );
    }

    // Keyboard: every navigation link exposed at this width must be reachable
    // by Tab, and the current route must announce itself with aria-current.
    const expectedRoutes = await cdp.eval(
      `Array.from(document.querySelectorAll(".app-nav a[data-route]")).filter((a) => a.getBoundingClientRect().width > 0).map((a) => a.getAttribute("data-route"))`,
    );
    const stops = await keyboardWalk(cdp, 24);
    const reachedRoutes = [...new Set(stops.filter((s) => s.route && s.visible).map((s) => s.route))];
    const active = await cdp.eval(
      `(() => { const a = document.querySelector(".app-nav a[aria-current='page']"); return a ? a.getAttribute("data-route") : null; })()`,
    );

    report.keyboard[width] = { expectedRoutes, reachedRoutes, activeRoute: active, stops: stops.length };

    const missing = expectedRoutes.filter((r) => !reachedRoutes.includes(r));
    if (missing.length > 0) {
      failures.push(`${width}px: navigation not reachable by Tab: ${missing.join(", ")}`);
    }
    // A destination you can reach but not see is not usable by keyboard.
    const noRing = stops.filter(
      (s) => s.route && s.visible && (s.outlineStyle === "none" || s.outlineWidth === 0),
    );
    if (noRing.length > 0) {
      failures.push(
        `${width}px: focused navigation without a visible focus ring: ${noRing.map((s) => s.route).join(", ")}`,
      );
    }
    if (active !== ROUTES[ROUTES.length - 1]) {
      failures.push(`${width}px: aria-current is ${active}, expected the current route`);
    }

    // Enter must activate a focused destination (real key event, not .click()).
    const target = expectedRoutes.find((r) => r !== active) ?? expectedRoutes[0];
    if (target) {
      // Both surfaces are in the DOM; only one is visible per breakpoint, and
      // focusing the hidden one is a silent no-op that would test the wrong
      // element. Focus the visible link for this width.
      await cdp.eval(
        `Array.from(document.querySelectorAll('.app-nav a[data-route="${target}"]'))
           .find((a) => a.getBoundingClientRect().width > 0).focus(); void 0`,
      );
      await pressKey(cdp, { key: "Enter", code: "Enter", vk: 13 });
      await waitForReady(cdp);
      const hash = await cdp.eval(`window.location.hash`);
      if (hash !== `#${target}`) {
        failures.push(`${width}px: Enter on the ${target} link did not navigate (hash ${hash})`);
      } else {
        await cdp.send("Page.navigate", { url: `${origin}/#/settings` });
        await waitForReady(cdp);
      }
    }
  }

  report.console = {
    pageErrors: [...new Set(pageErrors)].slice(0, 12),
    consoleErrors: [...new Set(consoleErrors)].slice(0, 12),
  };

  cdp.close();
  child.kill("SIGKILL");
  server.close();
  await rm(userDataDir, { recursive: true, force: true });

  /* ── Print ─────────────────────────────────────────────────────────── */
  console.log(`F06 shell check — ${chromePath}`);
  console.log(`Screenshots: ${SHOTS_DIR}\n`);
  for (const width of WIDTHS) {
    console.log(`── ${width}px ${width <= MOBILE_MAX ? "(mobile)" : ""}`);
    for (const route of ROUTES) {
      const o = report.widths[width][route];
      console.log(`   ${route.padEnd(14)} ${formatOverflow(o)}   [scrollWidth ${o.scrollWidth} / vw ${o.vw}]`);
    }
    const k = report.keyboard[width];
    console.log(`   keyboard       reachable: ${k.reachedRoutes.join(", ") || "(none)"}  active: ${k.activeRoute}`);
  }
  if (report.console.pageErrors.length || report.console.consoleErrors.length) {
    console.log("\nConsole output:");
    for (const e of report.console.pageErrors) console.log(`   page error: ${e}`);
    for (const e of report.console.consoleErrors) console.log(`   console error: ${e}`);
  }

  console.log(failures.length ? `\nFAIL (${failures.length})` : "\nPASS");
  for (const f of failures) console.log(` - ${f}`);

  await writeFile(path.join(SHOTS_DIR, "report.json"), JSON.stringify(report, null, 2));
  process.exit(failures.length ? 1 : 0);
}

main().catch((error) => {
  console.error(error);
  process.exit(2);
});
