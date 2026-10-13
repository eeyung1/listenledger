//! ListenLedger Edge Capture Engine
//!
//! Owns ambient counter audio capture, voice activity detection, speech-to-text,
//! transaction boundary detection, offline queue persistence, and authenticated delivery.

pub mod audio;
pub mod client;
pub mod extractor;
pub mod queue;
pub mod stt;

#[cfg(test)]
mod tests {
    use super::extractor::{detect_boundary, TransactionType};
    use super::queue::DurableQueue;
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

            let payload = crate::extractor::CapturePayload {
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
}

