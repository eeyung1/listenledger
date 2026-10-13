//! Anchor and transaction boundary extraction from transcribed speech.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TransactionType {
    Sale,
    Restock,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExtractedLine {
    pub product_name: String,
    pub quantity: i64,
    pub unit: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub amount: Option<Money>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Money {
    pub amount: i64,
    pub currency: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CapturePayload {
    pub capture_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub device_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub r#type: Option<TransactionType>,
    pub transcript: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub closing_phrase: Option<String>,
    pub occurred_at: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub extracted_lines: Vec<ExtractedLine>,
}

/// Detects transaction closing phrase and boundaries.
pub fn detect_boundary(transcript: &str) -> (Option<TransactionType>, Option<String>) {
    let lower = transcript.to_lowercase();
    if lower.contains("stock received") || lower.contains("restock complete") {
        (Some(TransactionType::Restock), Some("Stock received".to_string()))
    } else if lower.contains("thank you") {
        (Some(TransactionType::Sale), Some("Thank you".to_string()))
    } else {
        (None, None)
    }
}

