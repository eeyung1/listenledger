//! Anchor and transaction boundary extraction from transcribed speech.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TransactionType {
    Sale,
    Restock,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ExtractedLine {
    pub product_name: String,
    pub quantity: i64,
    pub unit: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub amount: Option<Money>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
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

/// Configurable shop boundary phrases distinguishing sales and restocks.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct BoundaryConfig {
    pub sale_phrase: String,
    pub restock_phrase: String,
}

impl Default for BoundaryConfig {
    fn default() -> Self {
        Self {
            sale_phrase: "Thank you".to_string(),
            restock_phrase: "Stock received".to_string(),
        }
    }
}

/// Detects transaction closing phrase and boundaries using shop-configured phrases.
pub fn detect_boundary_with_config(
    transcript: &str,
    config: &BoundaryConfig,
) -> (Option<TransactionType>, Option<String>) {
    let lower = transcript.to_lowercase();
    let restock_phrase_lower = config.restock_phrase.to_lowercase();
    let sale_phrase_lower = config.sale_phrase.to_lowercase();

    if lower.contains(&restock_phrase_lower) || lower.contains("restock complete") {
        (
            Some(TransactionType::Restock),
            Some(config.restock_phrase.clone()),
        )
    } else if lower.contains(&sale_phrase_lower) {
        (
            Some(TransactionType::Sale),
            Some(config.sale_phrase.clone()),
        )
    } else {
        (None, None)
    }
}

/// Detects transaction closing phrase and boundaries using default phrases.
pub fn detect_boundary(transcript: &str) -> (Option<TransactionType>, Option<String>) {
    detect_boundary_with_config(transcript, &BoundaryConfig::default())
}

/// Extracts multi-line receipt line items from transcript sentences.
pub fn extract_lines_from_transcript(
    transcript: &str,
    closing_phrase: Option<&str>,
) -> Vec<ExtractedLine> {
    let mut cleaned = transcript.to_string();
    if let Some(closing) = closing_phrase {
        let lower = cleaned.to_lowercase();
        let target = closing.to_lowercase();
        if let Some(idx) = lower.rfind(&target) {
            cleaned.drain(idx..);
        }
    }

    let sentences: Vec<&str> = cleaned
        .split(|c| c == '.' || c == ';' || c == '\n')
        .map(|s| s.trim())
        .filter(|s| !s.is_empty())
        .collect();

    let mut lines = Vec::new();

    for sent in sentences {
        if let Some(line) = parse_line_clause(sent) {
            lines.push(line);
        }
    }

    lines
}

fn parse_line_clause(clause: &str) -> Option<ExtractedLine> {
    let parts: Vec<&str> = clause.split(',').map(|s| s.trim()).collect();
    if parts.is_empty() {
        return None;
    }

    let item_part = parts[0];
    let amount_part = if parts.len() > 1 {
        Some(parts[1])
    } else {
        None
    };

    let words: Vec<&str> = item_part.split_whitespace().collect();
    if words.is_empty() {
        return None;
    }

    let mut qty: i64 = 1;
    let mut word_idx = 0;

    if let Some(parsed_qty) = parse_word_number(words[0]) {
        qty = parsed_qty;
        word_idx = 1;
    }

    let mut unit = "piece".to_string();
    if word_idx < words.len() {
        let candidate_unit = words[word_idx].to_lowercase();
        if let Some(canonical) = canonicalize_unit(&candidate_unit) {
            unit = canonical;
            word_idx += 1;
            // Skip "of" if present (e.g. "5 packs of Pepsi")
            if word_idx < words.len() && words[word_idx].eq_ignore_ascii_case("of") {
                word_idx += 1;
            }
        }
    }

    let product_name = words[word_idx..].join(" ");
    if product_name.is_empty() {
        return None;
    }

    let mut money = None;
    if let Some(amt_str) = amount_part {
        if let Some(minor) = parse_money_amount(amt_str) {
            money = Some(Money {
                amount: minor,
                currency: "NGN".to_string(),
            });
        }
    }

    Some(ExtractedLine {
        product_name,
        quantity: qty,
        unit,
        amount: money,
    })
}

fn parse_word_number(s: &str) -> Option<i64> {
    if let Ok(num) = s.parse::<i64>() {
        return Some(num);
    }
    match s.to_lowercase().as_str() {
        "one" | "a" => Some(1),
        "two" => Some(2),
        "three" => Some(3),
        "four" => Some(4),
        "five" => Some(5),
        "six" => Some(6),
        "seven" => Some(7),
        "eight" => Some(8),
        "nine" => Some(9),
        "ten" => Some(10),
        "eleven" => Some(11),
        "twelve" => Some(12),
        "twenty" => Some(20),
        _ => None,
    }
}

fn canonicalize_unit(s: &str) -> Option<String> {
    match s.trim_end_matches('s') {
        "carton" => Some("carton".to_string()),
        "pack" => Some("pack".to_string()),
        "bottle" => Some("bottle".to_string()),
        "sachet" => Some("sachet".to_string()),
        "tin" => Some("tin".to_string()),
        "piece" => Some("piece".to_string()),
        "bag" => Some("bag".to_string()),
        "roll" => Some("roll".to_string()),
        _ => None,
    }
}

fn parse_money_amount(s: &str) -> Option<i64> {
    let lower = s.to_lowercase();
    // Example: "twelve thousand naira", "4000", "forty-five thousand naira"
    let mut total_major: i64 = 0;
    let mut current: i64 = 0;

    for word in lower.split(|c: char| c.is_whitespace() || c == '-') {
        if let Ok(num) = word.parse::<i64>() {
            current += num;
        } else {
            match word {
                "one" => current += 1,
                "two" => current += 2,
                "three" => current += 3,
                "four" => current += 4,
                "five" => current += 5,
                "six" => current += 6,
                "seven" => current += 7,
                "eight" => current += 8,
                "nine" => current += 9,
                "ten" => current += 10,
                "eleven" => current += 11,
                "twelve" => current += 12,
                "twenty" => current += 20,
                "thirty" => current += 30,
                "forty" => current += 40,
                "fifty" => current += 50,
                "hundred" => {
                    if current == 0 {
                        current = 1;
                    }
                    current *= 100;
                }
                "thousand" => {
                    if current == 0 {
                        current = 1;
                    }
                    total_major += current * 1000;
                    current = 0;
                }
                "million" => {
                    if current == 0 {
                        current = 1;
                    }
                    total_major += current * 1000000;
                    current = 0;
                }
                _ => {}
            }
        }
    }

    total_major += current;
    if total_major > 0 {
        Some(total_major * 100) // Convert major to minor kobo
    } else {
        None
    }
}
