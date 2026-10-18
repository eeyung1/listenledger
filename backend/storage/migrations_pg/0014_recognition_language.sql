-- 0014_recognition_language.sql — Configurable speech recognition language (P4.4).
-- The counter listener previously hard-coded the browser's en-US recognizer.
-- Make it a per-shop setting. The default keeps the existing en-US baseline
-- until P2 (the STT bake-off) measures and selects the winner.

ALTER TABLE shops ADD COLUMN recognition_language TEXT NOT NULL DEFAULT 'en-US';
