import { el } from "./format.js";

export interface Identity { owner_id: string; email: string; shop_id: string; shop_name: string; currency: string; timezone: string }
interface AuthResponse extends Identity { recovery_code?: string }

export async function requireIdentity(): Promise<Identity> {
 const session = await fetch("/api/auth/session");
 if (session.ok) return await session.json() as Identity;
 if (session.status !== 401) throw new Error("Sign-in service unavailable. Please reload to retry.");
 return new Promise<Identity>((resolve) => {
  const root = document.getElementById("app")!;
  const nav = document.querySelector<HTMLElement>(".app-nav"); if (nav) nav.hidden = true;
  const section = el("section", "auth-panel");
  const title = el("h1", "", "Welcome to ListenLedger");
  const form = document.createElement("form");
  const mode = document.createElement("select");
  mode.setAttribute("aria-label", "Account action");
  for (const [value, text] of [["login", "Sign in"], ["signup", "Create account"], ["recover", "Recover account"]]) {
   const option = document.createElement("option"); option.value = value!; option.textContent = text!; mode.append(option);
  }
  form.append(mode);
  function field(label: string, type: string, autocomplete: string): HTMLInputElement {
   const wrapper = document.createElement("label"); wrapper.textContent = label;
   const input = document.createElement("input"); input.type = type; input.autocomplete = autocomplete as AutoFill; input.required = true;
   wrapper.append(input); form.append(wrapper); return input;
  }
  const email = field("Email", "email", "email");
  const password = field("Password (12–128 characters)", "password", "current-password"); password.minLength = 12; password.maxLength = 128;
  const shop = field("Shop name", "text", "organization"); shop.maxLength = 120;
  const recovery = field("Recovery code", "password", "off");
  const hint = el("p", "", "Recovery uses the private code saved when you created your account. Keep it somewhere safe; email recovery is not configured.");
  const status = el("p", ""); status.setAttribute("role", "status");
  const button = document.createElement("button"); button.type = "submit"; button.textContent = "Continue";
  function changeMode(): void {
   shop.parentElement!.hidden = mode.value !== "signup"; shop.required = mode.value === "signup";
   recovery.parentElement!.hidden = mode.value !== "recover"; recovery.required = mode.value === "recover";
   password.autocomplete = mode.value === "login" ? "current-password" : "new-password";
   status.textContent = "";
  }
  mode.addEventListener("change", changeMode); changeMode();
  form.append(hint, status, button); section.append(title, form); root.replaceChildren(section);
  form.addEventListener("submit", async (event) => {
   event.preventDefault(); button.disabled = true; status.textContent = "Please wait…";
   try {
    const response = await fetch(`/api/auth/${mode.value}`, {method: "POST", headers: {"Content-Type": "application/json", "X-ListenLedger-Request": "1"}, body: JSON.stringify({email: email.value, password: password.value, ...(mode.value === "signup" ? {shop_name: shop.value} : {}), ...(mode.value === "recover" ? {recovery_code: recovery.value} : {})})});
    const body = await response.json() as AuthResponse & {error?: string};
    if (!response.ok) throw new Error(body.error ?? "Unable to continue");
    password.value = ""; recovery.value = "";
    const finish = (): void => {if (nav) nav.hidden = false; root.replaceChildren(); resolve(body);};
    if (body.recovery_code) {
     const panel = el("section", "auth-panel");
     panel.append(el("h2", "", "Save your recovery code"), el("p", "", "This code is shown once. Store it privately; it can reset your password. Recovery replaces the previous code and signs out other sessions."), el("code", "recovery-code", body.recovery_code));
     const done = document.createElement("button"); done.textContent = "I have saved my code"; done.addEventListener("click", finish); panel.append(done); root.replaceChildren(panel);
    } else finish();
   } catch (error) {status.textContent = error instanceof Error ? error.message : "Connection failed. Try again."; button.disabled = false;}
  });
 });
}
export function addAccountControls(identity: Identity): void {
 const header = document.querySelector(".app-header");
 if (!header) return;
 const name = el("span", "account-name", identity.shop_name);
 const logout = document.createElement("button"); logout.textContent = "Sign out";
 logout.addEventListener("click", async () => {
  logout.disabled = true;
  try { const r = await fetch("/api/auth/logout", {method: "POST", headers: {"X-ListenLedger-Request": "1"}}); if (!r.ok) throw new Error(); window.location.reload(); }
  catch {logout.textContent = "Sign out failed — retry"; logout.disabled = false;}
 });
 header.append(name, logout);
}
