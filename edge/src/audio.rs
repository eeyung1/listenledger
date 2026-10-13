//! Audio capture and Voice Activity Detection (VAD).
//!
//! Blueprint §17: Raw audio is processed in memory and discarded immediately
//! once transcription and anchor extraction are complete.

use std::time::Duration;

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

