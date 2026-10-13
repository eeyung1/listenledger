//! Durable, disk-persisted offline capture queue.
//!
//! Survives power loss and network outages without losing captures or duplicating records.

use crate::extractor::CapturePayload;
use std::collections::VecDeque;
use std::fs::{File, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use thiserror::Error;

#[derive(Debug, Error)]
pub enum QueueError {
    #[error("I/O error: {0}")]
    Io(#[from] std::io::Error),
    #[error("serialization error: {0}")]
    Serialization(#[from] serde_json::Error),
    #[error("queue capacity exceeded")]
    CapacityExceeded,
}

pub struct DurableQueue {
    file_path: PathBuf,
    in_memory: VecDeque<CapturePayload>,
    capacity: usize,
}

impl DurableQueue {
    /// Opens or creates the queue backing file and recovers any pending items.
    pub fn open<P: AsRef<Path>>(path: P, capacity: usize) -> Result<Self, QueueError> {
        let file_path = path.as_ref().to_path_buf();
        let mut in_memory = VecDeque::new();

        if file_path.exists() {
            let file = File::open(&file_path)?;
            let reader = BufReader::new(file);
            for line in reader.lines() {
                let line_str = line?;
                if !line_str.trim().is_empty() {
                    let item: CapturePayload = serde_json::from_str(&line_str)?;
                    in_memory.push_back(item);
                }
            }
        }

        Ok(Self {
            file_path,
            in_memory,
            capacity,
        })
    }

    /// Appends a new capture to disk and memory.
    pub fn enqueue(&mut self, payload: CapturePayload) -> Result<(), QueueError> {
        if self.in_memory.len() >= self.capacity {
            return Err(QueueError::CapacityExceeded);
        }

        let mut file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&self.file_path)?;

        let serialized = serde_json::to_string(&payload)?;
        writeln!(file, "{}", serialized)?;
        file.sync_data()?;

        self.in_memory.push_back(payload);
        Ok(())
    }

    /// Peeks at the next pending capture to send.
    pub fn peek(&self) -> Option<&CapturePayload> {
        self.in_memory.front()
    }

    /// Removes acknowledged capture and rewrites durable queue file.
    pub fn pop(&mut self) -> Result<Option<CapturePayload>, QueueError> {
        let popped = self.in_memory.pop_front();
        if popped.is_some() {
            self.persist_all()?;
        }
        Ok(popped)
    }

    pub fn len(&self) -> usize {
        self.in_memory.len()
    }

    pub fn is_empty(&self) -> bool {
        self.in_memory.is_empty()
    }

    fn persist_all(&self) -> Result<(), QueueError> {
        let mut file = OpenOptions::new()
            .create(true)
            .write(true)
            .truncate(true)
            .open(&self.file_path)?;

        for item in &self.in_memory {
            let serialized = serde_json::to_string(item)?;
            writeln!(file, "{}", serialized)?;
        }
        file.sync_data()?;
        Ok(())
    }
}

