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
  createProduct,
  fetchInventory,
  importCatalogue,
  updateProduct,
  type CreateProductInput,
  type Product,
  type Unit,
  type UpdateProductInput,
} from "../api.js";
import {
  cell,
  clear,
  el,
  formatMoney,
  formatUnit,
  hook,
  isLowStock,
  setText,
} from "../format.js";
import { CONNECTION_STATUS_EVENT, type ConnectionStatusDetail } from "../ws.js";
import type { ViewCleanup } from "./index.js";

export function mountInventory(root: HTMLElement, shopId: string): ViewCleanup {
  let products: Product[] = [];
  let query = "";
  let disposed = false;

  const body = hook(root, "catalogue-body");
  const countLabel = hook(root, "inventory-count");
  const searchInput = hook<HTMLInputElement>(root, "catalogue-query");
  const addButton = hook<HTMLButtonElement>(root, "catalogue-add");
  const importButton = hook<HTMLButtonElement>(root, "catalogue-import");

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
    const units: Unit[] = ["piece", "pack", "carton", "bottle", "crate"];
    for (const u of units) {
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

    const errorMsg = el("div", "form-error");
    errorMsg.hidden = true;

    const footer = el("div", "modal-footer");
    const cancelBtn = el("button", "button button--secondary", "Cancel");
    cancelBtn.type = "button";
    const submitBtn = el("button", "button button--primary", isEdit ? "Save Changes" : "Create Product");
    submitBtn.type = "submit";
    footer.append(cancelBtn, submitBtn);

    form.append(nameLabel, skuLabel, priceLabel, unitLabel, qtyLabel, aliasesLabel, lowStockLabel, errorMsg, footer);
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

        if (isEdit) {
          const updateData: UpdateProductInput = {
            name,
            sku,
            aliases,
            price: { amount: priceKobo, currency: existing.price.currency },
            low_stock_threshold: lowStock,
          };
          const updated = await updateProduct(existing.id, updateData);
          applyProduct(updated);
        } else {
          const qty = parseInt(qtyInput.value, 10) || 0;
          const baseUnit = unitSelect.value as Unit;
          const createData: CreateProductInput = {
            name,
            sku,
            aliases,
            price: { amount: priceKobo, currency: "NGN" },
            quantity: qty,
            base_unit: baseUnit,
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

  function packagingSummary(product: Product): string {
    const rules = product.packaging ?? [];
    if (rules.length === 0) return "—";
    return rules
      .map((rule) => `1 ${rule.from} = ${rule.factor} ${formatUnit(rule.to, rule.factor)}`)
      .join(", ");
  }

  function catalogueRow(product: Product): HTMLTableRowElement {
    const row = el("tr", `table__row${isLowStock(product) ? " table__row--low" : ""}`);

    const name = cell("table__cell table__cell--product", product.name);
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
    if (isLowStock(product)) {
      stock.append(el("span", "badge badge--low-stock", "Low"));
    }

    const packaging = cell("table__cell", packagingSummary(product));

    const actions = cell("table__cell table__cell--actions");
    const editBtn = el("button", "button button--secondary button--sm", "Edit");
    editBtn.type = "button";
    editBtn.addEventListener("click", () => showProductModal(product));
    actions.append(editBtn);

    row.append(name, sku, aliases, price, stock, packaging, actions);
    return row;
  }

  function render(): void {
    if (body === null) return;
    clear(body);

    const needle = query.trim().toLowerCase();
    const matches = needle === ""
      ? products
      : products.filter(
          (product) =>
            product.name.toLowerCase().includes(needle) ||
            product.sku.toLowerCase().includes(needle) ||
            product.aliases.some((alias) => alias.toLowerCase().includes(needle)),
        );

    setText(countLabel, String(products.length));

    if (matches.length === 0) {
      const row = el("tr", "table__empty");
      const cellElement = cell(
        "",
        products.length === 0
          ? "No products yet. Add the first one to get started."
          : "No products match this search.",
      );
      cellElement.colSpan = 7;
      row.append(cellElement);
      body.append(row);
      return;
    }
    for (const product of matches) {
      body.append(catalogueRow(product));
    }
  }

  async function refetch(): Promise<void> {
    try {
      const snapshot = await fetchInventory(shopId);
      if (disposed) return;
      products = [...snapshot.products];
      render();
    } catch (error) {
      if (disposed) return;
      console.error("ListenLedger: could not load the catalogue", error);
      if (body !== null) {
        clear(body);
        const row = el("tr", "table__empty");
        const cellElement = cell("", "Could not reach the server. Retrying on reconnect.");
        cellElement.colSpan = 7;
        row.append(cellElement);
        body.append(row);
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
    const index = products.findIndex((existing) => existing.id === product.id);
    if (index === -1) {
      products.push(product);
      products.sort((a, b) => a.name.localeCompare(b.name));
    } else {
      products[index] = product;
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

  const onSearch = (): void => {
    query = searchInput?.value ?? "";
    render();
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
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    window.removeEventListener(CONNECTION_STATUS_EVENT, onConnectionStatus);
    searchInput?.removeEventListener("input", onSearch);
    addButton?.removeEventListener("click", onAddClick);
    importButton?.removeEventListener("click", onImportClick);
  };
}
