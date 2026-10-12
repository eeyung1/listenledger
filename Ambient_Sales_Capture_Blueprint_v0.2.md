# Ambient Sales Capture System

## Product Blueprint — v0.2

## 1. Product Concept

Build an **ambient, voice-driven sales and inventory system for small and local businesses**.

The final product combines a rechargeable physical device placed near the point of sale with software that understands structured sales conversations and a live dashboard for the business owner.

The salesperson should not need to type every sale into a POS, scan every product, press a microphone button, or send a voice note. Instead, the device listens for the structured part of the normal sales conversation and converts it into business data.

**Core principle: Conversation becomes the data-entry interface.**

---

## 2. Problem

Many small businesses struggle to maintain accurate records of:

- Products sold
- Quantities sold
- Selling prices
- Current inventory
- Daily revenue
- Cash flow

Traditional inventory and POS systems still depend heavily on someone correctly entering each transaction. In a busy shop, transactions can go unrecorded, be entered incorrectly, or only be reconstructed later.

However, conversation already happens during almost every transaction.

This product turns that existing conversation into structured business data.

---

## 3. Core Transaction Protocol

Sales staff are trained to clearly include three anchors:

1. **Product**
2. **Quantity**
3. **Amount**

A closing phrase such as **“Thank you”** marks the completion of the transaction.

Example:

> Customer: “I want Milo.”  
> Seller: “How many?”  
> Customer: “Two.”  
> Seller: “Two Milo, four thousand naira.”  
> Customer pays.  
> Seller: “Thank you.”

Expected result:

```text
Product: Milo
Quantity: 2
Amount: NGN 4,000
Status: Completed
```

The closing phrase should eventually be configurable for different shops, languages, and selling styles.

---

## 4. Core Processing Loop

```text
HEAR
  ↓
UNDERSTAND
  ↓
VALIDATE
  ↓
RECORD
  ↓
UPDATE
```

### Hear

Capture relevant speech around the sales point.

### Understand

Extract:

- Product
- Quantity
- Amount
- Transaction boundary

### Validate

Check that:

- The product exists
- The quantity is sensible
- The amount is plausible
- Recognition confidence is high enough

### Record

Create the transaction.

### Update

Immediately update:

- Inventory
- Sales totals
- Transaction history
- Business dashboard

Example:

```text
Before:
Milo stock = 20
Today's sales = NGN 50,000

Transaction:
Milo × 2 = NGN 4,000

After:
Milo stock = 18
Today's sales = NGN 54,000
```

---

## 5. Software First, Hardware as the Integrated Product

The commercial vision is **hardware + software**.

Development starts by proving the software intelligence before spending money manufacturing custom hardware.

Prototype hardware can initially be:

- Android phone
- Laptop
- Raspberry Pi
- External microphone
- Other inexpensive development hardware

The first question to prove is:

> **Can software reliably convert a structured real-world sales conversation into an accurate transaction?**

Development path:

```text
Software Prototype
       ↓
Test With Existing Hardware
       ↓
Real-Shop Testing
       ↓
Improve Accuracy
       ↓
Prove the System
       ↓
Design/Select Dedicated Hardware
       ↓
Integrated Hardware + Software Product
```

---

## 6. Final Hardware Vision

After the software has been validated, the system can become a compact rechargeable device designed specifically for shops.

Possible characteristics:

- Rechargeable battery
- Always-ready microphone
- Noise reduction
- Wi-Fi and/or mobile connectivity
- Local/offline processing where practical
- Simple status indicators
- Secure connection to the owner's account
- Easy placement near a sales counter

The merchant experience should eventually be:

```text
Charge it.
Place it.
Connect it.
Train staff on the transaction protocol.
Start receiving business information.
```

---

## 7. Software Architecture

### A. Capture Engine

Responsibilities:

- Audio capture
- Voice activity detection
- Speech-to-text
- Transaction detection
- Product extraction
- Quantity extraction
- Amount extraction
- Closing-phrase detection
- Product/SKU matching
- Confidence scoring
- Validation
- Transaction creation
- Inventory updates

### B. Owner Dashboard

The owner receives a mobile-first dashboard showing the business in near real time.

Initial information:

- Sales today
- Number of transactions
- Recent transactions
- Current inventory
- Low-stock products
- Transactions requiring review
- Product movement

Later versions can include:

- Gross profit estimates
- Expenses
- Cash-flow analysis
- Best-selling products
- Sales trends
- Supplier information
- Credit/debt sales
- Multi-shop analytics
- Daily, weekly, and monthly reports

---

## 8. Initial Dashboard

Keep V1 simple.

Main sections:

1. Overview
2. Transactions
3. Inventory
4. Analytics
5. Settings

The Overview should feel like a **live command center for the business**, rather than complicated accounting software.

---

## 9. Product Catalogue

Each business maintains its own catalogue.

For every product:

- Official product name
- SKU
- Selling price
- Current quantity
- Alternative names
- Common local pronunciations

Example:

```text
Official product:
Peak Milk 400g

Aliases:
Peak
Peak tin
Peak milk
Big Peak
```

This allows the system to learn how products are actually described in that shop.

---


## 10. Voice-Based Stock Intake / Restocking

The system must support inventory moving **into** the business as well as inventory moving **out** through sales.

When new goods arrive from a supplier, the seller or storekeeper should be able to read the purchase receipt aloud instead of manually entering every item.

Example:

> “Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Ten packs of Indomie, thirty thousand naira. Stock received.”

The system should recognize this as a **purchase/restock event**, not a customer sale.

Expected structured result:

```text
RESTOCK / PURCHASE

Pepsi
Quantity received: 5 packs
Purchase cost: NGN 12,000

Milo
Quantity received: 3 cartons
Purchase cost: NGN 45,000

Indomie
Quantity received: 10 packs
Purchase cost: NGN 30,000

Total purchase cost: NGN 87,000
Status: Received
```

The inventory engine then increases stock automatically.

Example:

```text
Before                 After

Pepsi:     8 packs  →  13 packs
Milo:      4 cartons → 7 cartons
Indomie:   6 packs  →  16 packs
```

### Two-Way Inventory Flow

The core inventory model therefore has two directions:

```text
STOCK IN
Supplier purchase / restock
        ↓
Seller reads receipt aloud
        ↓
System extracts products, quantities and costs
        ↓
Seller confirms / closes the restock
        ↓
Inventory increases
        ↓
Purchase/cost records update


STOCK OUT
Customer purchase
        ↓
Sales conversation
        ↓
System extracts products, quantities and amount
        ↓
Transaction closes
        ↓
Inventory decreases
        ↓
Sales/revenue records update
```

This is a core feature, not merely a future enhancement.

### Restock Validation

Before changing inventory, the system should validate:

- Product identity
- Quantity received
- Unit type, such as pack, carton, bottle, piece, or crate
- Purchase cost
- Whether the product already exists in the catalogue
- Recognition confidence

If confidence is low, the restock should go to **Review Required** instead of silently changing inventory.

For a new product, the system can ask for confirmation before adding it to the catalogue.

### Units and Packaging

The inventory model must understand packaging relationships.

Example:

```text
1 carton of Pepsi = 12 packs
1 pack = 6 bottles
```

These relationships are business/product-specific and should be configurable.

This allows the system to understand that receiving five packs increases stock differently from receiving five individual bottles.

### Closing a Restock

A separate phrase should distinguish inventory intake from a sale.

Initial examples:

- “Stock received.”
- “Restock complete.”
- “Purchase received.”

The exact phrase should be configurable.

### Business Value

Once the system understands both stock coming in and stock going out, it can build a much more accurate picture of the business:

- Quantity purchased
- Quantity sold
- Quantity remaining
- Purchase cost
- Sales revenue
- Cost of goods sold
- Gross-profit estimates
- Stock movement
- Reorder requirements
- Cash flowing into and out of the business

### Future Receipt Capture

Reading the supplier receipt aloud is the initial workflow.

A later version can also support:

```text
Photograph supplier receipt
        ↓
Extract products, quantities and costs
        ↓
Seller reviews
        ↓
Confirm
        ↓
Inventory updates
```

Both voice intake and receipt-image intake should eventually feed the same inventory engine.

---

## 11. Confidence and Review System

The system should not silently guess.

Example:

```text
Product: Milo — 98%
Quantity: 2 — 99%
Amount: NGN 4,000 — 98%
```

This transaction can potentially be recorded automatically.

But:

```text
Product: Milo — 55%
Quantity: 2 — 98%
Amount: NGN 4,000 — 97%
```

should become:

```text
REVIEW REQUIRED
```

Possible states:

- Confirmed
- Review Required
- Rejected
- Cancelled

Owners or authorized staff should be able to correct transactions quickly.

---

## 12. V1 Scope

Do not start by building everything.

V1 proves both inventory directions:

```text
SALE / STOCK OUT
Hear sale
→ Understand sale
→ Validate sale
→ Record sale
→ Deduct stock
→ Update dashboard
→ Allow correction

RESTOCK / STOCK IN
Hear receipt being read
→ Understand products, quantities and purchase costs
→ Validate restock
→ Confirm stock received
→ Increase inventory
→ Update purchase/cost records
→ Update dashboard
→ Allow correction
```

Initial environment:

- One shop
- One sales point
- One primary salesperson
- One microphone
- Approximately 20–50 known products
- English first
- Product + Quantity + Amount protocol

---

## 13. Real-Shop Testing Before Launch

The system should be proven in real businesses before a major launch.

### Stage 1

Build the working prototype.

### Stage 2

Test in 1–2 controlled shops.

Compare every captured transaction against what actually happened.

Measure:

- Transactions correctly detected
- Transactions missed
- False transactions
- Product accuracy
- Quantity accuracy
- Amount accuracy
- Complete transaction accuracy
- Correction rate
- Processing latency
- Staff protocol compliance

### Stage 3

Use failures to improve the system.

