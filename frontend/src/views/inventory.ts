/**
 * Inventory & Catalogue Management (route: #/inventory).
 *
 * The catalogue's source of truth: official names, SKUs, prices, stock on hand,
 * and the local aliases each product is actually called — the mechanism by
 * which recognition improves without retraining the speech model.
 *
 * Search is client-side, as on the transactions view: the pilot catalogue is
 * 20-50 products and the snapshot is fetched whole, so filtering here is
 * cheaper than a round trip per keystroke.
 */

import {
  adjustProductStock,
  archiveProduct,
  createProduct,
  fetchInventory,
  fetchProductMovements,
  importCatalogue,
  unarchiveProduct,
  updateProduct,
  ALL_UNITS,
  type CreateProductInput,
  type PackagingRelation,
  type Product,
  type Unit,
  type UpdateProductInput,
} from "../api.js";
import { setCachedCatalogue } from "../offline_catalogue.js";
import {
  cell,
  clear,
  el,
  formatDate,
  formatMoney,
  formatTime,
  formatUnit,
  hook,
  isLowStock,
  setText,
} from "../format.js";
import { CONNECTION_STATUS_EVENT, getSocket, type ConnectionStatusDetail } from "../ws.js";
import { uiStateRow, type UiStateOptions } from "../ui_state.js";
import type { ViewCleanup } from "./index.js";

type CatalogueTab = "active" | "archived" | "all";

/** Units a fresh packaging row offers first, in the order shops usually speak. */
const PACKAGING_SUGGESTIONS: readonly Unit[] = ["carton", "crate", "pack", "bottle", "bag"];

/**
 * Checks a packaging chain against the rules the API enforces
 * (models.ValidatePackaging), so the form can explain the problem instead of
 * relaying "invalid or unsupported packaging conversion" from a 422.
 *
 * Returns a sentence for the owner, or null when the chain is usable.
 */
export function packagingProblem(
  base: Unit,
  relations: readonly PackagingRelation[],
): string | null {
  const byFrom = new Map<Unit, PackagingRelation>();
  for (const relation of relations) {
    if (relation.from === relation.to) {
      return `The larger and smaller unit are both ${formatUnit(relation.from, 1)}. Pick two different units.`;
    }
    if (relation.from === base) {
      return `The base unit (${formatUnit(base, 1)}) cannot contain itself. Put a larger unit on the left.`;
    }
    if (!Number.isInteger(relation.factor) || relation.factor < 1) {
      return "Packaging needs a whole number of the smaller unit, at least 1.";
    }
    if (byFrom.has(relation.from)) {
      return `Each larger unit can be mapped once, but ${formatUnit(relation.from, 1)} appears twice.`;
    }
    byFrom.set(relation.from, relation);
  }

  // Each chain has to reach the base unit, or stock held in the larger unit has
  // no way to become stock the ledger counts.
  for (const start of byFrom.keys()) {
    let unit = start;
    const seen = new Set<Unit>();
    while (unit !== base) {
      if (seen.has(unit)) {
        return "This packaging loops back on itself. Each chain has to end at the base unit.";
      }
      seen.add(unit);
      const relation = byFrom.get(unit);
      if (relation === undefined) {
        return `"${formatUnit(unit, 2)}" is never converted to the base unit (${formatUnit(base, 1)}). Add a row for it or remove it.`;
      }
      unit = relation.to;
    }
  }
  return null;
}

