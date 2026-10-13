//! Speech-to-Text (STT) interface for local quantized model inference.

use thiserror::Error;

#[derive(Debug, Error)]
pub enum SttError {
    #[error("failed to load model weights: {0}")]
    ModelLoadError(String),
    #[error("inference execution failed: {0}")]
    InferenceError(String),
}

/// Abstract speech recognition provider.
pub trait SpeechRecognizer: Send + Sync {
    /// Transcribes a sequence of PCM audio samples into text.
    fn transcribe(&self, samples: &[f32]) -> Result<String, SttError>;
}

/// Local mock/benchmark recognizer used for unit testing and offline harness.
#[derive(Debug, Clone, Default)]
pub struct MockSpeechRecognizer {
    pub fixture_transcript: Option<String>,
}

impl SpeechRecognizer for MockSpeechRecognizer {
    fn transcribe(&self, _samples: &[f32]) -> Result<String, SttError> {
        Ok(self
            .fixture_transcript
            .clone()
            .unwrap_or_else(|| "Two Milo, four thousand naira. Thank you.".to_string()))
    }
}

