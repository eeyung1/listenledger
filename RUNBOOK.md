# ListenLedger Operational Runbook

This runbook documents the deployment architecture, disaster recovery procedures, edge hardware setup, and merchant operational protocols for ListenLedger pilots and production deployments.

---

## 1. System Architecture & Deployment Topology

ListenLedger uses a partitioned, single-writer architecture designed for edge counters and shop networks:

```
[ Sales Counter ]
  Physical Microphone (USB Boundary / Lapel)
         ↓
  Edge Engine (`edge/` - Rust)
  - 16kHz PCM Framing & Energy VAD
  - In-memory Quantized STT (Whisper tiny.en)
  - Zero-audio retention (buffers zeroed & discarded)
  - Durable Disk Queue (`~/.listenledger/queue.bin`)
         ↓ HTTPS POST /api/captures (Bearer token + Idempotency-Key)
[ Orchestrator Server ]
  Reverse Proxy (Caddy / Nginx with TLS 1.3 Termination)
         ↓ HTTP/1.1 + WebSockets (/ws)
  Go Orchestrator (`backend/` - Go HTTP Server)
  - Request ID Tracing (`X-Request-ID`) & Panic Recovery
  - Server Recognition & Validation Engine (`backend/validate`)
  - WebSocket Realtime Hub (`/ws`)
         ↓ Local File / NVMe
  SQLite Database (`listenledger.db`)
  - Single-writer WAL mode (`journal_mode=WAL`, `_txlock=immediate`)
  - Embedded versioned migrations
  - Atomic stock movement & ledger idempotency
```

### Supported Deployment Targets
- **Single-Store Local Deployment**: Go backend and SQLite run directly on a shop local server or mini-PC (e.g., Intel N100 / Raspberry Pi 5). Edge capture runs on the counter and reaches `http://127.0.0.1:8080` or `http://192.168.1.X:8080`.
- **Cloud-Hosted Orchestrator**: Go backend runs on a hardened Linux VM (Debian/Ubuntu) behind Caddy. SQLite runs on a persistent NVMe volume. Edge devices connect over outbound HTTPS.

> [!IMPORTANT]
> **SQLite Deployment Invariant**: SQLite requires a local filesystem with POSIX file-locking semantics. Never place the SQLite database on network filesystems (NFS, SMB, GlusterFS) which do not guarantee atomic WAL locks.

---

## 2. Backup & Disaster Recovery

### 2.1 Automated Online Backup
SQLite supports online, hot backups via `VACUUM INTO` without interrupting concurrent reads or writes.

Schedule automated backups via cron or systemd timer (e.g., hourly):
```sh
# /etc/cron.hourly/listenledger-backup
sqlite3 /var/lib/listenledger/listenledger.db \
  "VACUUM INTO '/var/backups/listenledger/backup-$(date +\%Y\%m\%d-\%H\%M\%S).db';"
```

### 2.2 Backup Verification Script
Run the automated verification script to validate backup snapshots against corruption:
```sh
./scripts/verify_backup_restore.sh /path/to/listenledger.db
```
The script:
1. Performs an online hot snapshot using `VACUUM INTO`.
2. Executes `PRAGMA integrity_check;`.
3. Restores into an isolated clean environment.
4. Verifies exact row count parity across all tables (`products`, `transactions`, `stock_movements`, `devices`, `shops`, `owners`).

### 2.3 Restoring from Backup
In case of server hardware failure:
1. Stop the backend service:
   ```sh
   systemctl stop listenledger
   ```
2. Replace the corrupted database file with the latest verified backup:
   ```sh
   cp /var/backups/listenledger/backup-YYYYMMDD-HHMMSS.db /var/lib/listenledger/listenledger.db
   ```
3. Run SQLite integrity check:
   ```sh
   sqlite3 /var/lib/listenledger/listenledger.db "PRAGMA integrity_check;"
   ```
4. Restart the backend service:
   ```sh
   systemctl start listenledger
   ```
5. Any in-flight transactions queued on edge devices will automatically replay safely without duplicating inventory movements due to `Idempotency-Key` deduplication.

---

## 3. Edge Hardware & Microphone Setup

