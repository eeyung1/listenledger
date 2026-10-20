import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import { mountSettings } from "../dist/views/settings.js";

const html = fs.readFileSync(new URL("../src/components/settings.html", import.meta.url), "utf8");
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const hook = (root, name) => root.querySelector(`[data-hook="${name}"]`);
const shop = {
  id: "shop_f33_test",
  owner_id: "owner_f33_test",
  name: "Mama Chukwudi Provisions",
  currency: "NGN",
  timezone: "Africa/Lagos",
  confidence_threshold: 0.80,
  sale_phrase: "thank you",
  restock_phrase: "stock received",
  recognition_language: "en-NG",
};
const response = (status, body) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => body,
});

function setup() {
  const { document } = setupDOMEnvironment();
  const root = document.createElement("div");
  root.innerHTML = html;
  document.body.append(root);
  return root;
}

test("F33: Settings is organized into Shop details, Voice settings, Account, and Optional devices", () => {
  const root = setup();

  // 1. Shop details panel
  const shopDetailsSection = root.querySelector('[aria-labelledby="shop-details-heading"]');
  assert.ok(shopDetailsSection, "Shop details section must exist with aria-labelledby='shop-details-heading'");
  const shopDetailsTitle = root.querySelector("#shop-details-heading");
  assert.ok(shopDetailsTitle, "Shop details heading must exist");
  assert.match(shopDetailsTitle.textContent, /Shop details/i);
  assert.ok(hook(shopDetailsSection, "setting-name"), "setting-name input exists in shop details");
  assert.ok(hook(shopDetailsSection, "setting-currency"), "setting-currency input exists in shop details");
  assert.ok(hook(shopDetailsSection, "setting-timezone"), "setting-timezone input exists in shop details");

  // 2. Voice settings panel
  const voiceSettingsSection = root.querySelector('[aria-labelledby="voice-settings-heading"]');
  assert.ok(voiceSettingsSection, "Voice settings section must exist with aria-labelledby='voice-settings-heading'");
  const voiceSettingsTitle = root.querySelector("#voice-settings-heading");
  assert.ok(voiceSettingsTitle, "Voice settings heading must exist");
  assert.match(voiceSettingsTitle.textContent, /Voice settings/i);
  assert.ok(hook(voiceSettingsSection, "setting-sale-phrase"), "setting-sale-phrase exists in voice settings");
  assert.ok(hook(voiceSettingsSection, "setting-restock-phrase"), "setting-restock-phrase exists in voice settings");
  assert.ok(hook(voiceSettingsSection, "setting-recognition-language"), "setting-recognition-language exists in voice settings");
  assert.ok(hook(voiceSettingsSection, "save-settings-btn"), "save-settings-btn exists in voice settings");
  assert.ok(hook(voiceSettingsSection, "settings-status"), "settings-status exists in voice settings");

  // 3. Account panel
  const accountSection = root.querySelector('[aria-labelledby="account-heading"]');
  assert.ok(accountSection, "Account section must exist with aria-labelledby='account-heading'");
  const accountTitle = root.querySelector("#account-heading");
  assert.ok(accountTitle, "Account heading must exist");
  assert.match(accountTitle.textContent, /Account/i);
  assert.ok(hook(accountSection, "account-shop-id"), "account-shop-id hook exists in account section");
  assert.ok(hook(accountSection, "account-owner-id"), "account-owner-id hook exists in account section");

  // 4. Optional devices panel
  const devicesSection = root.querySelector('[aria-labelledby="devices-heading"]');
  assert.ok(devicesSection, "Optional devices section must exist with aria-labelledby='devices-heading'");
  const devicesTitle = root.querySelector("#devices-heading");
  assert.ok(devicesTitle, "Devices heading must exist");
  assert.match(devicesTitle.textContent, /Optional devices/i);
  assert.equal(devicesSection.hidden, false, "Optional devices section is not hidden");
  assert.ok(hook(devicesSection, "pair-device-form"), "pair-device-form exists in devices section");
  assert.ok(hook(devicesSection, "device-list"), "device-list exists in devices section");

  // Both Shop details and Voice settings are contained within the main settings-form
  const settingsForm = hook(root, "settings-form");
  assert.ok(settingsForm, "settings-form exists");
  assert.ok(hook(settingsForm, "setting-name"), "setting-name is inside settings-form");
  assert.ok(hook(settingsForm, "setting-sale-phrase"), "setting-sale-phrase is inside settings-form");
  assert.ok(hook(settingsForm, "save-settings-btn"), "save-settings-btn is inside settings-form");
});

