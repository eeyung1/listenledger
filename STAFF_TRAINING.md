# ListenLedger — Staff Training & Cashier Protocol Guide

This guide establishes the operational protocols for shop cashiers and storekeepers participating in the ListenLedger controlled trials (Milestone 5: Evidence & Controlled Trials).

> **Pilot scope:** the current build runs entirely in the **browser** on the counter phone or tablet. Speech is transcribed by the **browser's speech recognition service**, which may send microphone audio to its provider over the internet to do that. ListenLedger itself never receives, stores or uploads raw audio. References below to a dedicated edge capture device, hardware mute button or USB microphone describe the parked appliance design, not the browser build.

---

## 1. Cashier Spoken Protocols

The counter microphone is the phone or tablet running the ListenLedger web app. The browser's speech recognition service transcribes speech as it is spoken; ListenLedger itself **never writes raw audio to disk** and only extracts transactions when an authoritative **boundary phrase** is spoken.

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

### Using Your Mobile Phone as the Counter Microphone
1. Open the ListenLedger web application on your smartphone (Google Chrome on Android or Safari on iOS).
2. On the **Overview** dashboard (`#/home`), locate the **Counter Microphone** dock right under the header.
3. Tap **"Start Listening"** and tap **"Allow"** when prompted by your browser for microphone permissions.
4. Place the phone on the cash counter or on a desk stand with the microphone unblocked.
5. Speak sales phrases naturally as you conclude transactions with customers.
6. When you say **"Thank you"**, the phone immediately validates the sale, logs the transaction in the ledger, updates the live feed, and plays a brief confirmation chime.

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
| **Internet Outage** | Continue speaking sales normally with the closing phrase. | The edge capture device stores captures in a capacity-limited local JSON-lines queue (`~/.listenledger/queue.bin`). Once connectivity restores, captures flush to the server automatically with exponential backoff and idempotency deduplication. |
| **Accidental Double-Speech** | No action required. | Duplicate retry protections reject replayed identical audio bursts without double-counting inventory. |
| **Microphone Red Light Off** | Check USB connection or reboot edge device. | Use manual counter entry on the dashboard (`Add Transaction` in `#transactions`) until hardware reboot completes. |
| **New Product Added to Shop** | Use **Import CSV** or **Add Product** in `#inventory`. | Provide common local nicknames under **Aliases** so staff speech matches immediately without retraining the AI model. |

---

## 5. Counter Privacy Notice & Customer Placard Protocol

Customer transparency and privacy trust are paramount. A prominent counter placard must be placed beside the microphone on every sales counter.

### Official Physical Placard Copy

```
┌────────────────────────────────────────────────────────┐
│          LISTENLEDGER VOICE-ASSISTED COUNTER           │
│                                                        │
│  This counter uses speech recognition to automatically │
│  log sales and stock movements.                        │
│                                                        │
│  • Speech is transcribed by the browser's speech       │
│    service, which may send microphone audio to its     │
│    provider over the internet to do so.                │
│  • ListenLedger does not record or store voice         │
│    recordings; it keeps only the written transcript.   │
│  • Only line items, quantities, and totals are logged. │
│  • Transcripts are kept for owner review and are       │
│    cleared by the owner's retention cleanup            │
│    (30-day default).                                   │
└────────────────────────────────────────────────────────┘
```

### Customer Communication Script
If a customer inquires about the microphone or counter device:
- **Staff Response**: *"It's an automated digital receipt helper. It only listens for the items and total when we conclude the sale with 'Thank you'. The speech is turned into text by the browser's speech service, which may send the audio to its provider over the internet to do that. ListenLedger itself doesn't save voice recordings — it keeps a written transcript that the shop owner can clear."*

### Physical Pause / Mute Button Protocol
- When staff take private phone calls, resolve personal matters, or step away from the counter:
  - Press the hardware **Mute / Pause button** on the microphone (the LED indicator turns solid red / session state displays `PAUSED`).
  - While paused, the edge engine drops all audio input and halts capture session framing.
  - Press again to un-mute when resuming customer service.

---

## 6. Historical Data Retention & Purging

- **30-Day Transcript Cutoff**: Spoken transcripts attached to historical transactions are maintained solely for owner review and dispute adjudication. The default retention window is 30 days.
- **Purging**: There is no scheduled purge job; the owner (or an operator) triggers cleanup explicitly with `POST /api/settings/purge-transcripts`, passing the window either as a JSON body (`{"retention_days": 30}`, the default, or `{"older_than": "<RFC3339>"}`) or as the equivalent query parameters (`?retention_days=30` / `?older_than=...`). This permanently blanks historical transcripts while preserving ledger amounts, SKUs, and stock audit trails.


