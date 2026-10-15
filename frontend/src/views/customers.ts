/**
 * Customer Credit & Debt Book (Customer Book / I-Owe-You / "Gbese" Ledger).
 *
 * Provides informal retail merchants with:
 * - Real-time accounts receivable overview
 * - Customer directory with phone, notes, and debt balances
 * - Single-click credit sales, cash repayments, and debt write-offs
 * - Full chronological ledger history per customer
 */

import {
  type Customer,
  type CustomerDebtType,
  createCustomer,
  fetchCustomerDebts,
  fetchCustomers,
  fetchShopCreditSummary,
  recordCustomerDebt,
  updateCustomer,
} from "../api.js";
import { clear, el, formatMoney, formatTime, setText } from "../format.js";

function removeModalBackdrop(): void {
  const existing = document.querySelectorAll(".modal-backdrop");
  for (const b of existing) {
    b.remove();
  }
}

/** Opens the main Customer Credit & Debt Book modal */
export function showCustomerBookModal(_root: HTMLElement, onDone?: () => void): void {
  removeModalBackdrop();

  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--customer-book");

  // Header
  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", "📖 Customer Credit & Debt Book (Gbese Ledger)"),
    el(
      "p",
      "modal__subtitle",
      "Manage informal customer credit, record sales on tick, log cash repayments, and track shop accounts receivable.",
    ),
  );

  const content = el("div", "modal__form");

  // Summary bar container
  const summaryBar = el("div", "customer-summary-bar");
  summaryBar.innerHTML = `
    <div class="customer-summary-item">
      <span class="customer-summary-item__label">Customers Owing</span>
      <span class="customer-summary-item__value" data-hook="summary-owing-count">—</span>
    </div>
    <div class="customer-summary-item">
      <span class="customer-summary-item__label">Total Debt (Receivables)</span>
      <span class="customer-summary-item__value customer-summary-item__value--debt" data-hook="summary-total-debt">—</span>
    </div>
    <div class="customer-summary-item">
      <span class="customer-summary-item__label">Today's Credit Sales</span>
      <span class="customer-summary-item__value" data-hook="summary-today-sales">—</span>
    </div>
    <div class="customer-summary-item">
      <span class="customer-summary-item__label">Today's Repayments</span>
      <span class="customer-summary-item__value" data-hook="summary-today-repayments">—</span>
    </div>
  `;

  // Filter Bar
  const filterBar = el("div", "customer-filter-bar");
  const inputsGroup = el("div", "customer-filter-bar__inputs");

  const searchInput = document.createElement("input");
  searchInput.type = "search";
  searchInput.placeholder = "Search customer by name or phone…";
  searchInput.className = "form-input customer-filter-bar__search";
  searchInput.setAttribute("data-hook", "customer-search-input");

  const checkboxLabel = el("label", "customer-filter-bar__checkbox");
  const debtOnlyCheckbox = document.createElement("input");
  debtOnlyCheckbox.type = "checkbox";
  debtOnlyCheckbox.setAttribute("data-hook", "customer-debt-only-checkbox");
  checkboxLabel.append(debtOnlyCheckbox, document.createTextNode("Owing debt only"));

  inputsGroup.append(searchInput, checkboxLabel);

  const registerBtn = el("button", "btn btn--primary btn--sm", "+ Register Customer");
  registerBtn.type = "button";
  registerBtn.setAttribute("data-hook", "register-customer-btn");
  registerBtn.addEventListener("click", () => {
    showRegisterCustomerModal(() => {
      void loadData();
      if (onDone) onDone();
    });
  });

  filterBar.append(inputsGroup, registerBtn);

  // Table container
  const tableContainer = el("div", "customer-table-container");
  const table = el("table", "customer-table");
  table.innerHTML = `
    <thead>
      <tr>
        <th>Customer</th>
        <th>Phone</th>
        <th>Outstanding Debt</th>
        <th>Notes</th>
        <th style="text-align: right;">Actions</th>
      </tr>
    </thead>
    <tbody data-hook="customer-table-body">
      <tr>
        <td colspan="5" style="text-align: center; color: #64748b; padding: 2rem;">Loading customers…</td>
      </tr>
    </tbody>
  `;
  tableContainer.append(table);

  // Modal Actions
  const actions = el("div", "modal__actions");
  const closeBtn = el("button", "btn btn--secondary", "Close");
  closeBtn.type = "button";
  closeBtn.setAttribute("data-hook", "customer-book-close-btn");
  closeBtn.addEventListener("click", () => {
    backdrop.remove();
    if (onDone) onDone();
  });
  actions.append(closeBtn);

  content.append(summaryBar, filterBar, tableContainer, actions);
  modal.append(header, content);
  backdrop.append(modal);
  document.body.append(backdrop);

  const tbody = table.querySelector('[data-hook="customer-table-body"]') as HTMLElement;
  const owingCountEl = summaryBar.querySelector('[data-hook="summary-owing-count"]') as HTMLElement;
  const totalDebtEl = summaryBar.querySelector('[data-hook="summary-total-debt"]') as HTMLElement;
  const todaySalesEl = summaryBar.querySelector('[data-hook="summary-today-sales"]') as HTMLElement;
  const todayRepaymentsEl = summaryBar.querySelector('[data-hook="summary-today-repayments"]') as HTMLElement;

  async function loadData(): Promise<void> {
    try {
      const [summaryRes, customersRes] = await Promise.all([
        fetchShopCreditSummary().catch(() => undefined),
        fetchCustomers({
          search: searchInput.value.trim() || undefined,
          has_debt: debtOnlyCheckbox.checked ? true : undefined,
          limit: 100,
        }),
      ]);

      if (summaryRes) {
        setText(owingCountEl, String(summaryRes.total_customers_with_debt));
        setText(totalDebtEl, formatMoney(summaryRes.total_outstanding_debt));
        setText(todaySalesEl, formatMoney(summaryRes.today_credit_sales_amount));
        setText(todayRepaymentsEl, formatMoney(summaryRes.today_repayments_amount));
      }

      clear(tbody);
      const customers = customersRes.customers;
      if (customers.length === 0) {
        const emptyRow = el("tr");
        const emptyCell = el(
          "td",
          "",
          debtOnlyCheckbox.checked
            ? "No customers with outstanding debt found."
            : "No registered customers found. Click '+ Register Customer' to get started.",
        );
        emptyCell.setAttribute("colspan", "5");
        emptyCell.style.textAlign = "center";
        emptyCell.style.padding = "2rem";
        emptyCell.style.color = "#64748b";
        emptyRow.append(emptyCell);
        tbody.append(emptyRow);
        return;
      }

      for (const c of customers) {
        const row = el("tr");
        row.setAttribute("data-customer-id", c.id);

        // Name
        const nameCell = el("td");
        const nameStrong = el("strong", "", c.name);
        nameCell.append(nameStrong);

        // Phone
        const phoneCell = el("td", "", c.phone || "—");

        // Balance badge
        const balanceCell = el("td");
        const isOwing = c.outstanding_balance.amount > 0;
        const badge = el(
          "span",
          `badge ${isOwing ? "badge--debt-positive" : "badge--debt-zero"}`,
          isOwing ? formatMoney(c.outstanding_balance) : "Clear (₦0)",
        );
        balanceCell.append(badge);

        // Notes
        const notesCell = el("td", "", c.notes || "—");

        // Actions
        const actionsCell = el("td");
        actionsCell.style.textAlign = "right";
        const group = el("div", "customer-action-btn-group");
        group.style.justifyContent = "flex-end";

        // Credit Sale button
        const creditBtn = el("button", "btn btn--sm btn--secondary", "+ Credit");
        creditBtn.type = "button";
        creditBtn.title = "Record a sale on credit";
        creditBtn.addEventListener("click", () => {
          showRecordDebtModal(c, "credit_sale", () => {
            void loadData();
            if (onDone) onDone();
          });
        });

        // Repayment button
        const repayBtn = el("button", "btn btn--sm btn--secondary", "Repay");
        repayBtn.type = "button";
        repayBtn.title = "Record cash repayment";
        repayBtn.addEventListener("click", () => {
          showRecordDebtModal(c, "repayment", () => {
            void loadData();
            if (onDone) onDone();
          });
        });

        // History button
        const historyBtn = el("button", "btn btn--sm btn--secondary", "History");
        historyBtn.type = "button";
        historyBtn.title = "View ledger history";
        historyBtn.addEventListener("click", () => {
          showCustomerHistoryModal(c);
        });

        // Edit button
        const editBtn = el("button", "btn btn--sm btn--secondary", "Edit");
        editBtn.type = "button";
        editBtn.title = "Edit customer profile";
        editBtn.addEventListener("click", () => {
          showEditCustomerModal(c, () => {
            void loadData();
            if (onDone) onDone();
          });
        });

        group.append(creditBtn, repayBtn, historyBtn, editBtn);
        actionsCell.append(group);

        row.append(nameCell, phoneCell, balanceCell, notesCell, actionsCell);
        tbody.append(row);
      }
    } catch (err: unknown) {
      clear(tbody);
      const errRow = el("tr");
      const errCell = el("td", "error", `Failed to load customers: ${err instanceof Error ? err.message : String(err)}`);
      errCell.setAttribute("colspan", "5");
      errCell.style.textAlign = "center";
      errRow.append(errCell);
      tbody.append(errRow);
    }
  }

  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  searchInput.addEventListener("input", () => {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      void loadData();
    }, 250);
  });

  debtOnlyCheckbox.addEventListener("change", () => {
    void loadData();
  });

  void loadData();
}

