//! ListenLedger Edge Capture Engine
//!
//! Owns ambient counter audio capture, voice activity detection, speech-to-text,
//! transaction boundary detection, offline queue persistence, and authenticated delivery.

pub mod audio;
pub mod benchmark;
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

    #[test]
    fn test_audio_ring_buffer_and_secure_discard() {
        use super::audio::{secure_discard_audio, AudioChunk, AudioRingBuffer};

        let mut buffer = AudioRingBuffer::new(Duration::from_millis(90));
        assert!(buffer.is_empty());
        assert_eq!(buffer.duration(), Duration::ZERO);

        let c1 = AudioChunk {
            samples: vec![0.1; 480],
            duration: Duration::from_millis(30),
        };
        let c2 = AudioChunk {
            samples: vec![0.2; 480],
            duration: Duration::from_millis(30),
        };
        let c3 = AudioChunk {
            samples: vec![0.3; 480],
            duration: Duration::from_millis(30),
        };

        buffer.push(c1);
        buffer.push(c2);
        buffer.push(c3);
        assert_eq!(buffer.len(), 3);
        assert_eq!(buffer.duration(), Duration::from_millis(90));

        // Pushing a 4th frame must evict the oldest frame (c1) to stay bounded
        let c4 = AudioChunk {
            samples: vec![0.4; 480],
            duration: Duration::from_millis(30),
        };
        buffer.push(c4);
        assert_eq!(buffer.len(), 3);
        assert_eq!(buffer.duration(), Duration::from_millis(90));

        // Drain samples
        let samples = buffer.drain_samples();
        assert_eq!(samples.len(), 3 * 480);
        assert!(buffer.is_empty());
        assert_eq!(buffer.duration(), Duration::ZERO);

        // Verify secure discard zeroes out buffer
        let mut test_chunk = AudioChunk {
            samples: vec![0.5, 0.6, 0.7],
            duration: Duration::from_millis(10),
        };
        secure_discard_audio(&mut test_chunk);
        assert!(test_chunk.samples.is_empty());
    }

    #[test]
    fn test_capture_session_state_machine() {
        use super::audio::{AudioChunk, AudioConfig, CaptureSession, CaptureSessionState};

        let config = AudioConfig {
            vad_energy_threshold: 0.015,
            ..Default::default()
        };
        let mut session = CaptureSession::new(config);
        assert_eq!(session.state, CaptureSessionState::Listening);
        assert_eq!(session.status_indicator(), "🟢 LISTENING");

        // Silence frame (low energy): session stays in Listening
        let silence = AudioChunk {
            samples: vec![0.001; 480],
            duration: Duration::from_millis(30),
        };
        assert!(!silence.is_speech(0.015));
        let state = session.process_frame(silence, false);
        assert_eq!(state, CaptureSessionState::Listening);

        // Speech frame (high energy): transitions to ActiveCapture
        let speech = AudioChunk {
            samples: vec![0.05; 480],
            duration: Duration::from_millis(30),
        };
        assert!(speech.is_speech(0.015));
        let state = session.process_frame(speech.clone(), false);
        assert_eq!(state, CaptureSessionState::ActiveCapture);
        assert_eq!(session.status_indicator(), "🔴 RECORDING");

        // Closing phrase detected: transitions to BoundaryDetected
        let state = session.process_frame(speech.clone(), true);
        assert_eq!(state, CaptureSessionState::BoundaryDetected);
        assert_eq!(session.status_indicator(), "✅ BOUNDARY DETECTED");

        // Drain audio resets state to Listening
        let drained = session.drain_audio_for_transcription();
        assert!(!drained.is_empty());
        assert_eq!(session.state, CaptureSessionState::Listening);

        // Pause / Resume test
        session.pause();
        assert_eq!(session.state, CaptureSessionState::Paused);
        assert_eq!(session.status_indicator(), "⏸️ PAUSED");
        let state = session.process_frame(speech, true);
        assert_eq!(state, CaptureSessionState::Paused); // Ignored while paused

        session.resume();
        assert_eq!(session.state, CaptureSessionState::Listening);
    }

    #[test]
    fn test_audio_input_sources() {
        use super::audio::{
            list_audio_devices, AudioConfig, AudioInputSource, MockAudioSource, WavAudioSource,
        };

        // 1. Device listing
        let devices = list_audio_devices().expect("list devices");
        assert!(!devices.is_empty());
        assert!(devices.iter().any(|d| d.is_default));

        // 2. MockAudioSource
        let mut mock_src = MockAudioSource::with_speech_burst(3, 0.04);
        assert_eq!(mock_src.name(), "MockAudioSource");
        let f1 = mock_src.read_frame().expect("read frame");
        assert!(f1.is_some());
        assert_eq!(mock_src.frames.len(), 2);

        // 3. WavAudioSource
        let config = AudioConfig::default();
        let samples = vec![0.02; 960]; // 2 frames worth (480 each)
        let mut wav_src = WavAudioSource::from_samples(samples, &config);
        assert_eq!(wav_src.name(), "WavAudioSource");

        let frame1 = wav_src.read_frame().expect("read wav 1");
        assert!(frame1.is_some());
        assert_eq!(frame1.unwrap().samples.len(), 480);

        let frame2 = wav_src.read_frame().expect("read wav 2");
        assert!(frame2.is_some());

        let frame3 = wav_src.read_frame().expect("read wav 3");
        assert!(frame3.is_none());
    }

    #[test]
    fn test_configurable_boundary_and_multiline_extraction() {
        use super::extractor::{
            detect_boundary_with_config, extract_lines_from_transcript, BoundaryConfig,
            TransactionType,
        };

        let custom_config = BoundaryConfig {
            sale_phrase: "all done".to_string(),
            restock_phrase: "delivery confirmed".to_string(),
        };

        // Boundary detection with custom phrases
        let (t_type, phrase) = detect_boundary_with_config(
            "Three Milk, two thousand naira. All done.",
            &custom_config,
        );
        assert_eq!(t_type, Some(TransactionType::Sale));
        assert_eq!(phrase, Some("all done".to_string()));

        let (t_type, phrase) =
            detect_boundary_with_config("Ten packs of water. Delivery confirmed.", &custom_config);
        assert_eq!(t_type, Some(TransactionType::Restock));
        assert_eq!(phrase, Some("delivery confirmed".to_string()));

        // Multi-line receipt extraction
        let transcript = "Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Stock received.";
        let lines = extract_lines_from_transcript(transcript, Some("Stock received"));
        assert_eq!(lines.len(), 2);

        assert_eq!(lines[0].product_name, "Pepsi");
        assert_eq!(lines[0].quantity, 5);
        assert_eq!(lines[0].unit, "pack");
        assert_eq!(lines[0].amount.as_ref().unwrap().amount, 1200000); // 12,000 NGN in kobo

        assert_eq!(lines[1].product_name, "Milo");
        assert_eq!(lines[1].quantity, 3);
        assert_eq!(lines[1].unit, "carton");
        assert_eq!(lines[1].amount.as_ref().unwrap().amount, 4500000); // 45,000 NGN in kobo
    }

    #[test]
    fn test_stt_speech_recognizers() {
        use super::stt::{CommandSpeechRecognizer, MockSpeechRecognizer, SpeechRecognizer};

        let mock = MockSpeechRecognizer::new("One Bread, five hundred naira. Thank you.");
        let text = mock.transcribe(&[]).expect("transcribe");
        assert_eq!(text, "One Bread, five hundred naira. Thank you.");

        let cmd_stt = CommandSpeechRecognizer::new("/bin/echo")
            .with_model("tiny.en")
            .with_language("en");
        assert_eq!(cmd_stt.binary_path, "/bin/echo");
        assert_eq!(cmd_stt.model_path.as_deref(), Some("tiny.en"));
        assert_eq!(cmd_stt.language, "en");

        let test_samples = vec![0.0f32; 160];
        let echoed = cmd_stt.transcribe(&test_samples).expect("echo transcribe");
        assert!(echoed.contains("-m tiny.en"));
        assert!(echoed.contains("-l en"));
        assert!(echoed.contains("-nt"));
        assert!(echoed.contains(".wav"));
    }

    #[test]
    fn test_encode_wav_and_wav_audio_source() {
        use super::audio::{encode_wav, AudioConfig, AudioInputSource, WavAudioSource};
        use std::io::Write;
        use tempfile::NamedTempFile;

        let original_samples = vec![0.0, 0.25, -0.25, 0.5, -0.5, 0.75, -0.75];
        let wav_bytes = encode_wav(&original_samples, 16000);
        assert!(wav_bytes.len() >= 44 + original_samples.len() * 2);
        assert_eq!(&wav_bytes[0..4], b"RIFF");
        assert_eq!(&wav_bytes[8..12], b"WAVE");
        assert_eq!(&wav_bytes[12..16], b"fmt ");

        let config = AudioConfig {
            sample_rate: 16000,
            channels: 1,
            frame_size_ms: 30,
            vad_energy_threshold: 0.015,
        };

        // 1. In-memory bytes parse
        let wav_src = WavAudioSource::from_wav_bytes(&wav_bytes, &config).expect("parse wav");
        assert_eq!(wav_src.samples.len(), original_samples.len());
        for (orig, decoded) in original_samples.iter().zip(wav_src.samples.iter()) {
            assert!((orig - decoded).abs() < 0.001);
        }

        // 2. File-backed load
        let mut tmp_file = NamedTempFile::new().expect("create temp file");
        tmp_file.write_all(&wav_bytes).expect("write wav");
        tmp_file.flush().expect("flush wav");

        let mut file_src =
            WavAudioSource::from_file(tmp_file.path(), &config).expect("load wav file");
        assert!(!file_src.is_exhausted());
        let frame = file_src.read_frame().expect("read frame");
        assert!(frame.is_some());
        assert!(file_src.is_exhausted());
    }

    #[test]
    fn test_acoustic_pipeline_benchmark() {
        use super::audio::AudioConfig;
        use super::benchmark::{run_acoustic_benchmark, standard_acoustic_scenarios};
        use super::extractor::BoundaryConfig;

        let config = AudioConfig::default();
        let boundary_config = BoundaryConfig::default();
        let scenarios = standard_acoustic_scenarios();
        assert_eq!(scenarios.len(), 4);

        let (metrics, results) = run_acoustic_benchmark(&scenarios, &config, &boundary_config);
        assert_eq!(metrics.total_scenarios, 4);
        assert_eq!(metrics.passed_scenarios, 4);
        assert_eq!(metrics.false_captures, 0);
        assert_eq!(metrics.missed_captures, 0);
        assert!(metrics.total_frames_processed > 0);
        assert_eq!(results.len(), 4);

        // Verify clean sale scenario
        let sale_res = results
            .iter()
            .find(|r| r.scenario_name == "CleanSale_Milo")
            .expect("sale res");
        assert!(sale_res.passed);
        assert!(sale_res.boundary_detected);
        assert_eq!(sale_res.captured_type, Some(TransactionType::Sale));
        assert!(!sale_res.extracted_lines.is_empty());
        assert_eq!(sale_res.extracted_lines[0].product_name, "Milo");

        // Verify casual chatter scenario has zero captures
        let chatter_res = results
            .iter()
            .find(|r| r.scenario_name == "CasualChatter_NoClosingPhrase")
            .expect("chatter res");
        assert!(chatter_res.passed);
        assert!(!chatter_res.boundary_detected);
        assert_eq!(chatter_res.captured_type, None);
        assert!(chatter_res.extracted_lines.is_empty());
    }
}
