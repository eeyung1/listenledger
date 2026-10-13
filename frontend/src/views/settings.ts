/**
 * Settings & Device Management view (route: #/settings).
 *
 * Allows the business owner to configure store details, currency, timezone,
 * recognition review confidence threshold, and sale/restock boundary phrases.
 * Also manages pairing and revoking hardware capture devices.
 */

import {
  fetchSettings,
  pairDevice,
  revokeDevice,
  rotateDeviceToken,
  updateSettings,
  type Device,
  type ShopSettings,
} from "../api.js";
import { clear, el, formatDate, hook, setText } from "../format.js";
import { CONNECTION_STATUS_EVENT, type ConnectionStatusDetail } from "../ws.js";
import type { ViewCleanup } from "./index.js";

export function mountSettings(root: HTMLElement, shopId: string): ViewCleanup {
  let disposed = false;
  const abortController = new AbortController();
  const signal = abortController.signal;
  let devices: Device[] = [];

  const nameInput = hook<HTMLInputElement>(root, "setting-name");
  const currInput = hook<HTMLInputElement>(root, "setting-currency");
  const tzInput = hook<HTMLInputElement>(root, "setting-timezone");
  const rangeInput = hook<HTMLInputElement>(root, "setting-threshold-range");
  const rangeVal = hook(root, "setting-threshold-val");
  const salePhraseInput = hook<HTMLInputElement>(root, "setting-sale-phrase");
  const restockPhraseInput = hook<HTMLInputElement>(root, "setting-restock-phrase");
  const settingsForm = hook<HTMLFormElement>(root, "settings-form");
  const settingsStatus = hook(root, "settings-status");
  const saveBtn = hook<HTMLButtonElement>(root, "save-settings-btn");

  const pairForm = hook<HTMLFormElement>(root, "pair-device-form");
  const deviceNameInput = hook<HTMLInputElement>(root, "device-name-input");
  const pairBtn = hook<HTMLButtonElement>(root, "pair-device-btn");
  const deviceList = hook(root, "device-list");
  const tokenCard = hook(root, "device-token-card");
  const tokenVal = hook(root, "device-token-value");
  const closeTokenBtn = hook(root, "close-token-card");

  function renderDevices(): void {
    if (deviceList === null) return;
    clear(deviceList);

    if (devices.length === 0) {
      deviceList.append(el("li", "device-list__empty", "No edge capture devices paired yet."));
      return;
    }

    for (const dev of devices) {
      const item = el("li", "device-item");
      const isRevoked = dev.revoked_at !== null && dev.revoked_at !== undefined;

      const info = el("div", "device-item__info");
      const name = el("strong", "device-item__name", dev.name);
      const meta = el(
        "span",
        "device-item__meta",
        `Paired: ${formatDate(dev.paired_at)}${isRevoked ? ` · Revoked: ${formatDate(dev.revoked_at!)}` : ""}`,
      );
      info.append(name, meta);

      const statusBadge = el(
        "span",
        `badge badge--${isRevoked ? "rejected" : "confirmed"}`,
        isRevoked ? "Revoked" : "Active",
      );

      const actions = el("div", "device-item__actions");
      actions.append(statusBadge);

      if (!isRevoked) {
        const rotateBtn = el("button", "button button--secondary button--sm", "Rotate Token");
        rotateBtn.addEventListener("click", async () => {
          if (!confirm(`Rotate credentials for device "${dev.name}"? The existing token will immediately become invalid.`)) {
            return;
          }
          rotateBtn.disabled = true;
          try {
            const res = await rotateDeviceToken(dev.id);
            if (tokenCard && tokenVal) {
              setText(tokenVal, res.token);
              tokenCard.hidden = false;
            }
            await loadSettings();
          } catch (err: unknown) {
            rotateBtn.disabled = false;
            alert(err instanceof Error ? err.message : "Failed to rotate device token");
          }
        });

        const revokeBtn = el("button", "button button--danger button--sm", "Revoke");
        revokeBtn.addEventListener("click", async () => {
          if (!confirm(`Revoke access for device "${dev.name}"? It will no longer be able to record captures.`)) {
            return;
          }
          revokeBtn.disabled = true;
          try {
            await revokeDevice(dev.id);
            await loadSettings();
          } catch (err: unknown) {
            revokeBtn.disabled = false;
            alert(err instanceof Error ? err.message : "Failed to revoke device");
          }
        });
        actions.append(rotateBtn, revokeBtn);
      }

      item.append(info, actions);
      deviceList.append(item);
    }
  }

  function populateForm(shop: ShopSettings): void {
    if (nameInput) nameInput.value = shop.name;
    if (currInput) currInput.value = shop.currency;
    if (tzInput) tzInput.value = shop.timezone;
    if (rangeInput) rangeInput.value = shop.confidence_threshold.toFixed(2);
    if (rangeVal) setText(rangeVal, `${Math.round(shop.confidence_threshold * 100)}%`);
    if (salePhraseInput) salePhraseInput.value = shop.sale_phrase;
    if (restockPhraseInput) restockPhraseInput.value = shop.restock_phrase;
  }

  async function loadSettings(): Promise<void> {
    try {
      const data = await fetchSettings(shopId, signal);
      if (disposed || signal.aborted) return;
      devices = [...data.devices];
      populateForm(data.shop);
      renderDevices();
    } catch (err: unknown) {
      if (disposed || signal.aborted) return;
      console.error("Failed to load settings:", err);
      if (settingsStatus) {
        settingsStatus.hidden = false;
        settingsStatus.textContent = "Could not load settings. Retrying on reconnect.";
      }
    }
  }

  const onThresholdChange = (): void => {
    if (rangeInput && rangeVal) {
      const val = parseFloat(rangeInput.value);
      setText(rangeVal, `${Math.round(val * 100)}%`);
    }
  };

  const onSaveSettings = async (e: Event): Promise<void> => {
    e.preventDefault();
    if (!settingsForm || !saveBtn || !settingsStatus) return;

    saveBtn.disabled = true;
    settingsStatus.hidden = false;
    settingsStatus.textContent = "Saving…";

    try {
      const updated = await updateSettings({
        name: nameInput?.value.trim(),
        currency: currInput?.value.trim().toUpperCase(),
        timezone: tzInput?.value.trim(),
        confidence_threshold: rangeInput ? parseFloat(rangeInput.value) : undefined,
        sale_phrase: salePhraseInput?.value.trim(),
        restock_phrase: restockPhraseInput?.value.trim(),
      });
      populateForm(updated);
      settingsStatus.textContent = "Settings saved successfully.";
      setTimeout(() => {
        if (!disposed && settingsStatus) settingsStatus.hidden = true;
      }, 3000);
    } catch (err: unknown) {
      settingsStatus.textContent = err instanceof Error ? err.message : "Save failed";
    } finally {
      saveBtn.disabled = false;
    }
  };

  const onPairDevice = async (e: Event): Promise<void> => {
    e.preventDefault();
    if (!deviceNameInput || !pairBtn) return;
    const devName = deviceNameInput.value.trim();
    if (!devName) return;

    pairBtn.disabled = true;
    try {
      const res = await pairDevice(devName);
      deviceNameInput.value = "";
      if (tokenCard && tokenVal) {
        setText(tokenVal, res.token);
        tokenCard.hidden = false;
      }
      await loadSettings();
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Pairing failed");
    } finally {
      pairBtn.disabled = false;
    }
  };

  const onCloseToken = (): void => {
    if (tokenCard) tokenCard.hidden = true;
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    if (detail.resyncRequired) {
      void loadSettings();
    }
  };

  const onSettingsUpdated = (): void => {
    void loadSettings();
  };

  rangeInput?.addEventListener("input", onThresholdChange);
  settingsForm?.addEventListener("submit", onSaveSettings);
  pairForm?.addEventListener("submit", onPairDevice);
  closeTokenBtn?.addEventListener("click", onCloseToken);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  window.addEventListener("ll:settings-updated", onSettingsUpdated);

  void loadSettings();

  return () => {
    disposed = true;
    abortController.abort();
    rangeInput?.removeEventListener("input", onThresholdChange);
    settingsForm?.removeEventListener("submit", onSaveSettings);
    pairForm?.removeEventListener("submit", onPairDevice);
    closeTokenBtn?.removeEventListener("click", onCloseToken);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    window.removeEventListener("ll:settings-updated", onSettingsUpdated);
  };
}