/** Modal to register a new customer */
export function showRegisterCustomerModal(onDone: () => void): void {
  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", "Register New Customer"),
    el("p", "modal__subtitle", "Add a trusted customer to your shop credit book."),
  );

  const form = el("form", "modal__form");

  // Name
  const nameGroup = el("div", "form-group");
  nameGroup.append(el("label", "form-label", "Customer Name *"));
  const nameInput = document.createElement("input");
  nameInput.type = "text";
  nameInput.required = true;
  nameInput.maxLength = 150;
  nameInput.placeholder = "e.g. Alhaji Musa or Mama Chinedu";
  nameInput.className = "form-input";
  nameInput.setAttribute("data-hook", "new-customer-name");
  nameGroup.append(nameInput);

  // Phone
  const phoneGroup = el("div", "form-group");
  phoneGroup.append(el("label", "form-label", "Phone Number (Optional)"));
  const phoneInput = document.createElement("input");
  phoneInput.type = "tel";
  phoneInput.maxLength = 32;
  phoneInput.placeholder = "e.g. 08012345678";
  phoneInput.className = "form-input";
  phoneInput.setAttribute("data-hook", "new-customer-phone");
  phoneGroup.append(phoneInput);

  // Notes
  const notesGroup = el("div", "form-group");
  notesGroup.append(el("label", "form-label", "Notes / Description (Optional)"));
  const notesInput = document.createElement("textarea");
  notesInput.rows = 2;
  notesInput.maxLength = 500;
  notesInput.placeholder = "e.g. Stall 4 neighbour, pays monthly on salary date";
  notesInput.className = "form-input";
  notesInput.setAttribute("data-hook", "new-customer-notes");
  notesGroup.append(notesInput);

  // Initial Debt
  const initDebtGroup = el("div", "form-group");
  initDebtGroup.append(el("label", "form-label", "Initial Debt / Opening Balance (Naira, Optional)"));
  const initDebtInput = document.createElement("input");
  initDebtInput.type = "number";
  initDebtInput.step = "any";
  initDebtInput.min = "0";
  initDebtInput.placeholder = "e.g. 5000 (leave 0 if customer owes nothing)";
  initDebtInput.className = "form-input";
  initDebtInput.setAttribute("data-hook", "new-customer-initial-debt");
  initDebtGroup.append(initDebtInput);

  const errorMsg = el("div", "modal__error form-error", "");
  errorMsg.style.display = "none";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "btn btn--secondary", "Cancel");
  cancelBtn.type = "button";
  cancelBtn.addEventListener("click", () => backdrop.remove());

  const submitBtn = el("button", "btn btn--primary", "Register Customer");
  submitBtn.type = "submit";
  actions.append(cancelBtn, submitBtn);

  form.append(nameGroup, phoneGroup, notesGroup, initDebtGroup, errorMsg, actions);
  modal.append(header, form);
  backdrop.append(modal);
  document.body.append(backdrop);
  if (typeof nameInput.focus === "function") {
    nameInput.focus();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    errorMsg.style.display = "none";
    errorMsg.textContent = "";

    const name = nameInput.value.trim();
    if (!name) {
      errorMsg.textContent = "Customer name is required.";
      errorMsg.style.display = "block";
      return;
    }

    let initialDebt: { amount: number; currency: string } | undefined;
    const debtVal = parseFloat(initDebtInput.value);
    if (!isNaN(debtVal) && debtVal > 0) {
      initialDebt = { amount: Math.round(debtVal * 100), currency: "NGN" };
    }

    submitBtn.disabled = true;
    setText(submitBtn, "Registering…");

    try {
      await createCustomer({
        name,
        phone: phoneInput.value.trim() || undefined,
        notes: notesInput.value.trim() || undefined,
        initial_debt: initialDebt,
        initial_debt_description: initialDebt ? "Carried over opening balance" : undefined,
      });
      backdrop.remove();
      onDone();
    } catch (err: unknown) {
      submitBtn.disabled = false;
      setText(submitBtn, "Register Customer");
      errorMsg.textContent = err instanceof Error ? err.message : "Failed to register customer";
      errorMsg.style.display = "block";
    }
  });
}

