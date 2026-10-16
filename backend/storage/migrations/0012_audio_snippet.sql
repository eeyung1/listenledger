-- 0012_audio_snippet.sql — Ambient counter audio snippet preservation for review adjudication.

ALTER TABLE transactions ADD COLUMN audio_data TEXT NOT NULL DEFAULT '';

