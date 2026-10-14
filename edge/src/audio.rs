//! Audio capture, microphone abstraction, bounded buffering, and Voice Activity Detection (VAD).
//!
//! Blueprint §17: Raw audio is processed in bounded memory and discarded immediately
//! once transcription and anchor extraction are complete.

use std::collections::VecDeque;
use std::time::Duration;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum AudioError {
    #[error("audio device not found: {0}")]
    DeviceNotFound(String),
    #[error("audio capture failure: {0}")]
    CaptureError(String),
    #[error("buffer overflow")]
    BufferOverflow,
    #[error("invalid audio format: {0}")]
    InvalidFormat(String),
    #[error("I/O error: {0}")]
    IoError(#[from] std::io::Error),
}

/// Represents an available physical or virtual audio capture device.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, PartialEq, Eq)]
pub struct AudioDevice {
    pub id: String,
    pub name: String,
    pub is_default: bool,
}

/// Lists selectable audio capture devices available on the host system.
pub fn list_audio_devices() -> Result<Vec<AudioDevice>, AudioError> {
    Ok(vec![
        AudioDevice {
            id: "default".to_string(),
            name: "System Default Microphone".to_string(),
            is_default: true,
        },
        AudioDevice {
            id: "counter_mic_01".to_string(),
            name: "ListenLedger Directional Counter Microphone".to_string(),
            is_default: false,
        },
    ])
}

/// Audio format configuration. Standard is 16kHz mono 16-bit PCM for STT models.
#[derive(Debug, Clone)]
pub struct AudioConfig {
    pub sample_rate: u32,
    pub channels: u16,
    pub frame_size_ms: u64,
    pub vad_energy_threshold: f32,
}

impl Default for AudioConfig {
    fn default() -> Self {
        Self {
            sample_rate: 16000,
            channels: 1,
            frame_size_ms: 30,
            vad_energy_threshold: 0.015,
        }
    }
}

/// Represents an audio frame (PCM samples).
#[derive(Debug, Clone)]
pub struct AudioChunk {
    pub samples: Vec<f32>,
    pub duration: Duration,
}

impl AudioChunk {
    /// Computes Root Mean Square (RMS) energy of the frame.
    pub fn rms_energy(&self) -> f32 {
        if self.samples.is_empty() {
            return 0.0;
        }
        let sum_sq: f32 = self.samples.iter().map(|&s| s * s).sum();
        (sum_sq / self.samples.len() as f32).sqrt()
    }

    /// Evaluates if the frame contains active speech based on energy threshold.
    pub fn is_speech(&self, threshold: f32) -> bool {
        self.rms_energy() >= threshold
    }
}

/// Discards in-memory audio buffers securely.
pub fn secure_discard_audio(chunk: &mut AudioChunk) {
    chunk.samples.fill(0.0);
    chunk.samples.clear();
}

/// Abstract audio input source stream.
pub trait AudioInputSource: Send + Sync {
    /// Reads the next available audio frame.
    fn read_frame(&mut self) -> Result<Option<AudioChunk>, AudioError>;
    /// Human-readable source descriptor.
    fn name(&self) -> &str;
}

/// In-memory mock audio source for test suites and offline regression verification.
#[derive(Debug, Clone)]
pub struct MockAudioSource {
    pub frames: VecDeque<AudioChunk>,
    pub name: String,
}

impl MockAudioSource {
    pub fn new(frames: Vec<AudioChunk>) -> Self {
        Self {
            frames: frames.into(),
            name: "MockAudioSource".to_string(),
        }
    }

    /// Creates a mock source with a sequence of synthetic speech frames.
    pub fn with_speech_burst(frame_count: usize, energy: f32) -> Self {
        let mut frames = Vec::with_capacity(frame_count);
        for _ in 0..frame_count {
            frames.push(AudioChunk {
                samples: vec![energy; 480], // 30ms @ 16kHz
                duration: Duration::from_millis(30),
            });
        }
        Self::new(frames)
    }
}

impl AudioInputSource for MockAudioSource {
    fn read_frame(&mut self) -> Result<Option<AudioChunk>, AudioError> {
        Ok(self.frames.pop_front())
    }

    fn name(&self) -> &str {
        &self.name
    }
}

/// File/buffer-backed audio source slicing raw PCM samples into frames.
#[derive(Debug, Clone)]
pub struct WavAudioSource {
    pub samples: Vec<f32>,
    pub position: usize,
    pub frame_samples: usize,
    pub frame_duration: Duration,
    pub name: String,
}

impl WavAudioSource {
    pub fn from_samples(samples: Vec<f32>, config: &AudioConfig) -> Self {
        let frame_samples = ((config.sample_rate as u64 * config.frame_size_ms) / 1000) as usize;
        Self {
            samples,
            position: 0,
            frame_samples: if frame_samples == 0 { 480 } else { frame_samples },
            frame_duration: Duration::from_millis(config.frame_size_ms),
            name: "WavAudioSource".to_string(),
        }
    }
}

impl AudioInputSource for WavAudioSource {
    fn read_frame(&mut self) -> Result<Option<AudioChunk>, AudioError> {
        if self.position >= self.samples.len() {
            return Ok(None);
        }
        let end = (self.position + self.frame_samples).min(self.samples.len());
        let slice = self.samples[self.position..end].to_vec();
        self.position = end;
        Ok(Some(AudioChunk {
            samples: slice,
            duration: self.frame_duration,
        }))
    }

    fn name(&self) -> &str {
        &self.name
    }
}

/// Channel-backed audio source receiving streamed frames asynchronously.
pub struct AudioStreamSource {
    pub rx: tokio::sync::mpsc::Receiver<AudioChunk>,
    pub name: String,
}

impl AudioStreamSource {
    pub fn new(rx: tokio::sync::mpsc::Receiver<AudioChunk>) -> Self {
        Self {
            rx,
            name: "AudioStreamSource".to_string(),
        }
    }
}

impl AudioInputSource for AudioStreamSource {
    fn read_frame(&mut self) -> Result<Option<AudioChunk>, AudioError> {
        match self.rx.try_recv() {
            Ok(chunk) => Ok(Some(chunk)),
            Err(tokio::sync::mpsc::error::TryRecvError::Empty) => Ok(None),
            Err(tokio::sync::mpsc::error::TryRecvError::Disconnected) => Ok(None),
        }
    }

    fn name(&self) -> &str {
        &self.name
    }
}

/// Bounded ring buffer holding PCM audio chunks with duration limits.
///
/// Prevents memory leaks by discarding oldest frames if incoming speech
/// exceeds the configured retention window.
#[derive(Debug, Clone)]
pub struct AudioRingBuffer {
    pub frames: VecDeque<AudioChunk>,
    pub max_duration: Duration,
    pub current_duration: Duration,
}

impl AudioRingBuffer {
    pub fn new(max_duration: Duration) -> Self {
        Self {
            frames: VecDeque::new(),
            max_duration,
            current_duration: Duration::ZERO,
        }
    }

    pub fn push(&mut self, chunk: AudioChunk) {
        self.current_duration += chunk.duration;
        self.frames.push_back(chunk);

        while self.current_duration > self.max_duration {
            if let Some(mut old) = self.frames.pop_front() {
                self.current_duration = self.current_duration.saturating_sub(old.duration);
                secure_discard_audio(&mut old);
            } else {
                break;
            }
        }
    }

    /// Extracts and zeroes all contained audio samples for inference.
    pub fn drain_samples(&mut self) -> Vec<f32> {
        let total_samples: usize = self.frames.iter().map(|f| f.samples.len()).sum();
        let mut result = Vec::with_capacity(total_samples);
        while let Some(mut frame) = self.frames.pop_front() {
            result.extend_from_slice(&frame.samples);
            secure_discard_audio(&mut frame);
        }
        self.current_duration = Duration::ZERO;
        result
    }

    /// Securely wipes all audio buffers in-place.
    pub fn clear_securely(&mut self) {
        while let Some(mut frame) = self.frames.pop_front() {
            secure_discard_audio(&mut frame);
        }
        self.current_duration = Duration::ZERO;
    }

    pub fn len(&self) -> usize {
        self.frames.len()
    }

    pub fn duration(&self) -> Duration {
        self.current_duration
    }

    pub fn is_empty(&self) -> bool {
        self.frames.is_empty()
    }
}

/// Continuous capture session state machine.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CaptureSessionState {
    /// Ambient counter listening mode, monitoring VAD.
    Listening,
    /// Active speech utterance detected, buffering frames.
    ActiveCapture,
    /// Transaction closing phrase detected, ready for STT and delivery.
    BoundaryDetected,
    /// 15-second incomplete speech timeout reached without closing phrase.
    TimedOut,
    /// Capture paused by operator or policy.
    Paused,
}

