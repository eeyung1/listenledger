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
    /// Gracefully recovers valid lines even if interrupted writes left corrupted trailing lines.
    pub fn open<P: AsRef<Path>>(path: P, capacity: usize) -> Result<Self, QueueError> {
        let file_path = path.as_ref().to_path_buf();
        let mut in_memory = VecDeque::new();
        let mut had_corrupted_lines = false;

        if file_path.exists() {
            let file = File::open(&file_path)?;
            let reader = BufReader::new(file);
            for line in reader.lines() {
                let line_str = match line {
                    Ok(l) => l,
                    Err(e) => {
                        tracing::warn!("Queue recovery encountered read error: {}", e);
                        had_corrupted_lines = true;
                        continue;
                    }
                };
                let trimmed = line_str.trim();
                if trimmed.is_empty() {
                    continue;
                }
                match serde_json::from_str::<CapturePayload>(trimmed) {
                    Ok(item) => in_memory.push_back(item),
                    Err(err) => {
                        tracing::warn!("Discarding corrupt queue entry (incomplete write): {}", err);
                        had_corrupted_lines = true;
                    }
                }
            }
        }

        let queue = Self {
            file_path,
            in_memory,
            capacity,
        };

        if had_corrupted_lines {
            // Rewrite the clean set to disk
            queue.persist_all()?;
        }

        Ok(queue)
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

    /// Removes acknowledged capture and atomically rewrites durable queue file.
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

    pub fn capacity(&self) -> usize {
        self.capacity
    }

    /// Atomically writes in-memory items to a temporary file and renames it over the target,
    /// preventing truncated or corrupt files if a power cut occurs during compaction.
    fn persist_all(&self) -> Result<(), QueueError> {
        let tmp_path = self.file_path.with_extension("tmp");
        {
            let mut file = OpenOptions::new()
                .create(true)
                .write(true)
                .truncate(true)
                .open(&tmp_path)?;

            for item in &self.in_memory {
                let serialized = serde_json::to_string(item)?;
                writeln!(file, "{}", serialized)?;
            }
            file.sync_data()?;
        }

        std::fs::rename(&tmp_path, &self.file_path)?;
        Ok(())
    }
}
