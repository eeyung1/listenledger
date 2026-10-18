import { el } from "./format.js";

export interface Identity { owner_id: string; email: string; shop_id: string; shop_name: string; currency: string; timezone: string }
interface AuthResponse extends Identity { recovery_code?: string }

/**
 * Shows or hides the parts of the shell that only make sense for a signed-in
 * shop: the navigation and the account bar. #app itself stays visible, because
 * the sign-in panel is rendered there.
 */
function setShellChromeVisible(visible: boolean): void {
 for (const node of document.querySelectorAll<HTMLElement>("[data-shell-chrome]")) node.hidden = !visible;
}

type AuthMode = "login" | "signup" | "recover";

export async function requireIdentity(): Promise<Identity> {
 const session = await fetch("/api/auth/session");
 if (session.ok) return await session.json() as Identity;
 if (session.status !== 401) throw new Error("Sign-in service unavailable. Please reload to retry.");
 setShellChromeVisible(false);
 return new Promise<Identity>((resolve) => {
  const root = document.getElementById("app")!;
  const section = el("section", "auth-panel");
  const title = el("h1", "");
  const description = el("p", ""); description.id = "auth-description";
  const actions = el("div", "form__actions");
  actions.setAttribute("role", "group"); actions.setAttribute("aria-label", "Account action");
  const form = document.createElement("form"); form.id = "auth-form";
  form.setAttribute("aria-describedby", description.id);
  let mode: AuthMode = "login";
  let submitting = false;
  const modeButtons = new Map<AuthMode, HTMLButtonElement>();
  const labels: Record<AuthMode, string> = {login: "Sign in", signup: "Create account", recover: "Recover account"};
  function field(label: string, name: string, type: string, autocomplete: string): HTMLInputElement {
   const wrapper = document.createElement("label"); wrapper.textContent = label;
   const input = document.createElement("input"); input.type = type; input.setAttribute("name", name);
   input.autocomplete = autocomplete as AutoFill; input.required = true;
   wrapper.append(input); form.append(wrapper); return input;
  }
  const email = field("Email", "email", "email", "email");
  const password = field("Password (12–128 characters)", "password", "password", "current-password");
  password.minLength = 12; password.maxLength = 128;
  const shop = field("Shop name", "shop_name", "text", "organization"); shop.maxLength = 120;
  const recovery = field("Recovery code", "recovery_code", "password", "off");
  const hint = el("p", "", "Use the private recovery code saved when you created your account. Email recovery is not configured. A successful reset replaces your code and signs out other sessions.");
  const status = el("p", ""); status.setAttribute("role", "status"); status.setAttribute("aria-live", "polite");
  const button = document.createElement("button"); button.type = "submit"; button.className = "button button--primary";
  function renderMode(): void {
   title.textContent = mode === "login" ? "Sign in to ListenLedger" : mode === "signup" ? "Create your account" : "Recover your account";
   description.textContent = mode === "login" ? "Use your email and password to open your shop."
    : mode === "signup" ? "Create an owner account and set up your shop." : "Use your recovery code and choose a new password.";
   for (const [value, action] of modeButtons) {
    action.setAttribute("aria-pressed", String(value === mode));
    action.className = value === mode ? "button button--primary button--sm" : "button button--secondary button--sm";
   }
   shop.parentElement!.hidden = mode !== "signup"; shop.required = mode === "signup"; shop.disabled = mode !== "signup";
   recovery.parentElement!.hidden = mode !== "recover"; recovery.required = mode === "recover"; recovery.disabled = mode !== "recover";
   password.autocomplete = mode === "login" ? "current-password" : "new-password";
   hint.hidden = mode !== "recover";
   button.textContent = mode === "recover" ? "Reset password" : labels[mode]; status.textContent = "";
  }
  for (const value of ["login", "signup", "recover"] as const) {
   const action = document.createElement("button"); action.type = "button"; action.textContent = labels[value];
   action.setAttribute("aria-controls", form.id);
   action.addEventListener("click", () => {
    if (submitting || mode === value) return;
    mode = value; password.value = ""; recovery.value = ""; renderMode(); email.focus?.();
   });
   modeButtons.set(value, action); actions.append(action);
  }
  renderMode(); form.append(hint, status, button);
  section.append(title, actions, description, form); root.replaceChildren(section);
  form.addEventListener("submit", async (event) => {
   event.preventDefault(); if (submitting) return;
   submitting = true; button.disabled = true;
   for (const action of modeButtons.values()) action.disabled = true;
   form.setAttribute("aria-busy", "true"); const submittedMode = mode;
   status.textContent = submittedMode === "login" ? "Signing in…" : submittedMode === "signup" ? "Creating account…" : "Resetting password…";
   try {
    const response = await fetch(`/api/auth/${submittedMode}`, {method: "POST", headers: {"Content-Type": "application/json", "X-ListenLedger-Request": "1"}, body: JSON.stringify({email: email.value, password: password.value, ...(submittedMode === "signup" ? {shop_name: shop.value} : {}), ...(submittedMode === "recover" ? {recovery_code: recovery.value} : {})})});
    const body = await response.json() as AuthResponse & {error?: string};
    if (!response.ok) throw new Error(body.error ?? "Unable to continue");
    password.value = ""; recovery.value = "";
    const finish = (): void => {setShellChromeVisible(true); root.replaceChildren(); resolve(body);};
    if (body.recovery_code) {
     const panel = el("section", "auth-panel");
     panel.append(el("h2", "", "Save your recovery code"), el("p", "", "This code is shown once. Store it privately; it can reset your password. Recovery replaces the previous code and signs out other sessions."), el("code", "recovery-code", body.recovery_code));
     const done = document.createElement("button"); done.type = "button"; done.className = "button button--primary"; done.textContent = "I have saved my code";
     done.addEventListener("click", finish); panel.append(done); root.replaceChildren(panel);
    } else finish();
   } catch (error) {
    status.textContent = error instanceof Error ? error.message : "Connection failed. Try again.";
    submitting = false; button.disabled = false; for (const action of modeButtons.values()) action.disabled = false;
    form.setAttribute("aria-busy", "false");
   }
  });
 });
}

/**
 * Fills the shop/account top bar: the shop the owner is signed in to, the
 * account it belongs to, and the way out of it.
 */
export function addAccountControls(identity: Identity): void {
 const shop = document.querySelector<HTMLElement>('[data-hook="account-shop-name"]');
 if (shop) shop.textContent = identity.shop_name;
 const slot = document.querySelector<HTMLElement>('[data-hook="account-controls"]');
 if (!slot) return;
 const account = el("span", "account-name", identity.email);
 const logout = document.createElement("button"); logout.type = "button";
 logout.className = "btn btn--secondary btn--sm"; logout.textContent = "Sign out";
 logout.addEventListener("click", async () => {
  logout.disabled = true;
  try { const r = await fetch("/api/auth/logout", {method: "POST", headers: {"X-ListenLedger-Request": "1"}}); if (!r.ok) throw new Error(); window.location.reload(); }
  catch {logout.textContent = "Sign out failed — retry"; logout.disabled = false;}
 });
 slot.replaceChildren(account, logout);
}
