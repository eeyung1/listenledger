import test from "node:test";
import assert from "node:assert/strict";
import { requireIdentity } from "../dist/auth.js";
import { setupDOMEnvironment, MockEvent, MockElement } from "./test_dom.mjs";

// The lightweight shared DOM fixture lacks the native parentElement property.
Object.defineProperty(MockElement.prototype, "parentElement", {get() { return this.parentNode; }});

const identity = {owner_id: "owner-test", email: "owner@example.com", shop_id: "shop-test", shop_name: "Test shop", currency: "NGN", timezone: "Africa/Lagos"};
const reply = (status, body) => ({ok: status >= 200 && status < 300, status, json: async () => body});
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
const click = (node) => node.dispatchEvent(new MockEvent("click"));

async function mount(t, handle) {
  const { document } = setupDOMEnvironment();
  const fetchBefore = globalThis.fetch;
  t.after(() => { globalThis.fetch = fetchBefore; });
  const app = document.createElement("main"); app.id = "app";
  const chrome = document.createElement("nav"); chrome.setAttribute("data-shell-chrome", "");
  document.body.append(chrome, app);
  const requests = [];
  globalThis.fetch = async (url, options) => {
    if (url === "/api/auth/session") return reply(401, {});
    requests.push({url, options, body: JSON.parse(options.body)});
    return handle(requests.at(-1));
  };
  const result = requireIdentity(); await flush();
  const form = app.querySelector("form");
  const actions = [...app.querySelector('[role="group"]').querySelectorAll("button")];
  const button = [...form.querySelectorAll("button")].find((node) => node.type === "submit");
  const input = (name) => form.querySelector(`input[name="${name}"]`);
  return {app, chrome, result, form, actions, button, input, requests};
}
function fill(ui) {
  ui.input("email").value = identity.email;
  ui.input("password").value = "a-long-test-password";
}
function submit(ui) {
  const event = new MockEvent("submit", {cancelable: true});
  ui.form.dispatchEvent(event); assert.equal(event.defaultPrevented, true);
}

test("F36: explicit account actions show required fields and clear secrets when switching", async (t) => {
  const ui = await mount(t, () => reply(400, {}));
  assert.equal(ui.app.querySelector("select"), null);
  assert.deepEqual(ui.actions.map((b) => b.textContent), ["Sign in", "Create account", "Recover account"]);
  assert.equal(ui.actions[0].getAttribute("aria-pressed"), "true");
  assert.equal(ui.button.textContent, "Sign in");
  assert.equal(ui.input("shop_name").disabled, true);
  assert.equal(ui.input("recovery_code").required, false);
  assert.equal(ui.input("password").autocomplete, "current-password");
  fill(ui); click(ui.actions[1]);
  assert.equal(ui.app.querySelector("h1").textContent, "Create your account");
  assert.equal(ui.button.textContent, "Create account");
  assert.equal(ui.input("shop_name").required, true);
  assert.equal(ui.input("shop_name").disabled, false);
  assert.equal(ui.input("recovery_code").parentElement.hidden, true);
  assert.equal(ui.input("password").autocomplete, "new-password");
  assert.equal(ui.input("password").value, "");
  ui.input("recovery_code").value = "private-code"; click(ui.actions[2]);
  assert.equal(ui.button.textContent, "Reset password");
  assert.equal(ui.input("shop_name").disabled, true);
  assert.equal(ui.input("recovery_code").required, true);
  assert.equal(ui.input("recovery_code").disabled, false);
  assert.equal(ui.input("recovery_code").value, "");
  assert.equal(ui.input("email").value, identity.email);
  assert.equal(ui.actions.filter((b) => b.getAttribute("aria-pressed") === "true").length, 1);
});