/** Modal to record a credit sale, cash repayment, or debt forgiveness */
export function showRecordDebtModal(customer: Customer, defaultType: CustomerDebtType, onDone: () => void): void {
  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", `Record Movement: ${customer.name}`),
    el("p", "modal__subtitle", `Current Balance: ${formatMoney(customer.outstanding_balance)}`),
  );

  const form = el("form", "modal__form");

  // Movement Type
  const typeGroup = el("div", "form-group");
  typeGroup.append(el("label", "form-label", "Movement Type *"));
  const typeSelect = document.createElement("select");
  typeSelect.className = "form-select";
  typeSelect.setAttribute("data-hook", "debt-type-select");
  typeSelect.innerHTML = `
    <option value="credit_sale" ${defaultType === "credit_sale" ? "selected" : ""}>Sale on Credit (Increases Debt)</option>
    <option value="repayment" ${defaultType === "repayment" ? "selected" : ""}>Cash Repayment (Decreases Debt)</option>
    <option value="forgiveness" ${defaultType === "forgiveness" ? "selected" : ""}>Debt Forgiveness / Discount (Decreases Debt)</option>
  `;
  typeGroup.append(typeSelect);

  // Amount
  const amountGroup = el("div", "form-group");
  amountGroup.append(el("label", "form-label", "Amount (Naira) *"));
  const amountInput = document.createElement("input");
  amountInput.type = "number";
  amountInput.step = "any";
  amountInput.min = "0.01";
  amountInput.required = true;
  amountInput.placeholder = "e.g. 3500";
  amountInput.className = "form-input";
  amountInput.setAttribute("data-hook", "debt-amount-input");
  amountGroup.append(amountInput);

  // Description
  const descGroup = el("div", "form-group");
  descGroup.append(el("label", "form-label", "Description / Reason *"));
  const descInput = document.createElement("input");
  descInput.type = "text";
  descInput.required = true;
  descInput.maxLength = 500;
  descInput.placeholder = defaultType === "repayment" ? "e.g. Paid cash at shop counter" : "e.g. 2 tins of milk on credit";
  descInput.className = "form-input";
  descInput.setAttribute("data-hook", "debt-desc-input");
  descGroup.append(descInput);

  // Receipt reference
  const refGroup = el("div", "form-group");
  refGroup.append(el("label", "form-label", "Receipt / Reference (Optional)"));
  const refInput = document.createElement("input");
  refInput.type = "text";
  refInput.placeholder = "e.g. REC-102";
  refInput.className = "form-input";
  refInput.setAttribute("data-hook", "debt-ref-input");
  refGroup.append(refInput);

  const errorMsg = el("div", "modal__error form-error", "");
  errorMsg.style.display = "none";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "btn btn--secondary", "Cancel");
  cancelBtn.type = "button";
  cancelBtn.addEventListener("click", () => backdrop.remove());

  const submitBtn = el("button", "btn btn--primary", "Save Movement");
  submitBtn.type = "submit";
  actions.append(cancelBtn, submitBtn);

  form.append(typeGroup, amountGroup, descGroup, refGroup, errorMsg, actions);
  modal.append(header, form);
  backdrop.append(modal);
  document.body.append(backdrop);

  if (typeof amountInput.focus === "function") {
    amountInput.focus();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    errorMsg.style.display = "none";
    errorMsg.textContent = "";

    const val = parseFloat(amountInput.value);
    if (isNaN(val) || val <= 0) {
      errorMsg.textContent = "Please enter a valid amount greater than zero.";
      errorMsg.style.display = "block";
      return;
    }
    const minorAmount = Math.round(val * 100);

    const desc = descInput.value.trim();
    if (!desc) {
      errorMsg.textContent = "Description is required.";
      errorMsg.style.display = "block";
      return;
    }

    submitBtn.disabled = true;
    setText(submitBtn, "Saving…");

    try {
      await recordCustomerDebt(customer.id, {
        type: typeSelect.value as CustomerDebtType,
        amount: { amount: minorAmount, currency: customer.outstanding_balance.currency || "NGN" },
        description: desc,
        receipt_ref: refInput.value.trim() || undefined,
      });
      backdrop.remove();
      onDone();
    } catch (err: unknown) {
      submitBtn.disabled = false;
      setText(submitBtn, "Save Movement");
      errorMsg.textContent = err instanceof Error ? err.message : "Failed to save movement";
      errorMsg.style.display = "block";
    }
  });
}

