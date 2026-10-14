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

impl MockSpeechRecognizer {
    pub fn new(transcript: impl Into<String>) -> Self {
        Self {
            fixture_transcript: Some(transcript.into()),
        }
    }
}

impl SpeechRecognizer for MockSpeechRecognizer {
    fn transcribe(&self, _samples: &[f32]) -> Result<String, SttError> {
        Ok(self
            .fixture_transcript
            .clone()
            .unwrap_or_else(|| "Two Milo, four thousand naira. Thank you.".to_string()))
    }
}

/// Process-driven speech recognizer executing a local CLI binary or whisper runtime.
#[derive(Debug, Clone)]
pub struct CommandSpeechRecognizer {
    pub binary_path: String,
    pub model_path: Option<String>,
    pub language: String,
}

impl CommandSpeechRecognizer {
    pub fn new(binary_path: impl Into<String>) -> Self {
        Self {
            binary_path: binary_path.into(),
            model_path: None,
            language: "en".to_string(),
        }
    }

    pub fn with_model(mut self, model_path: impl Into<String>) -> Self {
        self.model_path = Some(model_path.into());
        self
    }

    pub fn with_language(mut self, language: impl Into<String>) -> Self {
        self.language = language.into();
        self
    }
}

impl SpeechRecognizer for CommandSpeechRecognizer {
    fn transcribe(&self, samples: &[f32]) -> Result<String, SttError> {
        if samples.is_empty() {
            return Ok(String::new());
        }
        use std::process::Command;
        let mut cmd = Command::new(&self.binary_path);
        if let Some(ref m) = self.model_path {
            cmd.arg("--model").arg(m);
        }
        cmd.arg("--language").arg(&self.language);

        let output = cmd.output().map_err(|e| SttError::InferenceError(e.to_string()))?;
        if !output.status.success() {
            return Err(SttError::InferenceError(format!(
                "STT command failed (status {}): {}",
                output.status,
                String::from_utf8_lossy(&output.stderr)
            )));
        }
        Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
    }
}