test("F36: sign-in keeps its endpoint, headers and shell restoration", async (t) => {
  const ui = await mount(t, () => reply(200, identity));
  assert.equal(ui.chrome.hidden, true); fill(ui); submit(ui);
  assert.deepEqual(await ui.result, identity);
  assert.equal(ui.chrome.hidden, false); assert.equal(ui.app.children.length, 0);
  assert.equal(ui.requests[0].url, "/api/auth/login");
  assert.deepEqual(ui.requests[0].body, {email: identity.email, password: "a-long-test-password"});
  assert.equal(ui.requests[0].options.headers["X-ListenLedger-Request"], "1");
  assert.equal(ui.requests[0].options.method, "POST");
  assert.equal(ui.input("password").value, "");
});

test("F36: signup requires saving the recovery code before opening the shop", async (t) => {
  const ui = await mount(t, () => reply(201, {...identity, recovery_code: "save-this-once"}));
  click(ui.actions[1]); fill(ui); ui.input("shop_name").value = identity.shop_name;
  let resolved = false; ui.result.then(() => { resolved = true; });
  submit(ui); await flush();
  assert.equal(ui.requests[0].url, "/api/auth/signup");
  assert.deepEqual(ui.requests[0].body, {email: identity.email, password: "a-long-test-password", shop_name: identity.shop_name});
  assert.equal(ui.app.querySelector("code").textContent, "save-this-once");
  assert.equal(ui.chrome.hidden, true); assert.equal(resolved, false);
  assert.equal(ui.input("password").value, ""); click(ui.app.querySelector("button"));
  assert.equal((await ui.result).shop_id, identity.shop_id); assert.equal(ui.chrome.hidden, false);
});

test("F36: recovery rejects duplicate submits and mode changes, and allows retry after failure", async (t) => {
  let finishRequest; let attempt = 0;
  const ui = await mount(t, () => ++attempt === 1
    ? new Promise((resolve) => { finishRequest = resolve; })
    : reply(200, {...identity, recovery_code: "replacement-code"}));
  click(ui.actions[2]); fill(ui); ui.input("recovery_code").value = "old-code";
  submit(ui); submit(ui); click(ui.actions[0]);
  assert.equal(ui.requests.length, 1); assert.equal(ui.button.disabled, true);
  assert.ok(ui.actions.every((b) => b.disabled)); assert.equal(ui.form.getAttribute("aria-busy"), "true");
  assert.equal(ui.requests[0].url, "/api/auth/recover");
  assert.deepEqual(ui.requests[0].body, {email: identity.email, password: "a-long-test-password", recovery_code: "old-code"});
  finishRequest(reply(401, {error: "Invalid recovery code"})); await flush();
  assert.equal(ui.form.querySelector('[role="status"]').textContent, "Invalid recovery code");
  assert.equal(ui.form.getAttribute("aria-busy"), "false"); assert.equal(ui.button.disabled, false);
  assert.ok(ui.actions.every((b) => !b.disabled)); assert.equal(ui.button.textContent, "Reset password");
  ui.input("recovery_code").value = "correct-code"; submit(ui); await flush();
  assert.equal(ui.requests.length, 2); assert.equal(ui.requests[1].body.recovery_code, "correct-code");
  assert.equal(ui.app.querySelector("code").textContent, "replacement-code");
  assert.equal(ui.input("password").value, ""); assert.equal(ui.input("recovery_code").value, "");
  click(ui.app.querySelector("button")); await ui.result;
});

test("F36: network failure leaves the active flow usable for retry", async (t) => {
  let attempt = 0;
  const ui = await mount(t, () => { if (++attempt === 1) throw new Error("Network unavailable"); return reply(200, identity); });
  fill(ui); submit(ui); await flush();
  assert.equal(ui.form.querySelector('[role="status"]').textContent, "Network unavailable");
  assert.equal(ui.button.disabled, false); submit(ui); await ui.result; assert.equal(ui.requests.length, 2);
});

test("F36: existing sessions skip selection and service failures do not masquerade as sign-in", async (t) => {
  setupDOMEnvironment(); const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  globalThis.fetch = async () => reply(200, identity);
  assert.deepEqual(await requireIdentity(), identity);
  globalThis.fetch = async () => reply(503, {});
  await assert.rejects(requireIdentity(), /Sign-in service unavailable/);
});
