//! Acoustic simulation fixtures and pipeline latency benchmarking for ListenLedger Edge.

use crate::audio::{
    encode_wav, AudioConfig, AudioInputSource, CaptureSession, CaptureSessionState, WavAudioSource,
};
use crate::extractor::{
    detect_boundary_with_config, extract_lines_from_transcript, BoundaryConfig, ExtractedLine,
    TransactionType,
};
use crate::stt::{MockSpeechRecognizer, SpeechRecognizer};
use std::time::Instant;

/// Represents an acoustic test scenario for the edge capture pipeline.
#[derive(Debug, Clone)]
pub struct AcousticScenario {
    pub name: String,
    pub description: String,
    pub wav_bytes: Vec<u8>,
    pub simulated_transcript: String,
    pub expected_boundary: Option<TransactionType>,
    pub should_capture: bool,
    pub expected_min_lines: usize,
}

/// Aggregated latency and accuracy metrics from the acoustic benchmark.
#[derive(Debug, Clone)]
pub struct AcousticBenchmarkMetrics {
    pub total_scenarios: usize,
    pub passed_scenarios: usize,
    pub total_frames_processed: usize,
    pub avg_frame_processing_micros: f64,
    pub max_frame_processing_micros: u128,
    pub false_captures: usize,
    pub missed_captures: usize,
}

/// Generates a synthetic sinusoidal tone encoded in 16-bit mono RIFF WAV.
pub fn generate_tone_wav(
    duration_ms: u64,
    freq_hz: f32,
    amplitude: f32,
    sample_rate: u32,
) -> Vec<u8> {
    let num_samples = (sample_rate as u64 * duration_ms / 1000) as usize;
    let mut samples = Vec::with_capacity(num_samples);
    let angular_freq = 2.0 * std::f32::consts::PI * freq_hz / sample_rate as f32;

    for i in 0..num_samples {
        let sample = (i as f32 * angular_freq).sin() * amplitude;
        samples.push(sample);
    }

    encode_wav(&samples, sample_rate)
}

/// Generates a synthetic silence audio buffer encoded in 16-bit mono RIFF WAV.
pub fn generate_silence_wav(duration_ms: u64, sample_rate: u32) -> Vec<u8> {
    let num_samples = (sample_rate as u64 * duration_ms / 1000) as usize;
    let samples = vec![0.0f32; num_samples];
    encode_wav(&samples, sample_rate)
}

/// Generates a speech-like burst followed by trailing silence.
pub fn generate_speech_burst_wav(
    burst_ms: u64,
    silence_ms: u64,
    amplitude: f32,
    sample_rate: u32,
) -> Vec<u8> {
    let burst_samples = (sample_rate as u64 * burst_ms / 1000) as usize;
    let silence_samples = (sample_rate as u64 * silence_ms / 1000) as usize;
    let mut samples = Vec::with_capacity(burst_samples + silence_samples);

    for i in 0..burst_samples {
        let t = i as f32 / sample_rate as f32;
        // Superposition of formant-like frequencies (300Hz, 800Hz, 2500Hz)
        let sample = ((2.0 * std::f32::consts::PI * 300.0 * t).sin() * 0.5
            + (2.0 * std::f32::consts::PI * 800.0 * t).sin() * 0.3
            + (2.0 * std::f32::consts::PI * 2500.0 * t).sin() * 0.2)
            * amplitude;
        samples.push(sample);
    }

    samples.extend(vec![0.0f32; silence_samples]);
    encode_wav(&samples, sample_rate)
}

/// Returns standard acoustic test scenarios covering sales, restocks, and non-commercial speech.
pub fn standard_acoustic_scenarios() -> Vec<AcousticScenario> {
    vec![
        AcousticScenario {
            name: "CleanSale_Milo".to_string(),
            description: "High-energy commercial sale concluding with 'Thank you'".to_string(),
            wav_bytes: generate_speech_burst_wav(1500, 300, 0.25, 16000),
            simulated_transcript: "Two packs of Milo, five thousand naira. Thank you.".to_string(),
            expected_boundary: Some(TransactionType::Sale),
            should_capture: true,
            expected_min_lines: 1,
        },
        AcousticScenario {
            name: "CleanRestock_Indomie".to_string(),
            description: "Restock announcement concluding with 'Stock received'".to_string(),
            wav_bytes: generate_speech_burst_wav(2000, 300, 0.28, 16000),
            simulated_transcript: "Stock received: five cartons of Indomie, stock received."
                .to_string(),
            expected_boundary: Some(TransactionType::Restock),
            should_capture: true,
            expected_min_lines: 1,
        },
        AcousticScenario {
            name: "CasualChatter_NoClosingPhrase".to_string(),
            description: "Neighborly greeting without commercial boundary phrase".to_string(),
            wav_bytes: generate_speech_burst_wav(1200, 600, 0.15, 16000),
            simulated_transcript: "Good morning how is your family today? Everything is well."
                .to_string(),
            expected_boundary: None,
            should_capture: false,
            expected_min_lines: 0,
        },
        AcousticScenario {
            name: "AmbientSilence_BelowVAD".to_string(),
            description: "Sub-threshold background noise in empty shop".to_string(),
            wav_bytes: generate_silence_wav(1000, 16000),
            simulated_transcript: "".to_string(),
            expected_boundary: None,
            should_capture: false,
            expected_min_lines: 0,
        },
    ]
}

