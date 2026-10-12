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

import { fetchInventory, type Product } from "../api.js";
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
import type { ViewCleanup } from "./index.js";

export function mountInventory(root: HTMLElement, shopId: string): ViewCleanup {
  let products: Product[] = [];
  let query = "";
  let disposed = false;

  const body = hook(root, "catalogue-body");
  const countLabel = hook(root, "inventory-count");
  const searchInput = hook<HTMLInputElement>(root, "catalogue-query");

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

    // The actions column is in the template's header but has no behaviour yet:
    // editing a product needs PATCH /api/products/{id}, which does not exist.
    const actions = cell("table__cell table__cell--actions");
    actions.append(el("span", "table__placeholder", "—"));

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

  window.addEventListener("ll:inventory-updated", onInventoryUpdated);
  window.addEventListener("ll:product-created", onProductCreated);
  searchInput?.addEventListener("input", onSearch);

  void refetch();

  return () => {
    disposed = true;
    window.removeEventListener("ll:inventory-updated", onInventoryUpdated);
    window.removeEventListener("ll:product-created", onProductCreated);
    searchInput?.removeEventListener("input", onSearch);
  };
}