/** Modal to view complete debt and repayment ledger movements for a customer */
export function showCustomerHistoryModal(customer: Customer): void {
  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", `Credit Ledger: ${customer.name}`),
    el("p", "modal__subtitle", `Current Outstanding Debt: ${formatMoney(customer.outstanding_balance)}`),
  );

  const content = el("div", "modal__form");
  const historyList = el("div", "customer-debt-history");
  historyList.append(el("p", "hint", "Loading ledger movements…"));

  const actions = el("div", "modal__actions");
  const closeBtn = el("button", "btn btn--secondary", "Close");
  closeBtn.type = "button";
  closeBtn.addEventListener("click", () => backdrop.remove());
  actions.append(closeBtn);

  content.append(historyList, actions);
  modal.append(header, content);
  backdrop.append(modal);
  document.body.append(backdrop);

  void fetchCustomerDebts(customer.id)
    .then((res) => {
      clear(historyList);
      if (res.debts.length === 0) {
        historyList.append(el("p", "hint", "No credit or repayment movements recorded yet for this customer."));
        return;
      }

      for (const d of res.debts) {
        const item = el("div", `customer-debt-item customer-debt-item--${d.type}`);
        const left = el("div");

        let typeLabel = "Sale on Credit";
        let amountPrefix = "+";
        if (d.type === "repayment") {
          typeLabel = "Cash Repayment";
          amountPrefix = "-";
        } else if (d.type === "forgiveness") {
          typeLabel = "Debt Forgiveness";
          amountPrefix = "-";
        }

        const title = el("strong", "", `${typeLabel}: ${amountPrefix}${formatMoney(d.amount)}`);
        const meta = el(
          "div",
          "field__hint",
          `${formatTime(d.occurred_at)} · Balance After: ${formatMoney(d.balance_after)}`,
        );
        const desc = el("div", "", d.description);

        left.append(title, desc, meta);
        if (d.receipt_ref) {
          left.append(el("span", "badge", `Ref: ${d.receipt_ref}`));
        }

        item.append(left);
        historyList.append(item);
      }
    })
    .catch((err: unknown) => {
      clear(historyList);
      historyList.append(
        el("p", "error", `Failed to load ledger history: ${err instanceof Error ? err.message : String(err)}`),
      );
    });
}