### Stage 4

Expand to approximately 5 shops.

### Stage 5

Improve again using real-world data.

### Stage 6

Expand to approximately **10–15 shops**.

### Stage 7

Analyze the accumulated results and prepare for launch.

During the pilot, the system should initially run alongside the shop's existing recordkeeping rather than immediately becoming its only authoritative record.

---

## 14. Staff Training

Staff should receive a very short practical training.

The required spoken anchors are:

```text
PRODUCT
QUANTITY
AMOUNT
```

followed by the agreed closing phrase.

Example:

> “Two Milo, four thousand naira. Thank you.”

The goal is not to make employees speak unnaturally. It is to introduce a small, predictable verbal habit that gives the software reliable anchors.

---

## 15. Data Before Launch

The pilot shops provide evidence, not merely early users.

Useful pre-launch data includes:

- Number of shops tested
- Number of transactions processed
- End-to-end transaction accuracy
- Product recognition accuracy
- Quantity recognition accuracy
- Amount recognition accuracy
- False-positive rate
- Correction rate
- Average processing latency
- Staff protocol compliance
- Accuracy improvements between software versions

This evidence can support:

- Product decisions
- Launch claims
- Customer acquisition
- Hardware decisions
- Funding conversations

---

## 16. Funding and Scale Path

```text
Idea
 ↓
Prototype
 ↓
Real-Shop Testing
 ↓
Measured Data
 ↓
Improved Product
 ↓
10–15 Shop Beta
 ↓
Launch
 ↓
Funding / Scale
```

The objective is to enter funding conversations with evidence from real-world usage rather than only an idea or presentation.

---

## 17. Privacy by Design

An ambient-listening product requires careful privacy design.

The system should capture and retain only what is necessary for transaction processing.

Preferred direction:

```text
Audio
 ↓
Detect Relevant Speech
 ↓
Extract Transaction
 ↓
Store Structured Transaction Data
 ↓
Discard Unnecessary Raw Audio
```

Where practical, processing should happen locally on the device.

The production system will need clear controls and policies for:

- Audio processing
- Audio retention
- User access
- Security
- Appropriate notice/consent
- Applicable data-protection requirements

---

## 18. Key Technical Challenges

The pilot must test rather than assume solutions for:

- Background noise
- Nigerian accents
- Pidgin and eventually local languages
- Similar product names
- Local product pronunciations
- Multiple simultaneous speakers
- Price ambiguity
- Transaction boundaries
- Cancelled transactions
- Customers changing quantities
- Discounts
- Credit transactions
- Multiple products per transaction
- False positives

The earliest version should deliberately target **one counter and one primary salesperson** rather than a crowded supermarket.

---

## 19. Critical Hypotheses

The project must test:

**H1:** Salespeople will consistently use the Product + Quantity + Amount protocol.

**H2:** Speech recognition can reach useful accuracy in the target shop environment.

**H3:** A closing phrase can reliably identify transaction completion.

**H4:** The system can distinguish sales from unrelated conversation.

**H5:** Shop owners value automatically generated inventory and sales records enough to adopt the product.

**H6:** Correcting occasional errors requires substantially less work than manually entering every transaction.

---

## 20. Future Capabilities

After proving ambient transaction capture:

- Expense capture
- Debt and credit sales
- Customer accounts
- Supplier purchases
- Profit estimates
- Automatic reorder alerts
- Daily spoken summaries
- Anomaly detection
- Multiple counters
- Multiple languages
- Offline processing
- Accounting integrations
- Mobile owner dashboard
- WhatsApp reports
- Multi-branch business monitoring

---

## 21. Possible Business Model

Models to test include:

- Hardware purchase + monthly software subscription
- Low-cost device rental + monthly subscription
- Software-only plan using supported Android hardware

Pricing should be determined after interviewing businesses and measuring the economic value delivered.

---

## 22. Initial Target Customer

Start narrow.

Ideal pilot businesses have:

- Frequent verbal transactions
- One primary sales point
- Manageable product catalogues
- Incomplete/manual inventory records
- Enough transactions that manual recording is burdensome
- Owners willing to participate in testing

Do not initially target large supermarkets with many simultaneous checkout lanes.

---

## 23. North-Star Experience

The eventual experience is:

**The hardware listens.**

**The software structures the transaction.**

**The inventory updates.**

**The analytics update.**

**The owner sees what is happening in the business.**

At any moment, the owner should eventually be able to ask:

- What have we sold today?
- How much revenue have we generated?
- What stock is running low?
- What products are selling fastest?
- Which transactions need review?
- How is today's business performing compared with previous periods?

The business gains accurate records from conversations that were already happening.

---

## 24. Immediate Development Goal

Do **not** begin by manufacturing custom hardware.

First prove:

> **Can a microphone + software reliably turn a structured real-world sales conversation into an accurate inventory transaction without manual data entry?**

If yes, the hardware, analytics, dashboard, and commercial product can be built around that capability.

If not, the pilot data should reveal exactly where the system fails so it can be improved.