export function mountInventory(root: HTMLElement, shopId: string): ViewCleanup {
  let products: Product[] = [];
  let query = "";
  let currentTab: CatalogueTab = "active";
  let disposed = false;
  const abortController = new AbortController();
  const signal = abortController.signal;

  const body = hook(root, "catalogue-body");
  const countLabel = hook(root, "inventory-count");
  const searchInput = hook<HTMLInputElement>(root, "catalogue-query");
  const addButton = hook<HTMLButtonElement>(root, "catalogue-add");
  const importButton = hook<HTMLButtonElement>(root, "catalogue-import");
  const tabButtons = root.querySelectorAll<HTMLButtonElement>("[data-hook='catalogue-tabs'] .tab");

  function onTabClick(event: MouseEvent): void {
    const target = event.currentTarget as HTMLButtonElement | null;
    const tabName = target?.getAttribute("data-tab") as CatalogueTab | null;
    if (!tabName) return;
    currentTab = tabName;
    tabButtons.forEach((btn) => {
      const isActive = btn.getAttribute("data-tab") === currentTab;
      btn.classList.toggle("is-active", isActive);
      btn.setAttribute("aria-selected", isActive ? "true" : "false");
    });
    void refetch();
  }

  tabButtons.forEach((btn) => {
    btn.addEventListener("click", onTabClick as EventListener);
  });

  function showProductModal(existing?: Product): void {
    const isEdit = existing !== undefined;
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", isEdit ? `Edit ${existing.name}` : "Add Product");
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");

    const nameLabel = el("label", "form-field", "Product Name *");
    const nameInput = el("input", "input") as HTMLInputElement;
    nameInput.type = "text";
    nameInput.required = true;
    nameInput.value = existing?.name ?? "";
    nameLabel.append(nameInput);

    const skuLabel = el("label", "form-field", "SKU *");
    const skuInput = el("input", "input") as HTMLInputElement;
    skuInput.type = "text";
    skuInput.required = true;
    skuInput.value = existing?.sku ?? "";
    skuLabel.append(skuInput);

    const priceLabel = el("label", "form-field", "Price (NGN) *");
    const priceInput = el("input", "input") as HTMLInputElement;
    priceInput.type = "number";
    priceInput.step = "0.01";
    priceInput.min = "0";
    priceInput.required = true;
    priceInput.value = existing ? String(existing.price.amount / 100) : "";
    priceLabel.append(priceInput);

    const unitLabel = el("label", "form-field", "Base Unit *");
    const unitSelect = el("select", "select") as HTMLSelectElement;
    for (const u of ALL_UNITS) {
      const opt = el("option", "", u) as HTMLOptionElement;
      opt.value = u;
      if (existing && existing.base_unit === u) opt.selected = true;
      unitSelect.append(opt);
    }
    unitLabel.append(unitSelect);

    const qtyLabel = el("label", "form-field", isEdit ? "Stock (movements adjust stock)" : "Opening Stock *");
    const qtyInput = el("input", "input") as HTMLInputElement;
    qtyInput.type = "number";
    qtyInput.min = "0";
    qtyInput.value = existing ? String(existing.quantity) : "0";
    if (isEdit) {
      qtyInput.disabled = true;
    }
    qtyLabel.append(qtyInput);

    const aliasesLabel = el("label", "form-field", "Aliases (comma-separated)");
    const aliasesInput = el("input", "input") as HTMLInputElement;
    aliasesInput.type = "text";
    aliasesInput.placeholder = "e.g. Big Milo, Peak 400g";
    aliasesInput.value = existing ? existing.aliases.join(", ") : "";
    aliasesLabel.append(aliasesInput);

    const lowStockLabel = el("label", "form-field", "Reorder Threshold");
    const lowStockInput = el("input", "input") as HTMLInputElement;
    lowStockInput.type = "number";
    lowStockInput.min = "0";
    lowStockInput.value = existing && existing.low_stock_threshold !== undefined ? String(existing.low_stock_threshold) : "5";
    lowStockLabel.append(lowStockInput);

    // Packaging is how the shop says the product out loud ("two cartons") and
    // what turns a spoken unit into base units. The catalogue already lists it,
    // but without a field here a carton later found to hold 24 could only be
    // corrected by re-importing the whole catalogue.
    const packagingField = el("div", "form-field packaging-field");
    packagingField.setAttribute("role", "group");
    packagingField.setAttribute("aria-label", "Packaging");
    const packagingLabel = el("span", "form-label", "Packaging");
    const packagingHelp = el(
      "p",
      "modal-help",
      "Larger units this product is sold in, e.g. 1 carton = 12 pack. Leave empty when it is only sold by the base unit.",
    );
    const packagingRows = el("div", "packaging-rows");
    const addPackagingBtn = el("button", "button button--secondary button--sm", "Add packaging");
    addPackagingBtn.type = "button";

    const unitPicker = (value: Unit): HTMLSelectElement => {
      const select = el("select", "select") as HTMLSelectElement;
      for (const unit of ALL_UNITS) {
        const option = el("option", "", unit) as HTMLOptionElement;
        option.value = unit;
        option.selected = unit === value;
        select.append(option);
      }
      return select;
    };

    const addPackagingRow = (relation?: PackagingRelation): void => {
      const base = unitSelect.value as Unit;
      const suggested =
        PACKAGING_SUGGESTIONS.find((unit) => unit !== base) ??
        ALL_UNITS.find((unit) => unit !== base) ??
        "carton";

      const from = unitPicker(relation?.from ?? suggested);
      from.setAttribute("data-role", "packaging-from");
      from.setAttribute("aria-label", "Larger unit");

      const factor = el("input", "input") as HTMLInputElement;
      factor.type = "number";
      factor.min = "1";
      factor.step = "1";
      factor.value = relation ? String(relation.factor) : "12";
      factor.setAttribute("data-role", "packaging-factor");
      factor.setAttribute("aria-label", "How many smaller units one larger unit holds");

      const to = unitPicker(relation?.to ?? base);
      to.setAttribute("data-role", "packaging-to");
      to.setAttribute("aria-label", "Smaller unit");

      const removeBtn = el("button", "button button--secondary button--sm", "Remove");
      removeBtn.type = "button";

      const row = el("div", "packaging-row");
      removeBtn.addEventListener("click", () => {
        row.remove();
      });
      row.append(from, el("span", "packaging-row__equals", "="), factor, el("span", "packaging-row__times", "×"), to, removeBtn);
      packagingRows.append(row);
    };

    const readPackaging = (): PackagingRelation[] => {
      const relations: PackagingRelation[] = [];
      packagingRows.querySelectorAll(".packaging-row").forEach((row) => {
        const from = row.querySelector('[data-role="packaging-from"]') as HTMLSelectElement | null;
        const to = row.querySelector('[data-role="packaging-to"]') as HTMLSelectElement | null;
        const factor = row.querySelector('[data-role="packaging-factor"]') as HTMLInputElement | null;
        if (from === null || to === null || factor === null) return;
        relations.push({
          from: from.value as Unit,
          to: to.value as Unit,
          factor: Number.parseInt(factor.value, 10),
        });
      });
      return relations;
    };

    for (const relation of existing?.packaging ?? []) addPackagingRow(relation);
    addPackagingBtn.addEventListener("click", () => addPackagingRow());

    packagingField.append(packagingLabel, packagingHelp, packagingRows, addPackagingBtn);

    const errorMsg = el("div", "form-error");
    errorMsg.hidden = true;

    const footer = el("div", "modal-footer");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", isEdit ? "Save Changes" : "Create Product");
    submitBtn.type = "submit";
    footer.append(cancelBtn, submitBtn);

    form.append(nameLabel, skuLabel, priceLabel, unitLabel, qtyLabel, aliasesLabel, lowStockLabel, packagingField, errorMsg, footer);
    modal.append(header, form);
    overlay.append(modal);
    root.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      errorMsg.hidden = true;
      submitBtn.disabled = true;
      submitBtn.textContent = "Saving…";

      try {
        const name = nameInput.value.trim();
        const sku = skuInput.value.trim();
        const priceNaira = parseFloat(priceInput.value);
        if (isNaN(priceNaira) || priceNaira < 0) {
          throw new Error("Price must be a positive number");
        }
        const priceKobo = Math.round(priceNaira * 100);
        const aliases = aliasesInput.value
          .split(",")
          .map((a) => a.trim())
          .filter((a) => a.length > 0);
        const lowStock = parseInt(lowStockInput.value, 10) || 0;
        const baseUnit = unitSelect.value as Unit;
        const packaging = readPackaging();
        const packagingIssue = packagingProblem(baseUnit, packaging);
        if (packagingIssue !== null) {
          throw new Error(packagingIssue);
        }

        if (isEdit) {
          const updateData: UpdateProductInput = {
            name,
            sku,
            aliases,
            price: { amount: priceKobo, currency: existing.price.currency },
            base_unit: baseUnit,
            packaging,
            low_stock_threshold: lowStock,
          };
          const updated = await updateProduct(existing.id, updateData);
          applyProduct(updated);
        } else {
          const qty = parseInt(qtyInput.value, 10) || 0;
          const createData: CreateProductInput = {
            name,
            sku,
            aliases,
            price: { amount: priceKobo, currency: "NGN" },
            quantity: qty,
            base_unit: baseUnit,
            packaging,
            low_stock_threshold: lowStock,
          };
          const created = await createProduct(createData);
          applyProduct(created);
        }
        closeModal();
      } catch (err: unknown) {
        submitBtn.disabled = false;
        submitBtn.textContent = isEdit ? "Save Changes" : "Create Product";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Failed to save product";
      }
    });
  }

  function showImportModal(): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", "Import Catalogue (CSV)");
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");
    const help = el(
      "p",
      "modal-help",
      "Upload or paste CSV with columns: sku, name, price_amount, price_currency, quantity, base_unit, aliases, packaging, low_stock_threshold"
    );

    const fileLabel = el("label", "form-field", "Choose CSV file");
    const fileInput = el("input", "input") as HTMLInputElement;
    fileInput.type = "file";
    fileInput.accept = ".csv,text/csv";
    fileLabel.append(fileInput);

    const textLabel = el("label", "form-field", "Or paste CSV content directly");
    const textArea = el("textarea", "input") as HTMLTextAreaElement;
    textArea.rows = 5;
    textArea.placeholder = "sku,name,price_amount,price_currency,quantity,base_unit,aliases,packaging,low_stock_threshold\nMILO-400,Milo 400g,250000,NGN,50,pack,Milo;Big Milo,carton:12,10";
    textLabel.append(textArea);

    const statusMsg = el("div", "status-badge status-badge--error");
    statusMsg.hidden = true;

    const actions = el("div", "modal-actions");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Import");
    submitBtn.type = "submit";
    actions.append(cancelBtn, submitBtn);

    form.append(help, fileLabel, textLabel, statusMsg, actions);
    modal.append(header, form);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    fileInput.addEventListener("change", () => {
      const file = fileInput.files?.[0];
      if (file) {
        const reader = new FileReader();
        reader.onload = () => {
          if (typeof reader.result === "string") {
            textArea.value = reader.result;
          }
        };
        reader.readAsText(file);
      }
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const content = textArea.value.trim();
      if (!content) {
        statusMsg.hidden = false;
        statusMsg.className = "status-badge status-badge--error";
        statusMsg.textContent = "Please select a file or paste CSV content";
        return;
      }

      submitBtn.disabled = true;
      submitBtn.textContent = "Importing...";
      statusMsg.hidden = true;

      try {
        const res = await importCatalogue(content, "text/csv");
        submitBtn.disabled = false;
        submitBtn.textContent = "Import";
        statusMsg.hidden = false;
        if (res.failed > 0 && res.imported === 0 && res.updated === 0) {
          statusMsg.className = "status-badge status-badge--error";
          statusMsg.textContent = `Import failed: ${res.errors.join(", ")}`;
        } else {
          statusMsg.className = "status-badge status-badge--success";
          statusMsg.textContent = `Import complete: ${res.imported} added, ${res.updated} updated${res.failed > 0 ? `, ${res.failed} errors` : ""}.`;
          void refetch();
          setTimeout(closeModal, 1200);
        }
      } catch (err: unknown) {
        submitBtn.disabled = false;
        submitBtn.textContent = "Import";
        statusMsg.hidden = false;
        statusMsg.className = "status-badge status-badge--error";
        statusMsg.textContent = err instanceof Error ? err.message : "Import failed";
      }
    });
  }

  function showStockAuditModal(product: Product): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", `Stock Audit: ${product.name}`);
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const form = el("form", "modal-form");
    const help = el(
      "p",
      "modal-help",
      "Correct stock count to match physical shelf count. A stock adjustment movement is appended to the audit ledger.",
    );

    const currentStockLabel = el("label", "form-field", "Current Recorded Stock");
    const currentStockInput = el("input", "input") as HTMLInputElement;
    currentStockInput.type = "text";
    currentStockInput.disabled = true;
    currentStockInput.value = `${product.quantity} ${formatUnit(product.base_unit, product.quantity)}`;
    currentStockLabel.append(currentStockInput);

    const newCountLabel = el("label", "form-field", "Physical Count (Base Units) *");
    const newCountInput = el("input", "input") as HTMLInputElement;
    newCountInput.type = "number";
    newCountInput.min = "0";
    newCountInput.required = true;
    newCountInput.value = String(product.quantity);
    newCountLabel.append(newCountInput);

    const reasonLabel = el("label", "form-field", "Audit Reason *");
    const reasonInput = el("input", "input") as HTMLInputElement;
    reasonInput.type = "text";
    reasonInput.required = true;
    reasonInput.placeholder = "e.g. Physical inventory count / Damaged goods / Spoilage";
    reasonInput.value = "Physical inventory count reconciliation";
    reasonLabel.append(reasonInput);

    const errorMsg = el("div", "form-error");
    errorMsg.hidden = true;

    const footer = el("div", "modal-footer");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", "Save Adjustment");
    submitBtn.type = "submit";
    footer.append(cancelBtn, submitBtn);

    form.append(help, currentStockLabel, newCountLabel, reasonLabel, errorMsg, footer);
    modal.append(header, form);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    cancelBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      errorMsg.hidden = true;
      const newCount = parseInt(newCountInput.value, 10);
      if (isNaN(newCount) || newCount < 0) {
        errorMsg.hidden = false;
        errorMsg.textContent = "Physical count must be a non-negative number";
        return;
      }
      const reason = reasonInput.value.trim() || "Stock count adjustment";

      submitBtn.disabled = true;
      submitBtn.textContent = "Saving…";

      try {
        const res = await adjustProductStock(product.id, newCount, reason);
        applyProduct(res.product);
        closeModal();
      } catch (err: unknown) {
        submitBtn.disabled = false;
        submitBtn.textContent = "Save Adjustment";
        errorMsg.hidden = false;
        errorMsg.textContent = err instanceof Error ? err.message : "Failed to adjust stock";
      }
    });
  }

  function showMovementHistoryModal(product: Product): void {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal-dialog modal-dialog--wide");

    const header = el("div", "modal-header");
    const title = el("h3", "modal-title", `Movement Ledger: ${product.name}`);
    const closeBtn = el("button", "button button--secondary button--sm", "✕");
    closeBtn.type = "button";
    header.append(title, closeBtn);

    const content = el("div", "modal-form");
    const info = el(
      "p",
      "modal-help",
      `SKU: ${product.sku} · Current stock: ${product.quantity} ${formatUnit(product.base_unit, product.quantity)}`,
    );

    const statusMsg = el("div", "status-badge", "Loading movements…");

    const table = el("table", "table");
    const thead = el("thead");
    const headerRow = el("tr");
    headerRow.append(
      el("th", "", "Time"),
      el("th", "", "Type"),
      el("th", "", "Change"),
      el("th", "", "Quantity"),
      el("th", "", "Note / Tx ID"),
    );
    thead.append(headerRow);

    const tbody = el("tbody");
    table.append(thead, tbody);
    table.hidden = true;

    const footer = el("div", "modal-footer");
    const doneBtn = el("button", "button button--secondary", "Close");
    doneBtn.type = "button";
    footer.append(doneBtn);

    content.append(info, statusMsg, table, footer);
    modal.append(header, content);
    overlay.append(modal);
    document.body.append(overlay);

    const closeModal = (): void => {
      overlay.remove();
    };

    closeBtn.addEventListener("click", closeModal);
    doneBtn.addEventListener("click", closeModal);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeModal();
    });

    void fetchProductMovements(product.id, 50, 0, signal).then((res) => {
      statusMsg.hidden = true;
      table.hidden = false;
      clear(tbody);

      if (res.movements.length === 0) {
        const emptyRow = el("tr", "table__empty");
        const emptyCell = cell("", "No movements recorded for this product yet.");
        emptyCell.colSpan = 5;
        emptyRow.append(emptyCell);
        tbody.append(emptyRow);
        return;
      }

      for (const m of res.movements) {
        const tr = el("tr", "table__row");
        const timeCell = cell("table__cell", `${formatDate(m.created_at)} ${formatTime(m.created_at)}`);
        const typeCell = cell("table__cell", `${m.type} · ${m.status}`);

        const deltaText = m.delta > 0 ? `+${m.delta}` : String(m.delta);
        const deltaCell = cell("table__cell table__cell--mono", deltaText);
        if (m.delta > 0) {
          deltaCell.style.color = "#16a34a";
        } else if (m.delta < 0) {
          deltaCell.style.color = "#dc2626";
        }

        const quantityCell = cell(
          "table__cell table__cell--stock",
          `${m.quantity} ${formatUnit(m.unit, m.quantity)}`,
        );

        const noteCell = cell("table__cell table__cell--mono", m.transcript || m.transaction_id || "—");
        noteCell.style.fontSize = "0.8rem";

        tr.append(timeCell, typeCell, deltaCell, quantityCell, noteCell);
        tbody.append(tr);
      }
    }).catch((err: unknown) => {
      statusMsg.className = "status-badge status-badge--error";
      statusMsg.textContent = err instanceof Error ? err.message : "Failed to load movements";
    });
  }

  function packagingSummary(product: Product): string {
    const rules = product.packaging ?? [];
    if (rules.length === 0) return "—";
    return rules
      .map((rule) => `1 ${rule.from} = ${rule.factor} ${formatUnit(rule.to, rule.factor)}`)
      .join(", ");
  }

  function catalogueRow(product: Product): HTMLTableRowElement {
    const isArchived = Boolean(product.archived_at);
    const row = el("tr", `table__row${isArchived ? " table__row--archived" : isLowStock(product) ? " table__row--low" : ""}`);

    const name = cell("table__cell table__cell--product");
    name.append(document.createTextNode(product.name));
    if (isArchived) {
      name.append(el("span", "badge badge--rejected", "Archived"));
    }

    const sku = cell("table__cell table__cell--mono", product.sku);

    // The aliases are the shop's own vocabulary; an empty list is a product the
    // system can only recognise by its official name.
    const aliases = cell(
      "table__cell",
      product.aliases.length > 0 ? product.aliases.join(", ") : "—",
    );

    const price = cell("table__cell table__cell--amount", formatMoney(product.price));

    const stock = cell(
      "table__cell table__cell--stock",
      `${product.quantity} ${formatUnit(product.base_unit, product.quantity)}`,
    );
    if (!isArchived && isLowStock(product)) {
      stock.append(el("span", "badge badge--low-stock", "Low"));
    }

    const packaging = cell("table__cell", packagingSummary(product));

    const actions = cell("table__cell table__cell--actions");
    const editBtn = el("button", "button button--secondary button--sm", "Edit");
    editBtn.type = "button";
    editBtn.addEventListener("click", () => showProductModal(product));

    const auditBtn = el("button", "button button--secondary button--sm", "Audit");
    auditBtn.type = "button";
    auditBtn.title = "Correct stock count to match physical shelf count";
    auditBtn.addEventListener("click", () => showStockAuditModal(product));

    const historyBtn = el("button", "button button--secondary button--sm", "History");
    historyBtn.type = "button";
    historyBtn.title = "View product movement history ledger";
    historyBtn.addEventListener("click", () => showMovementHistoryModal(product));

    const archiveBtn = el(
      "button",
      `button button--sm ${isArchived ? "button--primary" : "button--secondary"}`,
      isArchived ? "Restore" : "Archive",
    );
    archiveBtn.type = "button";
    archiveBtn.title = isArchived
      ? "Restore product to active speech recognition"
      : "Safely archive product from speech recognition while preserving transaction history";
    archiveBtn.addEventListener("click", async () => {
      if (isArchived) {
        archiveBtn.disabled = true;
        try {
          await unarchiveProduct(product.id);
          void refetch();
        } catch (err: unknown) {
          archiveBtn.disabled = false;
          alert(err instanceof Error ? err.message : "Failed to restore product");
        }
      } else {
        if (!confirm(`Archive "${product.name}"? Historical ledger records remain intact, but it will no longer match spoken captures.`)) {
          return;
        }
        archiveBtn.disabled = true;
        try {
          await archiveProduct(product.id);
          void refetch();
        } catch (err: unknown) {
          archiveBtn.disabled = false;
          alert(err instanceof Error ? err.message : "Failed to archive product");
        }
      }
    });

    actions.append(editBtn, auditBtn, historyBtn, archiveBtn);

    row.append(name, sku, aliases, price, stock, packaging, actions);
    return row;
  }

  function render(): void {
    if (body === null) return;
    clear(body);

    const needle = query.trim().toLowerCase();

    // Filter by tab
    const tabProducts = products.filter((product) => {
      if (currentTab === "active") return !product.archived_at;
      if (currentTab === "archived") return Boolean(product.archived_at);
      return true; // "all"
    });

    // Filter by search needle
    const matches = needle === ""
      ? tabProducts
      : tabProducts.filter(
          (product) =>
            product.name.toLowerCase().includes(needle) ||
            product.sku.toLowerCase().includes(needle) ||
            product.aliases.some((alias) => alias.toLowerCase().includes(needle)),
        );

    const activeCount = products.filter((p) => !p.archived_at).length;
    const archivedCount = products.length - activeCount;
    if (currentTab === "active") {
      setText(countLabel, `${activeCount}`);
    } else if (currentTab === "archived") {
      setText(countLabel, `${archivedCount} archived`);
    } else {
      setText(countLabel, `${products.length} total (${activeCount} active, ${archivedCount} archived)`);
    }

    if (matches.length === 0) {
      let title = "No products yet. Add the first one to get started.";
      if (needle !== "") {
        title = "No products match this search.";
      } else if (currentTab === "archived") {
        title = "No archived products in catalogue.";
      } else if (currentTab === "active" && products.length > 0) {
        title = "All products are currently archived.";
      }
      // Only an empty catalogue has an obvious next step; a filtered-to-nothing
      // result does not, so it offers no action rather than a misleading one.
      const brandNewCatalogue = needle === "" && products.length === 0;
      const emptyOptions: UiStateOptions = brandNewCatalogue
        ? {
            title,
            detail: "Products are what voice capture matches against.",
            actionLabel: "Add product",
            onAction: () => showProductModal(),
          }
        : { title };
      body.append(uiStateRow("empty", emptyOptions, 7));
      return;
    }
    for (const product of matches) {
      body.append(catalogueRow(product));
    }
  }

  async function refetch(): Promise<void> {
    try {
      const snapshot = await fetchInventory(
        shopId,
        {
          q: query.trim() || undefined,
          status: currentTab !== "all" ? currentTab : undefined,
        },
        signal,
      );
      if (disposed || signal.aborted) return;
      if (snapshot.last_seq !== undefined) {
        getSocket()?.syncSequence(snapshot.last_seq);
      }
      products = [...snapshot.products];
      if (!query.trim() && (currentTab === "active" || currentTab === "all")) {
        const activeProds = snapshot.products.filter((p) => !p.archived_at);
        void setCachedCatalogue(shopId, activeProds);
      }
      render();
    } catch (error) {
      if (disposed || signal.aborted) return;
      console.error("ListenLedger: could not load the catalogue", error);
      if (body !== null) {
        clear(body);
        body.append(
          uiStateRow(
            "error",
            {
              title: "Could not load the catalogue.",
              detail: "We'll retry automatically when the connection returns.",
              actionLabel: "Retry",
              onAction: () => {
                void refetch();
              },
            },
            7,
          ),
        );
      }
    }
  }

  /**
   * Applies a stock or catalogue change.
   *
   * The inventory.updated event carries the whole product precisely so the view
   * can replace its row without refetching the catalogue.
   */
  function applyProduct(product: Product): void {
    const isArchived = Boolean(product.archived_at);
    const matchesStatus =
      currentTab === "all" ||
      (currentTab === "active" && !isArchived) ||
      (currentTab === "archived" && isArchived);

    const index = products.findIndex((existing) => existing.id === product.id);
    if (index !== -1) {
      if (matchesStatus) {
        products[index] = product;
      } else {
        products.splice(index, 1);
      }
    } else if (matchesStatus) {
      products.push(product);
      products.sort((a, b) => a.name.localeCompare(b.name));
    }
    render();
  }

  const onInventoryUpdated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Product }>).detail;
    applyProduct(detail.data);
  };

  const onProductCreated = (event: Event): void => {
    const detail = (event as CustomEvent<{ data: Product }>).detail;
    applyProduct(detail.data);
  };

  let searchDebounceTimer: ReturnType<typeof setTimeout> | undefined;
  const onSearch = (): void => {
    query = searchInput?.value ?? "";
    render();
    if (searchDebounceTimer !== undefined) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(() => {
      void refetch();
    }, 250);
  };

  const onAddClick = (): void => {
    showProductModal();
  };

  const onImportClick = (): void => {
    showImportModal();
  };

  const onConnectionStatus = (event: Event): void => {
    const detail = (event as CustomEvent<ConnectionStatusDetail>).detail;
    if (detail.resyncRequired) {
      void refetch();
    }
  };

  window.addEventListener("ll:inventory-updated", onInventoryUpdated);
  window.addEventListener("ll:product-created", onProductCreated);
  window.addEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
  searchInput?.addEventListener("input", onSearch);
  addButton?.addEventListener("click", onAddClick);
  importButton?.addEventListener("click", onImportClick);

  void refetch();

  return () => {
    disposed = true;
    abortController.abort();
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    searchInput?.removeEventListener("input", onSearch);
    addButton?.removeEventListener("click", onAddClick);
    importButton?.removeEventListener("click", onImportClick);
    tabButtons.forEach((btn) => {
      btn.removeEventListener("click", onTabClick as EventListener);
    });
  };
}
