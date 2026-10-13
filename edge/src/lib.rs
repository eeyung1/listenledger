//! ListenLedger Edge Capture Engine
//!
//! Owns ambient counter audio capture, voice activity detection, speech-to-text,
//! transaction boundary detection, offline queue persistence, and authenticated delivery.

pub mod audio;
pub mod client;
pub mod extractor;
pub mod health;
pub mod queue;
pub mod stt;

#[cfg(test)]
mod tests {
    use super::client::{calculate_backoff, ClientError, RetryConfig};
    use super::extractor::{detect_boundary, CapturePayload, TransactionType};
    use super::health::EdgeHealthMonitor;
    use super::queue::{DurableQueue, QueueError};
    use std::fs::OpenOptions;
    use std::io::Write;
    use std::time::Duration;
    use tempfile::tempdir;

    #[test]
    fn test_detect_boundary() {
        let (txn_type, phrase) = detect_boundary("Two Milo, four thousand naira. Thank you.");
        assert_eq!(txn_type, Some(TransactionType::Sale));
        assert_eq!(phrase, Some("Thank you".to_string()));

        let (txn_type, phrase) = detect_boundary("Three cartons of Milo. Stock received.");
        assert_eq!(txn_type, Some(TransactionType::Restock));
        assert_eq!(phrase, Some("Stock received".to_string()));
    }

    #[test]
    fn test_durable_queue_persistence() {
        let dir = tempdir().expect("create temp dir");
        let queue_file = dir.path().join("queue.jsonl");

        {
            let mut q = DurableQueue::open(&queue_file, 10).expect("open queue");
            assert_eq!(q.len(), 0);

            let payload = CapturePayload {
                capture_id: "cap_test_1".to_string(),
                device_id: Some("dev_1".to_string()),
                r#type: Some(TransactionType::Sale),
                transcript: "Two Milo, four thousand naira. Thank you.".to_string(),
                closing_phrase: Some("Thank you".to_string()),
                occurred_at: "2026-10-13T10:00:00Z".to_string(),
                extracted_lines: vec![],
            };
            q.enqueue(payload).expect("enqueue payload");
            assert_eq!(q.len(), 1);
        }

        // Reopen to verify restart durability
        {
            let mut q = DurableQueue::open(&queue_file, 10).expect("reopen queue");
            assert_eq!(q.len(), 1);
            let peeked = q.peek().expect("item exists");
            assert_eq!(peeked.capture_id, "cap_test_1");

            let popped = q.pop().expect("pop item");
            assert!(popped.is_some());
            assert_eq!(q.len(), 0);
        }
    }

    #[test]
    fn test_queue_corruption_recovery_and_capacity() {
        let dir = tempdir().expect("create temp dir");
        let queue_file = dir.path().join("queue.jsonl");

        // 1. Write one valid line and one partial/corrupted trailing line
        {
            let payload = CapturePayload {
                capture_id: "cap_valid_1".to_string(),
                device_id: Some("dev_1".to_string()),
                r#type: Some(TransactionType::Sale),
                transcript: "One Bread. That is all.".to_string(),
                closing_phrase: Some("That is all".to_string()),
                occurred_at: "2026-10-13T10:00:00Z".to_string(),
                extracted_lines: vec![],
            };
            let serialized = serde_json::to_string(&payload).expect("serialize");
            let mut file = OpenOptions::new()
                .create(true)
                .write(true)
                .open(&queue_file)
                .expect("open file");
            writeln!(file, "{}", serialized).expect("write valid");
            writeln!(file, "{{\"corrupt_json\": true, incomplete").expect("write corrupt");
        }

        // 2. Open queue: should recover the valid entry and discard corrupted entry
        {
            let mut q = DurableQueue::open(&queue_file, 2).expect("open queue with corrupt line");
            assert_eq!(q.len(), 1);
            let peeked = q.peek().expect("valid item peek");
            assert_eq!(peeked.capture_id, "cap_valid_1");

            // Capacity limit test
            let payload2 = CapturePayload {
                capture_id: "cap_valid_2".to_string(),
                device_id: Some("dev_1".to_string()),
                r#type: Some(TransactionType::Sale),
                transcript: "Two Milk.".to_string(),
                closing_phrase: None,
                occurred_at: "2026-10-13T10:01:00Z".to_string(),
                extracted_lines: vec![],
            };
            q.enqueue(payload2).expect("enqueue second item");
            assert_eq!(q.len(), 2);

            let payload3 = CapturePayload {
                capture_id: "cap_overflow".to_string(),
                device_id: Some("dev_1".to_string()),
                r#type: Some(TransactionType::Sale),
                transcript: "Three Sugar.".to_string(),
                closing_phrase: None,
                occurred_at: "2026-10-13T10:02:00Z".to_string(),
                extracted_lines: vec![],
            };
            match q.enqueue(payload3) {
                Err(QueueError::CapacityExceeded) => {}
                res => panic!("Expected CapacityExceeded, got {:?}", res),
            }
        }
    }

    #[test]
    fn test_client_error_classification_and_backoff() {
        let net_err = ClientError::ServerError(503, "Service Unavailable".to_string());
        assert!(net_err.is_retryable());
        assert!(!net_err.is_auth_error());

        let rate_err = ClientError::ServerError(429, "Too Many Requests".to_string());
        assert!(rate_err.is_retryable());

        let auth_err = ClientError::ServerError(401, "Unauthorized".to_string());
        assert!(!auth_err.is_retryable());
        assert!(auth_err.is_auth_error());

        let bad_request = ClientError::ServerError(400, "Bad Request".to_string());
        assert!(!bad_request.is_retryable());

        let config = RetryConfig {
            initial_interval: Duration::from_millis(100),
            max_interval: Duration::from_millis(800),
            multiplier: 2.0,
            max_attempts: 5,
        };

        assert_eq!(calculate_backoff(0, &config), Duration::ZERO);
        assert_eq!(calculate_backoff(1, &config), Duration::from_millis(100));
        assert_eq!(calculate_backoff(2, &config), Duration::from_millis(200));
        assert_eq!(calculate_backoff(3, &config), Duration::from_millis(400));
        assert_eq!(calculate_backoff(4, &config), Duration::from_millis(800));
        assert_eq!(calculate_backoff(5, &config), Duration::from_millis(800)); // capped
    }

    #[test]
    fn test_edge_health_diagnostics() {
        let mut monitor = EdgeHealthMonitor::new("http://localhost:8080");

        // Normal healthy state
        let report = monitor.report(2, 100, true);
        assert!(report.backend_reachable);
        assert!(report.microphone_active);
        assert!(report.stt_model_loaded);
        assert_eq!(report.queue_len, 2);
        assert_eq!(report.queue_utilization_pct, 2.0);
        assert!(report.active_issues.is_empty());

        // Degraded state: mic disconnected, backend down, queue nearly full
        monitor.set_microphone_status(false);
        monitor.set_stt_model_status(false);
        let degraded = monitor.report(95, 100, false);
        assert!(!degraded.backend_reachable);
        assert!(!degraded.microphone_active);
        assert!(!degraded.stt_model_loaded);
        assert_eq!(degraded.queue_utilization_pct, 95.0);
        assert_eq!(degraded.active_issues.len(), 4);
    }
}

