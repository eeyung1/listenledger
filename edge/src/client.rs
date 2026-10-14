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

impl ClientError {
    /// Determines whether the error represents a transient failure that should be retried.
    pub fn is_retryable(&self) -> bool {
        match self {
            ClientError::Network(_) => true,
            ClientError::ServerError(status, _) => match *status {
                408 | 429 | 500 | 502 | 503 | 504 => true,
                _ => false, // 400 Bad Request, 401 Unauthorized, 403 Forbidden, 409 Conflict are non-retryable
            },
        }
    }

    /// Determines whether the failure is an authentication/credential rejection.
    pub fn is_auth_error(&self) -> bool {
        matches!(self, ClientError::ServerError(401 | 403, _))
    }
}

/// Configuration for exponential retry backoff.
#[derive(Debug, Clone)]
pub struct RetryConfig {
    pub initial_interval: Duration,
    pub max_interval: Duration,
    pub multiplier: f64,
    pub max_attempts: usize,
}

impl Default for RetryConfig {
    fn default() -> Self {
        Self {
            initial_interval: Duration::from_millis(500),
            max_interval: Duration::from_secs(30),
            multiplier: 2.0,
            max_attempts: 5,
        }
    }
}

/// Calculates exponential backoff duration capped at max_interval.
pub fn calculate_backoff(attempt: usize, config: &RetryConfig) -> Duration {
    if attempt == 0 {
        return Duration::ZERO;
    }
    let factor = config.multiplier.powi((attempt - 1) as i32);
    let millis = (config.initial_interval.as_millis() as f64 * factor) as u64;
    let delay = Duration::from_millis(millis);
    if delay > config.max_interval {
        config.max_interval
    } else {
        delay
    }
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

    /// Sends a capture payload with bounded exponential retry and backoff.
    pub async fn submit_capture_with_retry(
        &self,
        payload: &CapturePayload,
        config: &RetryConfig,
    ) -> Result<String, ClientError> {
        let mut attempt = 0;
        loop {
            match self.submit_capture(payload).await {
                Ok(ack) => return Ok(ack),
                Err(err) => {
                    attempt += 1;
                    if attempt >= config.max_attempts || !err.is_retryable() {
                        return Err(err);
                    }
                    let delay = calculate_backoff(attempt, config);
                    tokio::time::sleep(delay).await;
                }
            }
        }
    }
}
