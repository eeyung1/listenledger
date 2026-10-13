//! HTTP client for submitting captures to the ListenLedger backend intake.

use crate::extractor::CapturePayload;
use reqwest::Client;
use std::time::Duration;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum ClientError {
    #[error("network request failed: {0}")]
    Network(#[from] reqwest::Error),
    #[error("server answered with status {0}: {1}")]
    ServerError(u16, String),
}

pub struct CaptureClient {
    base_url: String,
    device_token: String,
    http: Client,
}

impl CaptureClient {
    pub fn new(base_url: impl Into<String>, device_token: impl Into<String>) -> Self {
        let http = Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .unwrap_or_default();

        Self {
            base_url: base_url.into(),
            device_token: device_token.into(),
            http,
        }
    }

    /// Sends a capture payload to POST /api/captures with device authentication and idempotency.
    pub async fn submit_capture(&self, payload: &CapturePayload) -> Result<String, ClientError> {
        let url = format!("{}/api/captures", self.base_url.trim_end_matches('/'));
        let response = self
            .http
            .post(&url)
            .header("Authorization", format!("Bearer {}", self.device_token))
            .header("Idempotency-Key", &payload.capture_id)
            .json(payload)
            .send()
            .await?;

        let status = response.status();
        if status.is_success() {
            let body = response.text().await?;
            Ok(body)
        } else {
            let msg = response.text().await.unwrap_or_default();
            Err(ClientError::ServerError(status.as_u16(), msg))
        }
    }
}

