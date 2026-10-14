/**
 * Settings & Device Management view (route: #/settings).
 *
 * Allows the business owner to configure store details, currency, timezone,
 * recognition review confidence threshold, and sale/restock boundary phrases.
 * Also manages pairing and revoking hardware capture devices.
 */

import {
  fetchSettings,
  fetchTargetGates,
  pairDevice,
  revokeDevice,
  rotateDeviceToken,
  updateSettings,
  updateTargetGates,
  type Device,
  type ShopSettings,
  type TargetGates,
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

  const gatesBadge = hook(root, "gates-agreement-badge");
  const gatesForm = hook<HTMLFormElement>(root, "target-gates-form");
  const fullAccInput = hook<HTMLInputElement>(root, "gate-full-acc");
  const skuAccInput = hook<HTMLInputElement>(root, "gate-sku-acc");
  const qtyAccInput = hook<HTMLInputElement>(root, "gate-qty-acc");
  const unitAccInput = hook<HTMLInputElement>(root, "gate-unit-acc");
  const amtAccInput = hook<HTMLInputElement>(root, "gate-amt-acc");
  const falseCapInput = hook<HTMLInputElement>(root, "gate-false-cap");
  const missedTxnsInput = hook<HTMLInputElement>(root, "gate-missed-txns");
  const correctionRateInput = hook<HTMLInputElement>(root, "gate-correction-rate");
  const protocolCompInput = hook<HTMLInputElement>(root, "gate-protocol-comp");
  const p95LatencyInput = hook<HTMLInputElement>(root, "gate-p95-latency");
  const gatesMetaInfo = hook(root, "gates-meta-info");
  const gatesMetaText = hook(root, "gates-meta-text");
  const saveGatesBtn = hook<HTMLButtonElement>(root, "save-gates-btn");
  const gatesStatus = hook(root, "gates-status");

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

  async function loadTargetGates(): Promise<void> {
    try {
      const res = await fetchTargetGates(signal);
      if (disposed || signal.aborted) return;
      const g = res.gates;
      if (fullAccInput) fullAccInput.value = g.min_full_transaction_accuracy.toString();
      if (skuAccInput) skuAccInput.value = g.min_sku_accuracy.toString();
      if (qtyAccInput) qtyAccInput.value = g.min_quantity_accuracy.toString();
      if (unitAccInput) unitAccInput.value = g.min_unit_accuracy.toString();
      if (amtAccInput) amtAccInput.value = g.min_amount_accuracy.toString();
      if (falseCapInput) falseCapInput.value = g.max_false_capture_rate.toString();
      if (missedTxnsInput) missedTxnsInput.value = g.max_missed_transaction_rate.toString();
      if (correctionRateInput) correctionRateInput.value = g.max_correction_rate.toString();
      if (protocolCompInput) protocolCompInput.value = g.min_protocol_compliance_rate.toString();
      if (p95LatencyInput) p95LatencyInput.value = g.max_p95_latency_ms.toString();

      if (gatesBadge) {
        gatesBadge.className = `badge badge--${res.is_agreed ? "confirmed" : "review"}`;
        setText(gatesBadge, res.is_agreed ? "Agreed by Owner" : "Pending Agreement");
      }
      if (gatesMetaInfo && gatesMetaText) {
        if (res.is_agreed && g.agreed_at) {
          gatesMetaInfo.hidden = false;
          setText(gatesMetaText, `Formally agreed on ${formatDate(g.agreed_at)}${g.agreed_by ? ` by ${g.agreed_by}` : ""}.`);
        } else {
          gatesMetaInfo.hidden = true;
        }
      }
    } catch {
      // Ignored if abort or view unmounted
    }
  }

  async function loadSettings(): Promise<void> {
    try {
      const data = await fetchSettings(shopId, signal);
      if (disposed || signal.aborted) return;
      devices = [...data.devices];
      populateForm(data.shop);
      renderDevices();
      await loadTargetGates();
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

  const onSaveGates = async (e: Event): Promise<void> => {
    e.preventDefault();
    if (saveGatesBtn) saveGatesBtn.disabled = true;
    if (gatesStatus) {
      gatesStatus.hidden = false;
      gatesStatus.textContent = "Saving agreement…";
      gatesStatus.className = "form__status";
    }

    try {
      const payload: Partial<TargetGates> = {
        min_full_transaction_accuracy: parseFloat(fullAccInput?.value ?? "90"),
        min_sku_accuracy: parseFloat(skuAccInput?.value ?? "95"),
        min_quantity_accuracy: parseFloat(qtyAccInput?.value ?? "95"),
        min_unit_accuracy: parseFloat(unitAccInput?.value ?? "95"),
        min_amount_accuracy: parseFloat(amtAccInput?.value ?? "95"),
        max_false_capture_rate: parseFloat(falseCapInput?.value ?? "3"),
        max_missed_transaction_rate: parseFloat(missedTxnsInput?.value ?? "5"),
        max_correction_rate: parseFloat(correctionRateInput?.value ?? "10"),
        min_protocol_compliance_rate: parseFloat(protocolCompInput?.value ?? "85"),
        max_p95_latency_ms: parseFloat(p95LatencyInput?.value ?? "1500"),
      };

      const res = await updateTargetGates(payload);
      if (gatesBadge) {
        gatesBadge.className = "badge badge--confirmed";
        setText(gatesBadge, "Agreed by Owner");
      }
      if (gatesMetaInfo && gatesMetaText && res.gates.agreed_at) {
        gatesMetaInfo.hidden = false;
        setText(
          gatesMetaText,
          `Formally agreed on ${formatDate(res.gates.agreed_at)}${res.gates.agreed_by ? ` by ${res.gates.agreed_by}` : ""}.`,
        );
      }
      if (gatesStatus) {
        gatesStatus.textContent = "Target gates successfully agreed!";
        gatesStatus.className = "form__status form__status--success";
        setTimeout(() => {
          if (!disposed && gatesStatus) gatesStatus.hidden = true;
        }, 3000);
      }
    } catch (err: unknown) {
      if (gatesStatus) {
        gatesStatus.textContent = err instanceof Error ? err.message : "Failed to save target gates";
        gatesStatus.className = "form__status form__status--error";
      }
    } finally {
      if (saveGatesBtn) saveGatesBtn.disabled = false;
    }
  };

  const onSettingsUpdated = (): void => {
    void loadSettings();
  };

  rangeInput?.addEventListener("input", onThresholdChange);
  settingsForm?.addEventListener("submit", onSaveSettings);
  pairForm?.addEventListener("submit", onPairDevice);
  gatesForm?.addEventListener("submit", onSaveGates);
  closeTokenBtn?.addEventListener("click", onCloseToken);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  window.addEventListener("ll:settings-updated", onSettingsUpdated);
  window.addEventListener("ll:target-gates-updated", onSettingsUpdated);

  void loadSettings();

  return () => {
    disposed = true;
    abortController.abort();
    rangeInput?.removeEventListener("input", onThresholdChange);
    settingsForm?.removeEventListener("submit", onSaveSettings);
    pairForm?.removeEventListener("submit", onPairDevice);
    gatesForm?.removeEventListener("submit", onSaveGates);
    closeTokenBtn?.removeEventListener("click", onCloseToken);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    window.removeEventListener("ll:settings-updated", onSettingsUpdated);
    window.removeEventListener("ll:target-gates-updated", onSettingsUpdated);
  };
}