### 3.1 Recommended Hardware
- **Counter Microphone**: Directional boundary microphone or USB speakerphone with hardware noise suppression (e.g. Jabra Speak 410/510, Anker PowerConf S330, or Fifine K669B).
- **Placement**: Placed on the sales counter between 0.5m and 1.5m from the primary salesperson, angled away from street background noise.

### 3.2 Linux Audio Driver Configuration
List available audio capture devices:
```sh
arecord -l
```
Test 16kHz 16-bit mono audio recording:
```sh
arecord -D plughw:1,0 -f S16_LE -r 16000 -c 1 -d 5 /tmp/test_mic.wav
```
Adjust capture volume if speech is too quiet or clipping:
```sh
alsamixer
```

### 3.3 Model Weight Setup
Quantized Whisper model weights must be downloaded and verified prior to running:
```sh
cd edge
./scripts/download_model.sh tiny.en
```

### 3.4 Device Pairing
1. In the ListenLedger dashboard, navigate to **Settings → Paired Capture Devices**.
2. Enter the device name (e.g. `Counter 1 Jabra Mic`) and click **Pair New Device**.
3. Securely copy the generated bearer token.
4. Set the environment variable on the edge hardware:
   ```sh
   export LISTENLEDGER_DEVICE_TOKEN="ll_dev_..."
   export LISTENLEDGER_BACKEND_URL="https://shop.example.com"
   ```
5. Start the edge engine:
   ```sh
   cargo run --release
   ```

---

## 4. Privacy & Data Protection Protocols

1. **Zero Raw Voice Retention**:
   - In accordance with Blueprint §17, raw PCM audio exists solely in ephemeral memory buffers during speech.
   - Upon detecting voice boundary silence or the closing phrase, the audio buffer is overwritten with zeros and deallocated.
   - Raw audio is **never** written to local disk or transmitted across network connections.
2. **Transcript Retention**:
   - Spoken transcripts (e.g., “Two Milo, four thousand naira. Thank you.”) are stored in the SQLite ledger to enable owner adjudication in the Review Queue.
   - Transcripts can be audited and exported via `GET /api/transactions/export?format=csv`.
3. **Spreadsheet Formula Injection Defense**:
   - CSV exports sanitize leading formula trigger characters (`=`, `+`, `-`, `@`, `\t`, `\r`) by prepending a single quote `'` to protect Excel and Google Sheets consumers.

---

## 5. Staff Operational Protocol & Pilot Training

### 5.1 Standard Phrase Structure
Sales and restocking staff must follow structured speech boundaries:

| Transaction Type | Required Structure | Example | Effect |
|---|---|---|---|
| **Customer Sale** | `[Quantity] [Product Name], [Total Price] naira. Thank you.` | *"Two Milo, four thousand naira. Thank you."* | Decreases stock by 2; records confirmed revenue NGN 4,000. |
| **Supplier Restock** | `[Quantity] [Unit] of [Product Name], [Total Cost] naira. Stock received.` | *"Three cartons of Milo, forty-five thousand naira. Stock received."* | Converts cartons to packs; increases stock; records purchase cost. |

### 5.2 Adjudicating the Review Queue
When background noise or unfamiliar pronunciations lower recognition confidence:
1. The capture appears in the dashboard **Review Queue** with per-anchor confidence scores and the verbatim transcript.
2. The transaction has **zero effect on stock or revenue** while in review.
3. The owner reviews the card:
   - Click **Confirm** to accept candidate values and apply inventory movements.
   - Click **Reject** to discard inaccurate captures without touching inventory.

---

## 6. Service Health Monitoring & Troubleshooting

### 6.1 Health Check Endpoint
Query `GET /healthz`:
```json
{
  "status": "ok",
  "ws_clients": 2,
  "ws_events_dropped": 0
}
```
- If `ws_events_dropped` is increasing, check client network connectivity or adjust client reconnect backoff.

### 6.2 Request Tracing
Every request carries an `X-Request-ID` header. If a transaction fails or panics, locate the matching request ID in structured server logs:
```sh
journalctl -u listenledger -f | grep "req_..."
```
The server includes built-in panic recovery (`api.Recoverer`) preventing server crashes from uncaught handler exceptions.