/** Modal to update customer profile */
export function showEditCustomerModal(customer: Customer, onDone: () => void): void {
  const backdrop = el("div", "modal-backdrop");
  const modal = el("div", "modal modal--edit-confirm");

  const header = el("div", "modal__header");
  header.append(
    el("h2", "modal__title", `Edit Customer: ${customer.name}`),
    el("p", "modal__subtitle", "Update contact details and customer notes."),
  );

  const form = el("form", "modal__form");

  const nameGroup = el("div", "form-group");
  nameGroup.append(el("label", "form-label", "Customer Name *"));
  const nameInput = document.createElement("input");
  nameInput.type = "text";
  nameInput.required = true;
  nameInput.value = customer.name;
  nameInput.maxLength = 150;
  nameInput.className = "form-input";
  nameGroup.append(nameInput);

  const phoneGroup = el("div", "form-group");
  phoneGroup.append(el("label", "form-label", "Phone Number"));
  const phoneInput = document.createElement("input");
  phoneInput.type = "tel";
  phoneInput.value = customer.phone || "";
  phoneInput.maxLength = 32;
  phoneInput.className = "form-input";
  phoneGroup.append(phoneInput);

  const notesGroup = el("div", "form-group");
  notesGroup.append(el("label", "form-label", "Notes"));
  const notesInput = document.createElement("textarea");
  notesInput.rows = 2;
  notesInput.value = customer.notes || "";
  notesInput.maxLength = 500;
  notesInput.className = "form-input";
  notesGroup.append(notesInput);

  const errorMsg = el("div", "modal__error form-error", "");
  errorMsg.style.display = "none";

  const actions = el("div", "modal__actions");
  const cancelBtn = el("button", "btn btn--secondary", "Cancel");
  cancelBtn.type = "button";
  cancelBtn.addEventListener("click", () => backdrop.remove());

  const submitBtn = el("button", "btn btn--primary", "Save Changes");
  submitBtn.type = "submit";
  actions.append(cancelBtn, submitBtn);

  form.append(nameGroup, phoneGroup, notesGroup, errorMsg, actions);
  modal.append(header, form);
  backdrop.append(modal);
  document.body.append(backdrop);

  if (typeof nameInput.focus === "function") {
    nameInput.focus();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    errorMsg.style.display = "none";

    const name = nameInput.value.trim();
    if (!name) {
      errorMsg.textContent = "Customer name is required.";
      errorMsg.style.display = "block";
      return;
    }

    submitBtn.disabled = true;
    setText(submitBtn, "Saving…");

    try {
      await updateCustomer(customer.id, {
        name,
        phone: phoneInput.value.trim() || undefined,
        notes: notesInput.value.trim() || undefined,
      });
      backdrop.remove();
      onDone();
    } catch (err: unknown) {
      submitBtn.disabled = false;
      setText(submitBtn, "Save Changes");
      errorMsg.textContent = err instanceof Error ? err.message : "Failed to update customer";
      errorMsg.style.display = "block";
    }
  });
}
