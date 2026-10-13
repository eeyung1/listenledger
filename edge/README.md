# Edge Capture Engine (Rust)

Runs on the sales counter hardware (Android, laptop, or Raspberry Pi) and owns
everything between the microphone and an authenticated intake payload delivered
to the ListenLedger backend.

Per blueprint §7A & §17, this crate implements:

- **Audio Framing & VAD (`src/audio.rs`)**: Real-time PCM audio buffer management and energy-based Voice Activity Detection to ignore ambient silence.
- **Privacy Assurance (`src/audio.rs`)**: In-memory audio buffers are zeroed and discarded immediately following transcription; no raw voice data is stored on disk (blueprint §17).
- **Speech-to-Text (`src/stt.rs`)**: Interface for local quantized speech-to-text models (such as Whisper via quantized GGUF/C++ bindings) with benchmarking fixtures.
- **Boundary & Anchor Extraction (`src/extractor.rs`)**: Detects the configured closing phrase ("Thank you" for sales, "Stock received" for restocks) and structures candidate product, quantity, and amount anchors.
- **Durable Offline Queue (`src/queue.rs`)**: Disk-backed bounded queue surviving restarts and power interruptions; keeps stable capture IDs and removes items only upon backend acknowledgment.
- **Authenticated Delivery (`src/client.rs`)**: Posts payloads to `POST /api/captures` with device bearer tokens and shop-scoped `Idempotency-Key` headers.

## Configuration & Environment

- `LISTENLEDGER_BACKEND_URL`: URL of the orchestrator API (defaults to `http://127.0.0.1:8080`).
- `LISTENLEDGER_DEVICE_TOKEN`: Secret token obtained from `POST /api/devices` during device pairing.
- `LISTENLEDGER_QUEUE_FILE`: Path to persistent queue storage (defaults to `captures_queue.jsonl`).

## Building & Testing

```sh
cargo test
cargo run --release
```
