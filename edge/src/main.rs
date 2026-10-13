use chrono::Utc;
use listenledger_edge::client::CaptureClient;
use listenledger_edge::extractor::{detect_boundary, CapturePayload};
use listenledger_edge::queue::DurableQueue;
use listenledger_edge::stt::{MockSpeechRecognizer, SpeechRecognizer};
use std::env;
use std::path::PathBuf;
use uuid::Uuid;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt::init();

    let backend_url = env::var("LISTENLEDGER_BACKEND_URL")
        .unwrap_or_else(|_| "http://127.0.0.1:8080".to_string());
    let device_token = env::var("LISTENLEDGER_DEVICE_TOKEN")
        .unwrap_or_else(|_| "dev_test_token".to_string());
    let queue_path = env::var("LISTENLEDGER_QUEUE_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from("captures_queue.jsonl"));

    println!("Starting ListenLedger Edge Capture Engine");
    println!("Connecting to: {}", backend_url);
    println!("Offline queue at: {:?}", queue_path);

    let client = CaptureClient::new(&backend_url, &device_token);
    let mut queue = DurableQueue::open(&queue_path, 1000)?;

    // Flush any pending captures from previous runs
    while let Some(pending) = queue.peek() {
        println!("Flushing pending capture {} from queue...", pending.capture_id);
        match client.submit_capture(pending).await {
            Ok(ack) => {
                println!("Backend acknowledged: {}", ack);
                queue.pop()?;
            }
            Err(e) => {
                eprintln!("Delivery failed, will retry later: {}", e);
                break;
            }
        }
    }

    // Benchmark capture demonstration with SpeechRecognizer
    let stt = MockSpeechRecognizer::default();
    let transcript = stt.transcribe(&[])?;
    let (boundary_type, boundary_phrase) = detect_boundary(&transcript);

    let capture_id = format!("cap_edge_{}", Uuid::new_v4().simple());
    let payload = CapturePayload {
        capture_id: capture_id.clone(),
        device_id: Some("dev_edge_runner".to_string()),
        r#type: boundary_type,
        transcript,
        closing_phrase: boundary_phrase,
        occurred_at: Utc::now().to_rfc3339(),
        extracted_lines: vec![],
    };

    println!("Enqueuing new capture: {}", capture_id);
    queue.enqueue(payload)?;

    if let Some(next) = queue.peek() {
        match client.submit_capture(next).await {
            Ok(ack) => {
                println!("Successfully submitted capture: {}", ack);
                queue.pop()?;
            }
            Err(err) => {
                println!("Submission deferred to offline queue (reason: {})", err);
            }
        }
    }

    println!("ListenLedger Edge Capture Engine ready. Listening for counter audio...");
    Ok(())
}

