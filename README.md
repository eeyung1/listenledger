# ListenLedger: Ambient Sales Capture System

## Overview
ListenLedger is an ambient, voice-driven sales and inventory system designed for small and local businesses[cite: 1]. It replaces manual POS data entry by using the natural sales conversation as the data-entry interface[cite: 1]. The system listens for structured transactional anchors, processes the speech locally, and updates a live business dashboard in near real-time[cite: 1].

**Core Principle:** Conversation becomes the data-entry interface[cite: 1].

## Core Transaction Protocol
Sales staff are trained to include three anchors followed by a closing phrase[cite: 1]:
1. **Product**[cite: 1]
2. **Quantity**[cite: 1]
3. **Amount**[cite: 1]
4. **Boundary:** "Thank you" (Configurable)[cite: 1]

*Example:*
> **Customer:** "I want Milo."[cite: 1]
> **Seller:** "How many?"[cite: 1]
> **Customer:** "Two."[cite: 1]
> **Seller:** "Two Milo, four thousand naira. Thank you."[cite: 1]

## System Architecture

The architecture separates heavy, real-time audio processing from concurrent business logic and fast UI rendering.

### 1. Edge Capture Engine (Rust)
Runs locally on affordable hardware (Android, laptop, Raspberry Pi) to ensure fast, offline-capable processing without dropping audio frames[cite: 1].
* **Audio Capture:** Real-time PCM audio streaming[cite: 1].
* **Voice Activity Detection (VAD):** Filters out background noise, triggering processing only during speech[cite: 1].
* **Speech-to-Text:** Utilizes optimized, quantized models (e.g., Whisper via `whisper-cpp-plus`) for rapid, localized transcription[cite: 1].
* **Data Handoff:** Packages the extracted text payload and transmits it to the backend orchestrator.

### 2. Backend Orchestrator (Go)
Handles high-throughput, concurrent request-response cycles, managing the state and database operations for multiple businesses.
* **Validation Engine:** Matches transcribed text against the specific shop's product catalogue and alias database (e.g., mapping "Big Peak" to "Peak Milk 400g")[cite: 1].
* **Confidence Scoring:** Flags low-confidence captures for manual review instead of blindly updating the database[cite: 1].
* **Inventory Management:** Processes both **Stock Out** (ambient sales) and **Stock In** (reading supplier receipts aloud)[cite: 1].
* **Live Push:** Uses WebSockets to broadcast successful database updates to the client.

### 3. Frontend Dashboard (HTML, CSS, TypeScript)
A lightweight, mobile-first Single Page Application (SPA) acting as the live command center[cite: 1].
* **DOM Updates:** Listens for WebSocket events to instantly update "Sales Today," current inventory, and the transaction feed without page reloads[cite: 1].
* **Review Queue:** A dedicated interface for merchants to quickly confirm or correct flagged, low-confidence transactions[cite: 1].
* **Catalogue Management:** Interface for owners to map official SKUs to common local pronunciations and define packaging relationships (e.g., 1 carton = 12 packs)[cite: 1].

## Dashboard Views (SPA Routing)

The web application utilizes a single-page architecture built around a core `layout.html` shell, navigating smoothly between the following views without full page reloads:

* **Authentication & Onboarding (`login.html` & `signup.html`):** The secure entry point. Handles initial configuration (currency, custom closing phrases) and pairs the edge capture device to the owner's workspace.
* **The Overview / Command Center (`home.html`):** A live event feed showing transactions happening in real-time, daily KPIs (Sales Today, Number of Transactions), and high-priority alerts like low stock[cite: 1].
* **Transactions & Review System (`transactions.html`):** The unified ledger for all incoming and outgoing stock[cite: 1]. Contains the critical "Review Queue" for low-confidence captures (e.g., a 55% match), allowing the owner to manually correct and confirm or reject records[cite: 1].
* **Inventory & Catalogue Management (`inventory.html` / `search.html`):** The database interface to manage official product names, SKUs, pricing, and packaging logic[cite: 1]. Includes functionality to configure custom speech aliases so the system learns the shop's local jargon[cite: 1].
* **Analytics (`analytics.html`):** Visualizations of historical sales data, product movement trends, and transaction volumes[cite: 1].
* **Settings (`settings.html`):** Configuration controls for the NLP pipeline, confidence score thresholds, hardware management, and staff protocol settings[cite: 1].

## The Processing Loop
1. **HEAR:** Rust edge node captures and isolates speech[cite: 1].
2. **UNDERSTAND:** Rust transcribes product, quantity, amount, and boundary[cite: 1].
3. **VALIDATE:** Go backend maps text to SKUs, checks inventory limits, and assigns a confidence score[cite: 1].
4. **RECORD:** Go writes the transaction to the database or routes to the Review Queue[cite: 1].
5. **UPDATE:** Go pushes WebSocket events; TypeScript updates the live UI[cite: 1].

## Project Phases
1. **Software Prototype:** Prove the NLP extraction and Rust/Go data flow using existing hardware[cite: 1].
2. **Real-Shop Beta (1-2 Shops):** Measure end-to-end transaction accuracy, acoustic interference, and behavioral compliance[cite: 1].
3. **Refinement:** Tune VAD, confidence thresholds, and catalog aliases based on pilot data[cite: 1].
4. **Expansion (10-15 Shops):** Validate multi-tenant backend architecture and generate commercial viability metrics[cite: 1].
5. **Hardware Integration:** Transition from prototype edge devices to dedicated ListenLedger rechargeable hardware[cite: 1].