/// Result of executing a single scenario through the edge pipeline.
#[derive(Debug, Clone)]
pub struct ScenarioExecutionResult {
    pub scenario_name: String,
    pub passed: bool,
    pub boundary_detected: bool,
    pub captured_type: Option<TransactionType>,
    pub extracted_lines: Vec<ExtractedLine>,
    pub frame_count: usize,
    pub total_duration_micros: u128,
}

/// Executes the acoustic scenarios through the audio, VAD, STT, and extraction pipeline.
pub fn run_acoustic_benchmark(
    scenarios: &[AcousticScenario],
    config: &AudioConfig,
    boundary_config: &BoundaryConfig,
) -> (AcousticBenchmarkMetrics, Vec<ScenarioExecutionResult>) {
    let mut results = Vec::new();
    let mut total_frames = 0;
    let mut total_micros: u128 = 0;
    let mut max_frame_micros: u128 = 0;
    let mut false_captures = 0;
    let mut missed_captures = 0;
    let mut passed_count = 0;

    for sc in scenarios {
        let mut source =
            WavAudioSource::from_wav_bytes(&sc.wav_bytes, config).expect("valid wav audio bytes");
        let mut session = CaptureSession::new(config.clone());
        let stt = MockSpeechRecognizer::new(&sc.simulated_transcript);

        let mut boundary_detected = false;
        let mut captured_type = None;
        let mut extracted_lines = Vec::new();
        let mut frame_count = 0;
        let sc_start = Instant::now();

        while let Ok(Some(chunk)) = source.read_frame() {
            frame_count += 1;
            let f_start = Instant::now();
            let boundary_hint = source.is_exhausted();
            let state = session.process_frame(chunk, boundary_hint);
            let f_elapsed = f_start.elapsed().as_micros();

            total_frames += 1;
            total_micros += f_elapsed;
            if f_elapsed > max_frame_micros {
                max_frame_micros = f_elapsed;
            }

            if state == CaptureSessionState::BoundaryDetected {
                boundary_detected = true;
                let samples = session.drain_audio_for_transcription();
                if let Ok(transcript) = stt.transcribe(&samples) {
                    let (b_type, b_phrase) =
                        detect_boundary_with_config(&transcript, boundary_config);
                    captured_type = b_type;
                    extracted_lines =
                        extract_lines_from_transcript(&transcript, b_phrase.as_deref());
                }
                break;
            }
        }

        let sc_duration = sc_start.elapsed().as_micros();

        let pass = if sc.should_capture {
            if !boundary_detected {
                missed_captures += 1;
                false
            } else if captured_type != sc.expected_boundary {
                false
            } else {
                extracted_lines.len() >= sc.expected_min_lines
            }
        } else if boundary_detected {
            false_captures += 1;
            false
        } else {
            true
        };

        if pass {
            passed_count += 1;
        }

        results.push(ScenarioExecutionResult {
            scenario_name: sc.name.clone(),
            passed: pass,
            boundary_detected,
            captured_type,
            extracted_lines,
            frame_count,
            total_duration_micros: sc_duration,
        });
    }

    let avg_frame_micros = if total_frames > 0 {
        total_micros as f64 / total_frames as f64
    } else {
        0.0
    };

    let metrics = AcousticBenchmarkMetrics {
        total_scenarios: scenarios.len(),
        passed_scenarios: passed_count,
        total_frames_processed: total_frames,
        avg_frame_processing_micros: avg_frame_micros,
        max_frame_processing_micros: max_frame_micros,
        false_captures,
        missed_captures,
    };

    (metrics, results)
}
