# Edge Capture Engine (Rust)

Runs on the counter hardware (Android, laptop, or Raspberry Pi) and owns
everything between the microphone and a text payload.

Per blueprint §7A, this crate is responsible for:

- Real-time PCM audio capture
- Voice activity detection, so silence and background noise are not processed
- Speech-to-text using a quantized local model (e.g. Whisper via `whisper-cpp-plus`)
- Detecting the transaction boundary — the configurable closing phrase
- Extracting the four anchors: product, quantity, amount, boundary
- Packaging the extracted payload and handing it to the backend orchestrator
- Discarding raw audio once a transaction has been extracted (blueprint §17)

## Status

Directory placeholder. The Cargo workspace has not been initialised yet — that
is the next step for this component:

```sh
cargo init --name listenledger-edge
```

See `../TODO.md` for the full sequencing.
