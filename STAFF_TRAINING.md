# ListenLedger — Staff Training & Cashier Protocol Guide

This guide establishes the operational protocols for shop cashiers and storekeepers participating in the ListenLedger controlled trials (Milestone 5: Evidence & Controlled Trials).

---

## 1. Cashier Spoken Protocols

The counter microphone operates in continuous RAM-buffered listening mode. It **never writes raw audio to disk** and only extracts transactions when an authoritative **boundary phrase** is spoken.

### Standard Sales Protocol
When serving a customer at the counter:
1. Announce the item quantity, product name, and total amount naturally to the customer.
2. Conclude the sale with the agreed closing boundary phrase: **"Thank you"**.

#### Examples
- **Single Item**:  
  > *"Two packs of Milo, five thousand naira. Thank you."*
- **Multiple Items**:  
  > *"One tin of Peak milk and one St Louis sugar, four thousand seven hundred. Thank you."*
- **Alternative Units**:  
  > *"Three bottles of Coke, nine hundred naira. Thank you."*

### Why the Boundary Phrase Matters
- **Transaction Demarcation**: The system treats the closing phrase as the definitive boundary of the transaction.
- **Noise Rejection**: Casual counter conversation, banter, phone calls, or general chatter without the phrase are discarded automatically by the local edge engine.

---

## 2. Storekeeper Restock Protocol

When goods arrive from a supplier or wholesale distributor:
1. Start the spoken record with the opening prefix: **"Stock received:"**
2. Read the delivered quantities and products aloud directly from the supplier invoice or delivery note.
3. Conclude with the restock boundary phrase: **"Stock received."**

#### Examples
- **Cartons / Bulk**:  
  > *"Stock received: five cartons of Milo from wholesaler, stock received."*
- **Crates**:  
  > *"Stock received: one crate of Coca-Cola bottles, stock received."*
- **Mixed Delivery**:  
  > *"Stock received: two cartons of Indomie chicken and three cartons of Peak, stock received."*

The inventory engine automatically translates packaged units (e.g., cartons, crates) into the shop's base stocking units (e.g., packs, bottles) using the catalogue packaging relationships.

---

## 3. Owner Daily Review Protocol

Even with high-precision acoustic models, ambiguous or low-confidence voice captures are automatically quarantined in the **Review Queue** (`#/transactions` or Overview).

### Daily Review Checklist
1. **Midday Check (1:00 PM)**: Open the dashboard on tablet or mobile phone.
2. **Inspect Review Queue**: Review any cards flagged with yellow **Review Required** badges.
3. **Take Action**:
   - **Confirm / Correct**: If the voice capture was legitimate but the product alias was not yet in the catalogue (e.g. customer said *"small powder"* instead of *"Milo 500g"*), click **Confirm**, select the correct product SKU, and adjust the quantity. Live stock will update immediately.
   - **Reject**: If the customer or staff uttered the phrase accidentally during non-commercial conversation, click **Reject**. The capture will be archived with zero stock effect.
4. **End of Day Reconciliation (8:00 PM)**:
   - Check the **Summary KPI banner** (Total Sales Revenue and Transaction Count).
   - Verify low-stock warning badges in **Inventory** (`#/inventory`).
   - Trigger the daily backup or confirm automated backup verification.

---

## 4. Troubleshooting & Offline Fallback

| Situation | Immediate Protocol | System Behavior |
|---|---|---|
| **Internet Outage** | Continue speaking sales normally with the closing phrase. | The edge Raspberry Pi stores encrypted captures in a local SQLite burst queue. Once WiFi restores, captures flush to the server automatically with idempotency keys. |
| **Accidental Double-Speech** | No action required. | Duplicate retry protections reject replayed identical audio bursts without double-counting inventory. |
| **Microphone Red Light Off** | Check USB connection or reboot Raspberry Pi. | Use manual entry on the dashboard (`Add Transaction` in `#transactions`) until hardware reboot completes. |
| **New Product Added to Shop** | Use **Import CSV** or **Add Product** in `#inventory`. | Provide common local nicknames under **Aliases** so staff speech matches immediately without retraining the AI model. |

