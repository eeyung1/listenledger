//! Health diagnostics and status monitoring for the ListenLedger Edge Capture Engine.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EdgeHealthReport {
    pub backend_url: String,
    pub backend_reachable: bool,
    pub queue_len: usize,
    pub queue_capacity: usize,
    pub queue_utilization_pct: f64,
    pub microphone_active: bool,
    pub stt_model_loaded: bool,
    pub last_successful_delivery: Option<String>,
    pub active_issues: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct EdgeHealthMonitor {
    backend_url: String,
    microphone_active: bool,
    stt_model_loaded: bool,
    last_delivery: Option<String>,
}

impl EdgeHealthMonitor {
    pub fn new(backend_url: impl Into<String>) -> Self {
        Self {
            backend_url: backend_url.into(),
            microphone_active: true,
            stt_model_loaded: true,
            last_delivery: None,
        }
    }

    pub fn set_microphone_status(&mut self, active: bool) {
        self.microphone_active = active;
    }

    pub fn set_stt_model_status(&mut self, loaded: bool) {
        self.stt_model_loaded = loaded;
    }

    pub fn record_delivery_success(&mut self, timestamp: String) {
        self.last_delivery = Some(timestamp);
    }

    pub fn report(&self, queue_len: usize, queue_capacity: usize, backend_reachable: bool) -> EdgeHealthReport {
        let mut issues = Vec::new();

        if !self.microphone_active {
            issues.push("Microphone capture inactive or disconnected: check audio input device".to_string());
        }
        if !self.stt_model_loaded {
            issues.push("Speech model not loaded: check model weights path and memory limits".to_string());
        }
        if !backend_reachable {
            issues.push(format!("Backend unreachable at {}: captures will queue offline", self.backend_url));
        }
        let utilization = if queue_capacity > 0 {
            (queue_len as f64 / queue_capacity as f64) * 100.0
        } else {
            0.0
        };
        if utilization >= 90.0 {
            issues.push(format!("Queue capacity warning ({}% full): risk of dropping oldest captures if offline continues", utilization as u64));
        }

        EdgeHealthReport {
            backend_url: self.backend_url.clone(),
            backend_reachable,
            queue_len,
            queue_capacity,
            queue_utilization_pct: utilization,
            microphone_active: self.microphone_active,
            stt_model_loaded: self.stt_model_loaded,
            last_successful_delivery: self.last_delivery.clone(),
            active_issues: issues,
        }
    }
}

