# ListenLedger Operational Runbook

This runbook documents the deployment architecture, disaster recovery procedures, edge hardware setup, and merchant operational protocols for ListenLedger pilots and production deployments.

---

## Current cloud deployment procedure

Use [DATABASE_MIGRATION.md](DATABASE_MIGRATION.md) for Render + Supabase. Initialize using an administrator, apply `scripts/supabase_setup.sql`, and run the tested Docker image with a restricted runtime role. `/readyz` checks the database; the image refuses SQLite fallback and does not apply migrations at startup. The edge process is not part of this deployment. Do not use mock/canned audio transcription for real sales.

The local/SQLite and hardware sections below are reference procedures, not proof of completed cloud deployment or supported microphone integration. Physical capture, model inference and shop trials still need real acceptance evidence.

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

### 3.4 Speech Model Runner & Real-Time Factor (RTF) Benchmarking
Verify local quantized inference and evaluate Real-Time Factor ($RTF \le 1.0$) on prototype hardware:
```sh
cd edge
# Run verification diagnostic on recorded counter audio:
./scripts/stt_runner.py -m models/ggml-tiny.en.bin -f /tmp/test_mic.wav --benchmark
```
Verify pipeline scenarios across standard clean, noisy, and negative chatter conditions:
```sh
cargo run --bin benchmark
```

### 3.5 Device Pairing
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

1. **Raw Voice Retention**:
   - The pilot build captures speech in the **browser**: it relies on the browser's speech recognition service, which is a cloud service in Chrome, so microphone audio may leave the device to the browser vendor's speech provider while it is transcribed.
   - ListenLedger does not receive, store or queue raw audio: the browser posts only the transcript, `POST /api/captures` clears any `audio_data` field, and no new snippet recordings are created. The parked edge appliance (`edge/`, Blueprint §17) keeps raw PCM in ephemeral memory buffers and zeroes them after framing; that design is not part of the browser pilot.
   - Legacy transaction audio already in the database remains selectable and playable until it is purged (item 2).
2. **Transcript Retention & Automated Purging**:
   - Spoken transcripts (e.g., “Two Milo, four thousand naira. Thank you.”) and legacy audio snippets are stored in the database solely for owner review and dispute adjudication under an agreed retention policy.
   - **Automated Scheduled Purge**: By default, the server runs an automated background retention worker (`-retention-interval 24h`, `-retention-days 7` / env `RETENTION_INTERVAL`, `RETENTION_DAYS`) that enforces deletion of transcripts and legacy audio older than the 7-day pilot retention window across all shops. The scheduler status and policy can be inspected via `GET /api/settings/retention-policy` and updated via `PUT /api/settings/retention-policy`. Setting `-retention-interval 0` disables automated background purging.
   - **Manual Purge**: An operator or owner can trigger immediate cleanup with `POST /api/settings/purge-transcripts`, optionally passing the window as a JSON body (`{"retention_days": 7}`, or `{"older_than": "<RFC3339>"}`) or query parameters (`?retention_days=7` / `?older_than=...`). When omitted, the handler defaults to the active retention policy window (7 days for the pilot). The handler permanently clears historical transcripts and legacy audio while preserving financial ledger movements, amounts, and product movements.
   - Transcripts can be audited and exported prior to purging via `GET /api/transactions/export?format=csv`.
3. **Spreadsheet Formula Injection Defense**:
   - CSV exports sanitize leading formula trigger characters (`=`, `+`, `-`, `@`, `\t`, `\r`) by prepending a single quote `'` to protect Excel and Google Sheets consumers.
4. **Physical Counter Placard Requirement**:
   - In physical shop trials, a standard customer notification placard must be placed beside the microphone. It must state that the browser's speech service may send microphone audio to its provider in order to transcribe it, and that ListenLedger keeps only the written transcript and never records or stores raw audio. Use the placard copy in [STAFF_TRAINING.md](STAFF_TRAINING.md).

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

### 6.1 Health Check Endpoints
- **Liveness probe (`GET /healthz`)**:
  ```json
  {
    "status": "ok",
    "ws_clients": 2,
    "ws_events_dropped": 0
  }
  ```
  If `ws_events_dropped` is increasing, check client network connectivity or adjust client reconnect backoff.
- **Readiness probe (`GET /readyz`)**:
  Verifies live database connectivity via active ping. Returns HTTP 200 `ready` when storage is connected, or HTTP 503 `unavailable` if database connection pool is unreachable.

### 6.2 Request Tracing & Panic Recovery
Every request carries an `X-Request-ID` header. If a transaction fails or panics, locate the matching request ID in structured server logs:
```sh
journalctl -u listenledger -f | grep "req_..."
```
The server includes built-in panic recovery (`api.Recoverer`) preventing server crashes from uncaught handler exceptions.

### 6.3 Operational Failure Modes & Recovery Procedures

#### A. Microphone Disconnect / Unplugged Hardware
- **Symptoms**: Edge log reports `Microphone capture inactive or disconnected`, `EdgeHealthReport.microphone_active = false`.
- **Mitigation & Recovery**:
  1. Inspect physical USB cable connection to the microphone.
  2. Run `arecord -l` to verify audio device enumeration.
  3. Edge engine will automatically attempt to reopen stream or prompt restart.
  4. Staff should immediately use the dashboard **Manual Entry fallback** (`Add Transaction` in `#/transactions`) to record counter sales until the hardware is restored.

#### B. Speech Model Initialization or Load Failure
- **Symptoms**: Edge engine fails startup or reports `stt_model_loaded = false`.
- **Mitigation & Recovery**:
  1. Check model weights path in `LISTENLEDGER_STT_MODEL`.
  2. Verify file integrity using `edge/scripts/download_model.sh tiny.en` to validate the SHA256 checksum.
  3. Ensure sufficient RAM (minimum 512MB free memory for `tiny.en` quantization).

#### C. Network Outage & Offline Capture Replay
- **Symptoms**: Edge engine cannot reach orchestrator; dashboard indicates `Offline / Reconnecting`.
- **Mitigation & Recovery**:
  1. Staff continue speaking normal sales and restocking transactions at the counter.
  2. Edge engine stores captures in the durable local queue (`~/.listenledger/queue.bin`, capacity 10,000 captures).
  3. Upon network restoration, the background delivery worker flushes the queue with exponential backoff and jitter.
  4. The server validates each capture against its persistent `Idempotency-Key` and intake hash, guaranteeing that no transaction or stock movement is ever duplicated.

#### D. Disk Space Warning & Queue Bounds
- **Symptoms**: Edge health report alerts `Queue capacity warning (90% full)`.
- **Mitigation & Recovery**:
  1. Check disk utilization (`df -h`).
  2. Restore network connectivity to allow pending queue drain.
  3. Bounded queue protects against out-of-memory or unbounded disk consumption by enforcing strict capacity limits.

#### E. Production HTTPS & Security Headers
- ListenLedger supports direct TLS serving as well as reverse-proxy termination:
  - **Direct HTTPS**: Launch backend with `-tls-cert /path/to/cert.pem -tls-key /path/to/key.pem` (or environment variables `LISTENLEDGER_TLS_CERT` and `LISTENLEDGER_TLS_KEY`). The server configures TLS 1.2+ minimum version and secure ciphers.
  - **Reverse Proxy**: Behind Caddy, Nginx, Render, or Cloudflare, the server respects `X-Forwarded-Proto: https` and applies strict HSTS (`Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`).
  - **Defensive Headers**: All HTTP responses include `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, and Content Security Policy (CSP).


