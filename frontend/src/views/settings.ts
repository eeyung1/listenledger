/**
 * Settings & Device Management view (route: #/settings).
 *
 * Allows the business owner to configure store details, currency, timezone,
 * recognition review confidence threshold, and sale/restock boundary phrases.
 * Also manages pairing and revoking hardware capture devices.
 */

import {
  evaluateCohortReadiness,
  fetchPilotCohort,
  fetchPilotReconciliations,
  fetchPilotReconciliationDetail,
  fetchSettings,
  fetchTargetGates,
  investigatePilotDiscrepancy,
  pairDevice,
  reconcilePilot,
  revokeDevice,
  rotateDeviceToken,
  updateSettings,
  updateTargetGates,
  type CohortReadinessResponse,
  type Device,
  type IndependentRecordInput,
  type PhysicalStockCountInput,
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
  const recognitionLanguageInput = hook<HTMLInputElement>(root, "setting-recognition-language");
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

  const cohortBadge = hook(root, "cohort-phase-badge");
  const cohortReadinessBadge = hook(root, "cohort-readiness-badge");
  const cohortChecksList = hook(root, "cohort-checks-list");
  const evaluateCohortBtn = hook<HTMLButtonElement>(root, "evaluate-cohort-btn");

  const recForm = hook<HTMLFormElement>(root, "reconciliation-form");
  const recPeriodStart = hook<HTMLInputElement>(root, "rec-period-start");
  const recPeriodEnd = hook<HTMLInputElement>(root, "rec-period-end");
  const recSource = hook<HTMLInputElement>(root, "rec-source");
  const recAuditor = hook<HTMLInputElement>(root, "rec-auditor");
  const recRecordsInput = hook<HTMLTextAreaElement>(root, "rec-records-input");
  const recStockInput = hook<HTMLTextAreaElement>(root, "rec-stock-input");
  const runRecBtn = hook<HTMLButtonElement>(root, "run-reconcile-btn");
  const recStatus = hook(root, "reconcile-status");
  const recList = hook(root, "reconciliation-list");

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
    if (recognitionLanguageInput) recognitionLanguageInput.value = shop.recognition_language ?? "";
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

  async function loadCohort(): Promise<void> {
    try {
      const cohort = await fetchPilotCohort(signal);
      if (disposed || signal.aborted) return;
      if (cohortBadge) {
        let label = "Phase 1: Controlled Trial";
        if (cohort.cohort_phase === "phase2_dual_shop") label = "Phase 2: 1–2 Shops";
        else if (cohort.cohort_phase === "phase3_expanded_5") label = "Phase 3: 5 Shops";
        else if (cohort.cohort_phase === "phase4_scaled_15") label = "Phase 4: 10–15 Shops";

        setText(cohortBadge, label);
        cohortBadge.className = `badge badge--${cohort.stage_status === "qualified" ? "confirmed" : "review"}`;
      }

      const readiness = await evaluateCohortReadiness(false);
      if (disposed || signal.aborted) return;
      renderCohortReadiness(readiness);
    } catch {
      // Ignored if view unmounted
    }
  }

  function renderCohortReadiness(r: CohortReadinessResponse): void {
    if (cohortReadinessBadge) {
      cohortReadinessBadge.className = `badge badge--${r.readiness_status === "qualified" ? "confirmed" : "review"}`;
      setText(cohortReadinessBadge, r.readiness_status === "qualified" ? "Qualified to Expand" : "Expansion Blocked");
    }
    if (cohortChecksList) {
      clear(cohortChecksList);
      for (const c of r.checks) {
        const item = el("li", "cohort-check-item");
        const icon = el("span", `check-icon check-icon--${c.satisfied ? "pass" : "fail"}`, c.satisfied ? "✓" : "✗");
        const reqText = el("strong", "check-name", c.requirement);
        const detail = el("span", "check-detail", ` — ${c.details}`);
        item.append(icon, reqText, detail);
        cohortChecksList.append(item);
      }
    }
  }

  async function loadReconciliations(): Promise<void> {
    if (!recList) return;
    try {
      const res = await fetchPilotReconciliations(signal);
      if (disposed || signal.aborted) return;
      clear(recList);

      if (res.reconciliations.length === 0) {
        recList.append(el("li", "reconciliation-list__empty", "No reconciliation audits performed yet."));
        return;
      }

      for (const rec of res.reconciliations) {
        const item = el("li", "reconciliation-item");
        const header = el("div", "reconciliation-item__header");
        const title = el("strong", "reconciliation-item__title", `${rec.independent_source} (${formatDate(rec.created_at)})`);
        const statusBadge = el("span", `badge badge--${rec.status === "reconciled" ? "confirmed" : "rejected"}`, rec.status === "reconciled" ? "Reconciled" : "Discrepant");
        header.append(title, statusBadge);

        const summary = el(
          "p",
          "reconciliation-item__summary",
          `Audited by ${rec.conducted_by} · Matched: ${rec.matched_records}/${rec.total_independent_records} · Missed: ${rec.missed_captures} · False: ${rec.false_captures} · Rev Discrepancy: NGN ${(rec.revenue_discrepancy / 100).toLocaleString()} · Stock Discrepancies: ${rec.stock_discrepancy_count}`,
        );

        item.append(header, summary);

        if (rec.status !== "reconciled") {
          const detailBtn = el("button", "button button--secondary button--sm", "Inspect & Resolve Discrepancies");
          detailBtn.addEventListener("click", async () => {
            detailBtn.disabled = true;
            try {
              const full = await fetchPilotReconciliationDetail(rec.id);
              if (full.discrepancies && full.discrepancies.length > 0) {
                const disc = full.discrepancies.find((d) => d.investigation_status === "unexplained") ?? full.discrepancies[0];
                const cause = prompt(
                  `Discrepancy: ${disc.discrepancy_type} for ${disc.product_name}.\nRoot cause options:\nacoustic_noise, speech_boundary_miss, cashier_protocol_drift, customer_cancellation, physical_shrinkage, supplier_shortage, other\n\nEnter root cause:`,
                  "acoustic_noise",
                );
                if (!cause) {
                  detailBtn.disabled = false;
                  return;
                }
                const notes = prompt("Enter investigation audit findings:", "Investigated discrepancy in trading session");
                if (notes) {
                  await investigatePilotDiscrepancy(disc.id, {
                    root_cause: cause.trim(),
                    investigation_notes: notes.trim(),
                  });
                  alert("Discrepancy root cause recorded successfully!");
                  await loadReconciliations();
                  await loadCohort();
                }
              }
            } catch (err: unknown) {
              alert(err instanceof Error ? err.message : "Failed to fetch discrepancy detail");
            } finally {
              detailBtn.disabled = false;
            }
          });
          item.append(detailBtn);
        }

        recList.append(item);
      }
    } catch {
      // Ignored on abort
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
      await loadCohort();
      await loadReconciliations();
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
        recognition_language: recognitionLanguageInput?.value.trim(),
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

  const onRunReconcile = async (e: Event): Promise<void> => {
    e.preventDefault();
    if (!recForm || !runRecBtn) return;

    let independentRecords: IndependentRecordInput[] = [];
    if (recRecordsInput && recRecordsInput.value.trim()) {
      try {
        independentRecords = JSON.parse(recRecordsInput.value.trim());
        if (!Array.isArray(independentRecords)) {
          throw new Error("Independent records must be a JSON array");
        }
      } catch (err: unknown) {
        if (recStatus) {
          recStatus.hidden = false;
          recStatus.textContent = err instanceof Error ? `Invalid records JSON: ${err.message}` : "Invalid records JSON";
          recStatus.className = "form__status form__status--error";
        }
        return;
      }
    }

    let physicalStock: PhysicalStockCountInput[] = [];
    if (recStockInput && recStockInput.value.trim()) {
      try {
        physicalStock = JSON.parse(recStockInput.value.trim());
        if (!Array.isArray(physicalStock)) {
          throw new Error("Physical stock counts must be a JSON array");
        }
      } catch (err: unknown) {
        if (recStatus) {
          recStatus.hidden = false;
          recStatus.textContent = err instanceof Error ? `Invalid stock JSON: ${err.message}` : "Invalid stock JSON";
          recStatus.className = "form__status form__status--error";
        }
        return;
      }
    }

    const periodStartVal = recPeriodStart?.value ? new Date(recPeriodStart.value).toISOString() : new Date(Date.now() - 3600000).toISOString();
    const periodEndVal = recPeriodEnd?.value ? new Date(recPeriodEnd.value).toISOString() : new Date().toISOString();
    const sourceVal = recSource?.value.trim() || "Paper Counter Register";
    const auditorVal = recAuditor?.value.trim() || "Shop Owner & Auditor";

    runRecBtn.disabled = true;
    if (recStatus) {
      recStatus.hidden = false;
      recStatus.textContent = "Running dual-entry reconciliation audit…";
      recStatus.className = "form__status";
    }

    try {
      const res = await reconcilePilot({
        period_start: periodStartVal,
        period_end: periodEndVal,
        independent_source: sourceVal,
        independent_records: independentRecords,
        physical_stock_counts: physicalStock,
        conducted_by: auditorVal,
      });

      if (recStatus) {
        const passedText = res.all_gates_passed ? "All target gates passed!" : "Discrepancies flagged for investigation.";
        recStatus.textContent = `Audit completed: ${res.reconciliation.status}. ${passedText}`;
        recStatus.className = `form__status ${res.all_gates_passed ? "form__status--success" : "form__status--error"}`;
      }

      await loadReconciliations();
      await loadCohort();
    } catch (err: unknown) {
      if (recStatus) {
        recStatus.textContent = err instanceof Error ? err.message : "Reconciliation failed";
        recStatus.className = "form__status form__status--error";
      }
    } finally {
      runRecBtn.disabled = false;
    }
  };

  const onEvaluateCohort = async (): Promise<void> => {
    if (!evaluateCohortBtn) return;
    evaluateCohortBtn.disabled = true;
    try {
      const readiness = await evaluateCohortReadiness(true);
      renderCohortReadiness(readiness);
      await loadCohort();
      if (readiness.readiness_status === "qualified") {
        alert(`Cohort promoted! Now in phase: ${readiness.next_phase}`);
      } else {
        alert("Expansion blocked: Not all checklist criteria are satisfied.");
      }
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Failed to evaluate readiness");
    } finally {
      evaluateCohortBtn.disabled = false;
    }
  };

  rangeInput?.addEventListener("input", onThresholdChange);
  settingsForm?.addEventListener("submit", onSaveSettings);
  pairForm?.addEventListener("submit", onPairDevice);
  gatesForm?.addEventListener("submit", onSaveGates);
  recForm?.addEventListener("submit", onRunReconcile);
  evaluateCohortBtn?.addEventListener("click", onEvaluateCohort);
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
    recForm?.removeEventListener("submit", onRunReconcile);
    evaluateCohortBtn?.removeEventListener("click", onEvaluateCohort);
    closeTokenBtn?.removeEventListener("click", onCloseToken);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    window.removeEventListener("ll:settings-updated", onSettingsUpdated);
    window.removeEventListener("ll:target-gates-updated", onSettingsUpdated);
  };
}