test("F35: Browser microphones require no external device pairing and privacy matches speech-processing behaviour", () => {
  const root = setup();

  // Devices panel notice
  const notice = hook(root, "browser-mic-notice");
  assert.ok(notice, "Browser mic notice must exist");
  assert.match(
    notice.textContent,
    /Browser Microphones Require No Device Pairing/i,
    "Notice clearly states browser microphones require no device pairing",
  );
  assert.match(
    notice.textContent,
    /directly inside your browser|captured directly through your browser/i,
    "Notice explains browser-level speech capture",
  );

  // Privacy notice panel
  const privacySection = root.querySelector('[aria-labelledby="privacy-heading"]');
  assert.ok(privacySection, "Privacy section must exist");
  const privacyText = privacySection.textContent;

  assert.match(
    privacyText,
    /Web Speech API/i,
    "Privacy statement notes Web Speech API",
  );
  assert.match(
    privacyText,
    /transcripts/i,
    "Privacy statement notes ListenLedger receives text transcripts",
  );
  assert.match(
    privacyText,
    /No External Pairing Required/i,
    "Privacy statement affirms browser microphones need no external pairing",
  );
  assert.match(
    privacyText,
    /7-day/i,
    "Privacy statement references 7-day scheduled retention deletion",
  );
});

test("F33: mountSettings populates Account identifiers and saves settings updates", async (t) => {
  const root = setup();
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });

  const patches = [];
  globalThis.fetch = async (url, options = {}) => {
    const pathname = new URL(String(url), "http://localhost").pathname;
    if (pathname === "/api/settings") {
      if (options.method === "PATCH") {
        const body = JSON.parse(options.body);
        patches.push(body);
        return response(200, { ...shop, ...body });
      }
      return response(200, { shop, devices: [] });
    }
    if (pathname === "/api/settings/target-gates") {
      return response(200, {
        is_agreed: true,
        gates: {
          min_full_transaction_accuracy: 90,
          min_sku_accuracy: 95,
          min_quantity_accuracy: 95,
          min_unit_accuracy: 95,
          min_amount_accuracy: 95,
          max_false_capture_rate: 3,
          max_missed_transaction_rate: 5,
          max_correction_rate: 10,
          min_protocol_compliance_rate: 85,
          max_p95_latency_ms: 1500,
        },
      });
    }
    if (pathname === "/api/pilot/cohort") {
      return response(200, {
        cohort_phase: "phase1_controlled",
        stage_status: "in_progress",
      });
    }
    if (pathname === "/api/pilot/cohort/readiness") {
      return response(200, {
        readiness_status: "in_progress",
        checks: [],
      });
    }
    if (pathname === "/api/pilot/reconciliations") {
      return response(200, { reconciliations: [] });
    }
    return response(404, { error: "not found" });
  };

  const cleanup = mountSettings(root, shop.id);
  t.after(cleanup);
  await flush();

  // Verify Account fields are populated
  assert.equal(hook(root, "account-shop-id").textContent, shop.id);
  assert.equal(hook(root, "account-owner-id").textContent, shop.owner_id);

  // Verify Shop details are populated
  assert.equal(hook(root, "setting-name").value, shop.name);
  assert.equal(hook(root, "setting-currency").value, shop.currency);
  assert.equal(hook(root, "setting-timezone").value, shop.timezone);

  // Verify Voice settings are populated
  assert.equal(hook(root, "setting-sale-phrase").value, shop.sale_phrase);
  assert.equal(hook(root, "setting-restock-phrase").value, shop.restock_phrase);
  assert.equal(hook(root, "setting-recognition-language").value, shop.recognition_language);

  // Update a field and save
  hook(root, "setting-name").value = "Updated Shop Name";
  hook(root, "setting-sale-phrase").value = "payment received";
  hook(root, "settings-form").dispatchEvent(new MockEvent("submit"));
  await flush();

  assert.equal(patches.length, 1);
  assert.equal(patches[0].name, "Updated Shop Name");
  assert.equal(patches[0].sale_phrase, "payment received");
  assert.match(hook(root, "settings-status").textContent, /saved successfully/i);
});
