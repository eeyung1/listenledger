use chrono::Utc;
use listenledger_edge::audio::{
    list_audio_devices, AudioConfig, AudioInputSource, CaptureSession,
    CaptureSessionState, MockAudioSource,
};
use listenledger_edge::client::CaptureClient;
use listenledger_edge::extractor::{
    detect_boundary_with_config, extract_lines_from_transcript, BoundaryConfig,
    CapturePayload,
};
use listenledger_edge::queue::DurableQueue;
use listenledger_edge::stt::{MockSpeechRecognizer, SpeechRecognizer};
use std::env;
use std::path::PathBuf;
use std::time::Duration;
use uuid::Uuid;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt::init();

    let backend_url = env::var("LISTENLEDGER_BACKEND_URL")
        .unwrap_or_else(|_| "http://127.0.0.1:8080".to_string());
    let device_token = env::var("LISTENLEDGER_DEVICE_TOKEN")
        .unwrap_or_else(|_| "dev_test_token".to_string());
    let device_id = env::var("LISTENLEDGER_DEVICE_ID")
        .unwrap_or_else(|_| "dev_edge_runner".to_string());
    let queue_path = env::var("LISTENLEDGER_QUEUE_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from("captures_queue.jsonl"));

    println!("Starting ListenLedger Edge Capture Engine");
    println!("Connecting to: {}", backend_url);
    println!("Offline queue at: {:?}", queue_path);

    if let Ok(devices) = list_audio_devices() {
        println!("Available Audio Capture Devices:");
        for dev in devices {
            let def_str = if dev.is_default { " (default)" } else { "" };
            println!("  - [{}] {}{}", dev.id, dev.name, def_str);
        }
    }

    let client = CaptureClient::new(&backend_url, &device_token);
    let mut queue = DurableQueue::open(&queue_path, 1000)?;

    // 1. Flush any pending captures from previous runs
    flush_offline_queue(&client, &mut queue).await;

    // 2. Continuous capture session loop
    let audio_config = AudioConfig::default();
    let boundary_config = BoundaryConfig::default();
    let mut session = CaptureSession::new(audio_config.clone());
    let stt = MockSpeechRecognizer::default();

    println!(
        "Capture session initialized: status={}",
        session.status_indicator()
    );
    println!("Listening for counter speech (press Ctrl+C to stop)...");

    // Mock audio source producing demonstration speech burst
    let mut source = MockAudioSource::with_speech_burst(50, 0.05);

    let mut tick_interval = tokio::time::interval(Duration::from_millis(30));

    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {
                println!("\nShutdown signal received. Discarding audio securely...");
                session.reset_securely();
                break;
            }
            _ = tick_interval.tick() => {
                if let Ok(Some(chunk)) = source.read_frame() {
                    let boundary_hint = source.frames.is_empty();
                    let state = session.process_frame(chunk, boundary_hint);

                    match state {
                        CaptureSessionState::BoundaryDetected => {
                            println!("Boundary closing phrase detected!");
                            let samples = session.drain_audio_for_transcription();

                            if let Ok(transcript) = stt.transcribe(&samples) {
                                let (boundary_type, boundary_phrase) =
                                    detect_boundary_with_config(&transcript, &boundary_config);
                                let lines = extract_lines_from_transcript(&transcript, boundary_phrase.as_deref());

                                let capture_id = format!("cap_edge_{}", Uuid::new_v4().simple());
                                let payload = CapturePayload {
                                    capture_id: capture_id.clone(),
                                    device_id: Some(device_id.clone()),
                                    r#type: boundary_type,
                                    transcript,
                                    closing_phrase: boundary_phrase,
                                    occurred_at: Utc::now().to_rfc3339(),
                                    extracted_lines: lines,
                                };

                                println!("Enqueuing new capture: {}", capture_id);
                                if let Err(e) = queue.enqueue(payload) {
                                    eprintln!("Failed to enqueue capture: {}", e);
                                }
                                flush_offline_queue(&client, &mut queue).await;
                            }
                        }
                        CaptureSessionState::TimedOut => {
                            println!("Capture timed out after 15 seconds without closing phrase. Discarding audio...");
                            session.reset_securely();
                        }
                        _ => {}
                    }
                } else {
                    // When source runs out of frames in mock mode, poll/flush queue and sleep lightly
                    flush_offline_queue(&client, &mut queue).await;
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        }
    }

    println!("ListenLedger Edge Capture Engine stopped cleanly.");
    Ok(())
}

async fn flush_offline_queue(client: &CaptureClient, queue: &mut DurableQueue) {
    while let Some(pending) = queue.peek() {
        println!("Flushing pending capture {} from queue...", pending.capture_id);
        match client.submit_capture(pending).await {
            Ok(ack) => {
                println!("Backend acknowledged capture: {}", ack);
                if let Err(e) = queue.pop() {
                    eprintln!("Failed to pop acknowledged capture: {}", e);
                    break;
                }
            }
            Err(e) => {
                eprintln!("Delivery deferred to retry backoff: {}", e);
                break;
            }
        }
    }
}
