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
- `LISTENLEDGER_MODEL_PATH`: Path to quantized speech-to-text model binary (defaults to `models/ggml-tiny.en.bin`).

## Hardware Pilot Setup (Raspberry Pi & Linux Counter)

1. **Microphone Hardware Recommendation**:
   - Boundary microphone / USB conference speakerphone with hardware echo cancellation (e.g., Jabra Speak 410/510, Anker PowerConf S330, or a directional USB lapel microphone mounted near counter cash point).
   - Verify card index and device mapping:
     ```sh
     arecord -l
     ```
   - Test capture levels with 16kHz 16-bit mono PCM:
     ```sh
     arecord -D hw:1,0 -f S16_LE -r 16000 -c 1 -d 5 test_mic.wav
     ```

2. **Quantized Whisper Model Download**:
   - Download the validated `tiny.en` GGML model weights (verified via SHA256 checksum):
     ```sh
     ./scripts/download_model.sh tiny.en
     ```

3. **Pair Device with Store Dashboard**:
   - Go to dashboard **Settings → Paired Capture Devices → Pair New Device**.
   - Input device name (e.g., "Counter 1 Lapel Mic").
   - Export the returned token into environment:
     ```sh
     export LISTENLEDGER_DEVICE_TOKEN="ll_dev_..."
     export LISTENLEDGER_BACKEND_URL="https://your-shop-domain.com"
     ```

4. **Running the Edge Engine**:
   ```sh
   cargo run --release
   ```

## Local Speech-to-Text Runner & Hardware Benchmark

The edge capture engine includes an executable local inference and benchmarking driver (`scripts/stt_runner.py`):

```sh
# Run benchmark diagnostic on test audio:
./scripts/stt_runner.py -m models/ggml-tiny.en.bin -f test_mic.wav --benchmark
```

Reports Real-Time Factor (RTF = inference_latency / audio_duration), CPU architecture, core utilization, and transcript output. On standard prototype hardware (e.g. Raspberry Pi 4 Quad-Core Cortex-A72 @ 1.5GHz), `tiny.en` achieves an RTF $\le 0.45$ (more than $2\times$ faster than real-time speech).

To run the acoustic pipeline benchmark across all 7 clean, noisy, and negative chatter scenarios:
```sh
cargo run --bin benchmark
```

## Privacy & Audio Retention Guarantee

Per product blueprint §17:
- **Zero Raw Audio Retention**: Audio is captured into ephemeral ring buffers in volatile system RAM only.
- **Immediate Discard & Zeroing**: As soon as speech frames are processed and the voice activity boundary is detected, the raw PCM buffer is overwritten with zeros and freed.
- **No Remote Audio Transmission**: Raw audio waveforms never leave the physical edge hardware. Only structured text anchors and per-anchor confidence scores are sent to the orchestrator.

## Building & Testing

```sh
cargo test
cargo run --release
```