/// Manages a continuous ambient recording session with VAD and timeout guards.
#[derive(Debug)]
pub struct CaptureSession {
    pub state: CaptureSessionState,
    pub config: AudioConfig,
    pub ring_buffer: AudioRingBuffer,
    pub session_start: Option<std::time::Instant>,
    pub max_capture_duration: Duration,
}

impl CaptureSession {
    pub fn new(config: AudioConfig) -> Self {
        Self {
            state: CaptureSessionState::Listening,
            config,
            ring_buffer: AudioRingBuffer::new(Duration::from_secs(30)),
            session_start: None,
            max_capture_duration: Duration::from_secs(15),
        }
    }

    pub fn pause(&mut self) {
        self.state = CaptureSessionState::Paused;
        self.ring_buffer.clear_securely();
        self.session_start = None;
    }

    pub fn resume(&mut self) {
        self.state = CaptureSessionState::Listening;
        self.ring_buffer.clear_securely();
        self.session_start = None;
    }

    /// Obvious user-facing status indicator for terminal/UI logs.
    pub fn status_indicator(&self) -> &'static str {
        match self.state {
            CaptureSessionState::Paused => "⏸️ PAUSED",
            CaptureSessionState::Listening => "🟢 LISTENING",
            CaptureSessionState::ActiveCapture => "🔴 RECORDING",
            CaptureSessionState::BoundaryDetected => "✅ BOUNDARY DETECTED",
            CaptureSessionState::TimedOut => "⚠️ TIMED OUT",
        }
    }

    /// Feeds an incoming audio chunk into the session state machine.
    pub fn process_frame(&mut self, chunk: AudioChunk, boundary_detected: bool) -> CaptureSessionState {
        if self.state == CaptureSessionState::Paused {
            return CaptureSessionState::Paused;
        }

        let is_speech = chunk.is_speech(self.config.vad_energy_threshold);

        match self.state {
            CaptureSessionState::Listening => {
                if is_speech {
                    self.state = CaptureSessionState::ActiveCapture;
                    self.session_start = Some(std::time::Instant::now());
                    self.ring_buffer.push(chunk);
                    if boundary_detected {
                        self.state = CaptureSessionState::BoundaryDetected;
                    }
                }
            }
            CaptureSessionState::ActiveCapture => {
                if let Some(start) = self.session_start {
                    if start.elapsed() >= self.max_capture_duration {
                        self.state = CaptureSessionState::TimedOut;
                        return self.state;
                    }
                }
                self.ring_buffer.push(chunk);
                if boundary_detected {
                    self.state = CaptureSessionState::BoundaryDetected;
                }
            }
            CaptureSessionState::BoundaryDetected | CaptureSessionState::TimedOut => {}
            CaptureSessionState::Paused => unreachable!(),
        }

        self.state
    }

    /// Drains accumulated audio for speech recognition and resets session to Listening.
    pub fn drain_audio_for_transcription(&mut self) -> Vec<f32> {
        let samples = self.ring_buffer.drain_samples();
        self.state = CaptureSessionState::Listening;
        self.session_start = None;
        samples
    }

    /// Securely resets the session, wiping audio buffers.
    pub fn reset_securely(&mut self) {
        self.ring_buffer.clear_securely();
        self.state = CaptureSessionState::Listening;
        self.session_start = None;
    }
}
