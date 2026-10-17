/**
 * Ambient Counter Audio Capture & Speech-to-Text Interface.
 *
 * Provides in-browser voice recognition and audio level monitoring for mobile
 * phones, tablets, and counter laptops. Integrates with the Web Speech API
 * (`SpeechRecognition` / `webkitSpeechRecognition`) to continuously listen for
 * counter sales dialogues, detect configurable closing boundary phrases ("Thank you",
 * "Stock received"), and trigger capture ingestion into the backend ledger.
 */

import type { TransactionType } from "./api.js";

export type CounterListenerState = "idle" | "listening" | "processing" | "error";

export interface BoundaryCaptureCandidate {
  readonly transcript: string;
  readonly type: TransactionType | "cancelled";
  readonly closingPhrase: string;
  readonly audioData?: string | undefined;
}

export interface BoundaryDetectionResult {
  readonly detected: boolean;
  readonly type?: TransactionType | "cancelled" | undefined;
  readonly closingPhrase?: string | undefined;
  readonly cleanTranscript?: string | undefined;
}

export type EnvironmentAudioMode = "quiet" | "moderate" | "noisy";

export interface AudioVADMetrics {
  readonly currentLevel: number;
  readonly noiseFloor: number;
  readonly speechThreshold: number;
  readonly isSpeaking: boolean;
  readonly adaptiveSilenceTimeoutMs: number;
  readonly isCalibrating: boolean;
  readonly environmentMode: EnvironmentAudioMode;
  readonly snrDb: number;
}

export interface CounterSpeechListenerOptions {
  readonly salePhrase?: string | undefined;
  readonly restockPhrase?: string | undefined;
  readonly catalogueWords?: readonly string[] | undefined;
  readonly enableVoiceFeedback?: boolean | undefined;
  readonly onStateChange?: ((state: CounterListenerState) => void) | undefined;
  readonly onInterimTranscript?: ((transcript: string) => void) | undefined;
  readonly onBoundaryDetected?: ((candidate: BoundaryCaptureCandidate) => void) | undefined;
  readonly onError?: ((error: Error) => void) | undefined;
  readonly onAudioLevel?: ((level: number) => void) | undefined;
  readonly onWakeLockChange?: ((active: boolean) => void) | undefined;
  readonly onSpeechActivityChange?: ((isSpeaking: boolean) => void) | undefined;
  readonly onVADChange?: ((metrics: AudioVADMetrics) => void) | undefined;
  readonly onSpectralBands?: ((bands: number[]) => void) | undefined;
  /**
   * How far back (ms) to look for the anchor-bearing utterance that led into a
   * standalone closing phrase. Covers the natural pause while a customer pays.
   */
  readonly anchorLookbackMs?: number | undefined;
}

// Minimal browser typing for vendor-prefixed SpeechRecognition
interface SpeechRecognitionResultItem {
  readonly transcript: string;
  readonly confidence: number;
}

interface SpeechRecognitionResultEntry {
  readonly isFinal: boolean;
  readonly length: number;
  [index: number]: SpeechRecognitionResultItem;
}

interface SpeechRecognitionEventLike extends Event {
  readonly resultIndex: number;
  readonly results: {
    readonly length: number;
    [index: number]: SpeechRecognitionResultEntry;
  };
}

interface SpeechRecognitionErrorEventLike extends Event {
  readonly error: string;
  readonly message?: string;
}

// Minimal browser typing for SpeechGrammarList
export interface SpeechGrammarLike {
  src: string;
  weight: number;
}

export interface SpeechGrammarListLike {
  readonly length: number;
  [index: number]: SpeechGrammarLike;
  addFromURI(src: string, weight?: number): void;
  addFromString(string: string, weight?: number): void;
}

export type SpeechGrammarListConstructor = new () => SpeechGrammarListLike;

// Minimal browser typing for WakeLockSentinel
export interface WakeLockSentinelLike extends EventTarget {
  readonly released: boolean;
  readonly type: string;
  release(): Promise<void>;
  onrelease: ((this: WakeLockSentinelLike, ev: Event) => void) | null;
}

interface SpeechRecognitionLike extends EventTarget {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  maxAlternatives: number;
  grammars?: SpeechGrammarListLike;
  start(): void;
  stop(): void;
  abort(): void;
  onstart: ((this: SpeechRecognitionLike, ev: Event) => void) | null;
  onend: ((this: SpeechRecognitionLike, ev: Event) => void) | null;
  onerror: ((this: SpeechRecognitionLike, ev: SpeechRecognitionErrorEventLike) => void) | null;
  onresult: ((this: SpeechRecognitionLike, ev: SpeechRecognitionEventLike) => void) | null;
}

type SpeechRecognitionConstructor = new () => SpeechRecognitionLike;

/** Returns the browser's native SpeechRecognition constructor if available. */
export function getSpeechRecognitionConstructor(): SpeechRecognitionConstructor | null {
  if (typeof window === "undefined") return null;
  const win = window as unknown as {
    SpeechRecognition?: SpeechRecognitionConstructor;
    webkitSpeechRecognition?: SpeechRecognitionConstructor;
  };
  return win.SpeechRecognition ?? win.webkitSpeechRecognition ?? null;
}

/** Checks whether speech recognition is supported in the current browser. */
export function isSpeechRecognitionSupported(): boolean {
  return getSpeechRecognitionConstructor() !== null;
}

/** Returns the browser's native SpeechGrammarList constructor if available. */
export function getSpeechGrammarListConstructor(): SpeechGrammarListConstructor | null {
  if (typeof window === "undefined") return null;
  const win = window as unknown as {
    SpeechGrammarList?: SpeechGrammarListConstructor;
    webkitSpeechGrammarList?: SpeechGrammarListConstructor;
  };
  return win.SpeechGrammarList ?? win.webkitSpeechGrammarList ?? null;
}

/** Checks whether SpeechGrammarList is supported in the current browser. */
export function isSpeechGrammarListSupported(): boolean {
  return getSpeechGrammarListConstructor() !== null;
}

/** Checks whether the Screen Wake Lock API is supported. */
export function isWakeLockSupported(): boolean {
  return (
    typeof navigator !== "undefined" &&
    Boolean((navigator as unknown as { wakeLock?: { request: unknown } }).wakeLock?.request)
  );
}

/** Checks whether SpeechSynthesis is supported in the current browser. */
export function isSpeechSynthesisSupported(): boolean {
  return typeof window !== "undefined" && Boolean(window.speechSynthesis);
}

/** Checks whether microphone audio capture (getUserMedia) is supported. */
export function isAudioCaptureSupported(): boolean {
  return typeof navigator !== "undefined" && Boolean(navigator.mediaDevices?.getUserMedia);
}

/**
 * Evaluates whether a spoken transcript concludes with a known boundary phrase.
 * Normalizes punctuation, case, and surrounding whitespace.
 */
export function detectClosingBoundary(
  transcript: string,
  salePhrase = "thank you",
  restockPhrase = "stock received",
): BoundaryDetectionResult {
  const trimmed = transcript.trim();
  if (!trimmed) {
    return { detected: false };
  }

  // Strip trailing punctuation: . , ! ? ; :
  const normalized = trimmed.replace(/[.,!?;:]+$/, "").trim().toLowerCase();
  const normSale = salePhrase.trim().toLowerCase();
  const normRestock = restockPhrase.trim().toLowerCase();

  if (normSale && (normalized === normSale || normalized.endsWith(" " + normSale))) {
    return {
      detected: true,
      type: "sale",
      closingPhrase: salePhrase,
      cleanTranscript: trimmed,
    };
  }

  if (normRestock && (normalized === normRestock || normalized.endsWith(" " + normRestock))) {
    return {
      detected: true,
      type: "restock",
      closingPhrase: restockPhrase,
      cleanTranscript: trimmed,
    };
  }

  const cancellationPhrases = [
    "cancel sale",
    "cancel the sale",
    "cancel transaction",
    "customer cancelled",
    "customer canceled",
    "cancel that",
    "drop that",
    "never mind",
    "cancel restock",
  ];
  for (const cancelPhrase of cancellationPhrases) {
    if (normalized === cancelPhrase || normalized.endsWith(" " + cancelPhrase)) {
      return {
        detected: true,
        type: "cancelled",
        closingPhrase: cancelPhrase,
        cleanTranscript: trimmed,
      };
    }
  }

  return { detected: false };
}

/** A finalized speech utterance with the time it was recognized. */
export interface TranscriptUtterance {
  readonly text: string;
  readonly at: number;
}

/**
 * Default look-back window for assembling a capture when the closing phrase is
 * spoken in a later result or recognition session than the anchors. The
 * protocol has a natural pause while the customer pays.
 */
export const DEFAULT_ANCHOR_LOOKBACK_MS = 60_000;

const ANCHOR_NUMBER_WORDS = [
  "zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
  "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
  "seventeen", "eighteen", "nineteen", "twenty", "thirty", "forty", "fifty",
  "sixty", "seventy", "eighty", "ninety", "hundred", "thousand", "million",
];

const ANCHOR_CURRENCY_WORDS = ["naira", "kobo", "₦"];

/**
 * Heuristically reports whether an utterance carries capture anchors (a
 * quantity or an amount). A standalone closing phrase such as "Thank you"
 * carries none, so the anchors must be recovered from an earlier utterance.
 */
export function utteranceHasAnchors(text: string): boolean {
  const t = text.trim().toLowerCase();
  if (!t) return false;
  if (/\d/.test(t)) return true;
  if (ANCHOR_CURRENCY_WORDS.some((w) => t.includes(w))) return true;
  return ANCHOR_NUMBER_WORDS.some((w) => new RegExp(`\\b${w}\\b`).test(t));
}

/**
 * Assembles the transcript to submit when a closing boundary fires.
 *
 * If the closing utterance already contains anchors, it is used as-is. When the
 * closing phrase arrives alone — for example a later result or a restarted
 * recognition session, after the customer has finished paying — the most recent
 * anchor-bearing utterance within the look-back window is prepended so the
 * product, quantity and amount are not lost.
 */
export function assembleCaptureTranscript(
  utterances: readonly TranscriptUtterance[],
  closingText: string,
  closingAt: number,
  windowMs: number = DEFAULT_ANCHOR_LOOKBACK_MS,
): string {
  const closing = closingText.trim();
  if (utteranceHasAnchors(closing)) return closing;

  const cutoff = closingAt - windowMs;
  const recent = utterances.filter((u) => u.at >= cutoff && u.text.trim() !== "");

  let anchorStart = -1;
  for (let i = recent.length - 1; i >= 0; i--) {
    if (utteranceHasAnchors(recent[i]!.text)) {
      anchorStart = i;
      break;
    }
  }
  if (anchorStart === -1) return closing;

  const parts = recent.slice(anchorStart).map((u) => u.text.trim());
  const last = parts[parts.length - 1] ?? "";
  const closingLower = closing.toLowerCase();
  if (closing && last.toLowerCase().endsWith(closingLower)) {
    return parts.join(" ");
  }
  return [...parts, closing].filter(Boolean).join(" ");
}

/**
 * Computes Root Mean Square (RMS) energy for audio time-domain samples.
 */
export function calculateAudioRMS(samples: Float32Array | number[]): number {
  if (samples.length === 0) return 0;
  let sumSquares = 0;
  for (let i = 0; i < samples.length; i++) {
    const val = samples[i] ?? 0;
    sumSquares += val * val;
  }
  return Math.sqrt(sumSquares / samples.length);
}

/**
 * Estimates ambient background noise floor from historical audio level samples.
 * Uses 30th percentile filtering to avoid bias from occasional speech or transient sounds.
 */
export function estimateNoiseFloor(levels: readonly number[]): number {
  if (levels.length === 0) return 0.05;
  const sorted = [...levels].sort((a, b) => a - b);
  const idx = Math.floor(sorted.length * 0.3);
  const p30 = sorted[idx] ?? 0.05;
  return Math.max(0.02, Math.min(0.6, Number(p30.toFixed(3))));
}

/**
 * Dynamically computes speech detection threshold above ambient noise floor.
 */
export function calculateSpeechThreshold(noiseFloor: number): number {
  const threshold = Math.max(noiseFloor + 0.07, noiseFloor * 1.55);
  return Math.min(0.85, Number(threshold.toFixed(3)));
}

/**
 * Derives adaptive silence timeout in milliseconds based on ambient noise floor.
 * Quiet environments: ~1200ms for snappy counter turnover.
 * Noisy open markets: up to 2500ms to tolerate background interruptions and cashier pauses.
 */
export function calculateAdaptiveSilenceTimeout(noiseFloor: number): number {
  const minFloor = 0.06;
  const maxFloor = 0.4;
  const minTimeout = 1200;
  const maxTimeout = 2500;
  const clamped = Math.max(minFloor, Math.min(maxFloor, noiseFloor));
  const ratio = (clamped - minFloor) / (maxFloor - minFloor);
  return Math.round(minTimeout + ratio * (maxTimeout - minTimeout));
}

/**
 * Classifies the acoustic environment profile based on calibrated noise floor.
 */
export function classifyEnvironmentMode(noiseFloor: number): EnvironmentAudioMode {
  if (noiseFloor < 0.12) return "quiet";
  if (noiseFloor < 0.28) return "moderate";
  return "noisy";
}

/**
 * Computes the estimated Signal-to-Noise Ratio (SNR) in decibels (dB).
 * Formula: 20 * log10(signalLevel / max(noiseFloor, 0.001))
 * Clamped to a practical range between -6.0 dB and +30.0 dB.
 */
export function calculateSNR(signalLevel: number, noiseFloor: number): number {
  const safeFloor = Math.max(0.001, noiseFloor);
  const safeSignal = Math.max(0.0001, signalLevel);
  const ratio = safeSignal / safeFloor;
  const db = 20 * Math.log10(ratio);
  const clamped = Math.max(-6.0, Math.min(30.0, db));
  return Number(clamped.toFixed(1));
}

/**
 * Aggregates frequency bin data into a normalized discrete multi-band spectral array.
 * Useful for 16-band visualizers representing vocal frequency presence (300Hz - 3.4kHz)
 * and acoustic noise profiles (low-frequency motor/generator hum vs high-frequency hiss).
 */
export function calculateSpectralBands(freqData: Uint8Array | number[], bandCount = 16): number[] {
  if (freqData.length === 0 || bandCount <= 0) {
    return new Array(bandCount).fill(0);
  }
  const result: number[] = [];
  const binsPerBand = Math.max(1, Math.floor(freqData.length / bandCount));
  for (let b = 0; b < bandCount; b++) {
    const start = b * binsPerBand;
    const end = b === bandCount - 1 ? freqData.length : (b + 1) * binsPerBand;
    let sum = 0;
    let count = 0;
    for (let i = start; i < end; i++) {
      sum += (freqData[i] ?? 0);
      count++;
    }
    const avg = count > 0 ? sum / count : 0;
    // Normalize 0..255 byte frequency values to 0.0..1.0
    result.push(Number(Math.min(1.0, Math.max(0.0, avg / 255.0)).toFixed(3)));
  }
  return result;
}


/**
 * Synthesizes an immediate audio chime feedback using the Web Audio API.
 * Never throws an error if audio autoplay is restricted.
 */
export function playAudioFeedback(type: "sale" | "review" | "error" | "start" | "cancelled"): void {
  try {
    if (typeof window === "undefined") return;
    const AudioContextClass =
      window.AudioContext ||
      (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AudioContextClass) return;

    const ctx = new AudioContextClass();
    const now = ctx.currentTime;

    if (type === "start") {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "sine";
      osc.frequency.setValueAtTime(880, now); // A5 blip
      gain.gain.setValueAtTime(0.08, now);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.08);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(now);
      osc.stop(now + 0.08);
    } else if (type === "sale") {
      // Cheerful ascending chime (D5 -> A5)
      const osc1 = ctx.createOscillator();
      const osc2 = ctx.createOscillator();
      const gain = ctx.createGain();

      osc1.type = "sine";
      osc1.frequency.setValueAtTime(587.33, now); // D5
      osc2.type = "sine";
      osc2.frequency.setValueAtTime(880, now + 0.08); // A5

      gain.gain.setValueAtTime(0.12, now);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.28);

      osc1.connect(gain);
      osc2.connect(gain);
      gain.connect(ctx.destination);

      osc1.start(now);
      osc1.stop(now + 0.08);
      osc2.start(now + 0.08);
      osc2.stop(now + 0.28);
    } else if (type === "review") {
      // Gentle warning dual tone (A4 -> F4)
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "triangle";
      osc.frequency.setValueAtTime(440, now);
      osc.frequency.setValueAtTime(349.23, now + 0.12);
      gain.gain.setValueAtTime(0.15, now);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.3);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(now);
      osc.stop(now + 0.3);
    } else if (type === "cancelled") {
      // Descending cancel tone (G4 -> E4)
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "sine";
      osc.frequency.setValueAtTime(392.0, now); // G4
      osc.frequency.setValueAtTime(329.63, now + 0.1); // E4
      gain.gain.setValueAtTime(0.12, now);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.25);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(now);
      osc.stop(now + 0.25);
    } else {
      // Low error buzz
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "sawtooth";
      osc.frequency.setValueAtTime(220, now);
      gain.gain.setValueAtTime(0.1, now);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.18);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(now);
      osc.stop(now + 0.18);
    }
  } catch {
    // Gracefully ignore audio context autoplay restrictions
  }
}

export interface SpeakFeedbackOptions {
  readonly lang?: string | undefined;
  readonly rate?: number | undefined;
  readonly pitch?: number | undefined;
  readonly volume?: number | undefined;
}

/**
 * Speaks an immediate verbal confirmation using the browser's SpeechSynthesis API.
 * Never throws if speech synthesis is unavailable or voice playback is muted.
 */
export function speakFeedback(text: string, options: SpeakFeedbackOptions = {}): boolean {
  if (!isSpeechSynthesisSupported() || typeof window === "undefined") {
    return false;
  }
  try {
    const synth = window.speechSynthesis;
    synth.cancel(); // Stop any pending utterance for snappy counter feedback
    const UtteranceClass =
      (window as unknown as { SpeechSynthesisUtterance?: typeof SpeechSynthesisUtterance })
        .SpeechSynthesisUtterance ??
      (typeof SpeechSynthesisUtterance !== "undefined" ? SpeechSynthesisUtterance : null);
    if (!UtteranceClass) {
      return false;
    }
    const utterance = new UtteranceClass(text);
    utterance.lang = options.lang ?? "en-US";
    utterance.rate = options.rate ?? 1.15; // Brisk pace for counter efficiency
    utterance.pitch = options.pitch ?? 1.0;
    utterance.volume = options.volume ?? 0.9;
    synth.speak(utterance);
    return true;
  } catch {
    return false;
  }
}

// Recognition restart backoff (see CounterSpeechListener.onend).
// Sessions shorter than RESTART_BACKOFF_RESET_MS count as failures; a session
// that lasted at least that long is a normal browser timeout and resets the
// escalation. RESTART_BASE_DELAY_MS doubles per failure up to RESTART_MAX_DELAY_MS.
const RESTART_BASE_DELAY_MS = 150;
const RESTART_MAX_DELAY_MS = 5000;
const RESTART_BACKOFF_RESET_MS = 5000;
const MAX_RESTART_FAILURES = 5;

/**
 * Continuous Counter Ambient Speech Listener.
 *
 * Manages the browser microphone and SpeechRecognition session, maintaining
 * an active listening state, detecting closing boundaries, boosting recognition
 * with catalogue product grammars, and holding screen wake lock for uninterrupted
 * counter operations.
 */
export class CounterSpeechListener {
  private readonly options: CounterSpeechListenerOptions;
  private recognition: SpeechRecognitionLike | null = null;
  private mediaStream: MediaStream | null = null;
  private audioContext: AudioContext | null = null;
  private analyser: AnalyserNode | null = null;
  private animFrameId: number | null = null;
  private isExplicitlyStopped = false;
  private state: CounterListenerState = "idle";
  private currentTranscript = "";
  private wakeLockSentinel: WakeLockSentinelLike | null = null;
  private onVisibilityChangeBound: (() => void) | null = null;
  private catalogueWords: string[] = [];

  // Recognition restart backoff. Chrome and mobile Safari end the recognition
  // session on their own (silence timeouts, tab backgrounding, network drops).
  // Restarting flat-out every 150 ms produces a beep loop on Android when
  // `start()` keeps failing, so escalate the delay and give up loudly after
  // MAX_RESTART_FAILURES consecutive sessions that never actually ran.
  private restartFailures = 0;
  private restartTimer: ReturnType<typeof setTimeout> | null = null;
  private recognitionStartedAt = 0;

  // Rolling, timestamped buffer of finalized utterances so a pause before the
  // closing phrase (across results or recognition restarts) does not lose the
  // product, quantity and amount anchors.
  private speechUtterances: TranscriptUtterance[] = [];
  private currentSessionFinalText = "";

  // Ambient Voice Activity Detection (VAD) & Adaptive Silence State
  private vadNoiseFloor = 0.05;
  private vadSpeechThreshold = 0.12;
  private vadAdaptiveSilenceTimeoutMs = 1200;
  private vadIsSpeaking = false;
  private vadIsCalibrating = false;
  private vadCalibrationSamples: number[] = [];
  private vadCalibrationStartTime = 0;
  private vadSpeechDebounce = 0;
  private vadSilenceDebounce = 0;
  private currentAudioLevel = 0;

  // UI-only spectral meter; raw-audio recording is disabled for the pilot.
  private currentSpectralBands: number[] = new Array(16).fill(0);

  constructor(options: CounterSpeechListenerOptions = {}) {
    this.options = options;
    if (options.catalogueWords) {
      this.setCatalogueWords(options.catalogueWords);
    }
  }

  public getState(): CounterListenerState {
    return this.state;
  }

  public isListening(): boolean {
    return this.state === "listening";
  }

  public isWakeLockActive(): boolean {
    return this.wakeLockSentinel !== null && !this.wakeLockSentinel.released;
  }

  public getCurrentTranscript(): string {
    return this.currentTranscript;
  }

  public getCatalogueWords(): readonly string[] {
    return this.catalogueWords;
  }

  /** Returns current voice activity detection (VAD) and noise floor metrics. */
  public getVADMetrics(): AudioVADMetrics {
    return {
      currentLevel: this.currentAudioLevel,
      noiseFloor: this.vadNoiseFloor,
      speechThreshold: this.vadSpeechThreshold,
      isSpeaking: this.vadIsSpeaking,
      adaptiveSilenceTimeoutMs: this.vadAdaptiveSilenceTimeoutMs,
      isCalibrating: this.vadIsCalibrating,
      environmentMode: classifyEnvironmentMode(this.vadNoiseFloor),
      snrDb: calculateSNR(this.currentAudioLevel, this.vadNoiseFloor),
    };
  }

  /** Returns the latest 16-band normalized frequency spectrum (0.0 to 1.0). */
  public getSpectralBands(): readonly number[] {
    return this.currentSpectralBands;
  }

  /** Returns real-time estimated Signal-to-Noise Ratio (SNR) in dB. */
  public getSNR(): number {
    return calculateSNR(this.currentAudioLevel, this.vadNoiseFloor);
  }

  /** Raw-audio retention is disabled until the P6 policy is implemented. */
  public async exportRecentAudioSnippet(): Promise<string | undefined> {
    return undefined;
  }

  /** Returns true if active voice articulation is currently detected above the ambient noise floor. */
  public isSpeechActive(): boolean {
    return this.vadIsSpeaking;
  }

  /** Returns current real-time audio input level (0.0 to 1.0). */
  public getAudioLevel(): number {
    return this.currentAudioLevel;
  }

  /** Returns the dynamically calculated silence pause timeout in milliseconds. */
  public getAdaptiveSilenceTimeout(): number {
    return this.vadAdaptiveSilenceTimeoutMs;
  }

  /**
   * Resets and triggers an ambient background noise floor calibration cycle.
   * Useful when market acoustic conditions shift (e.g. generator started or street traffic peak).
   */
  public calibrateNoiseFloor(): void {
    this.vadIsCalibrating = true;
    this.vadCalibrationStartTime = Date.now();
    this.vadCalibrationSamples = [];
    this.vadNoiseFloor = 0.05;
    this.vadSpeechThreshold = 0.12;
    this.vadAdaptiveSilenceTimeoutMs = 1200;
    this.vadIsSpeaking = false;
    this.vadSpeechDebounce = 0;
    this.vadSilenceDebounce = 0;
    this.options.onVADChange?.(this.getVADMetrics());
  }

  /**
   * Updates the catalogue vocabulary used to bias the speech recognition grammar.
   * Sanitizes phrases to alphanumeric tokens and updates active recognizer if present.
   */
  public setCatalogueWords(words: readonly string[]): void {
    const sanitized: string[] = [];
    for (const w of words) {
      const trimmed = w.trim().toLowerCase();
      // Remove punctuation and invalid chars from grammar token
      const clean = trimmed.replace(/[^a-z0-9\s-]/g, "").trim();
      if (clean.length > 0) {
        sanitized.push(clean);
      }
    }
    this.catalogueWords = Array.from(new Set(sanitized));

    if (this.recognition) {
      this.applyGrammar(this.recognition);
    }
  }

  /**
   * Starts continuous ambient counter listening.
   * Requests screen wake lock, microphone access, and initializes speech recognition.
   */
  public async start(): Promise<void> {
    const RecognizerClass = getSpeechRecognitionConstructor();
    if (!RecognizerClass) {
      const err = new Error(
        "Speech recognition is not supported in this browser. Please use Google Chrome on Android or Safari on iOS.",
      );
      this.setState("error");
      this.options.onError?.(err);
      throw err;
    }

    this.isExplicitlyStopped = false;
    this.currentTranscript = "";
    this.speechUtterances = [];
    this.currentSessionFinalText = "";
    this.calibrateNoiseFloor();

    // 1. Request Screen Wake Lock so mobile phone display doesn't sleep at counter
    await this.requestWakeLock();
    this.setupVisibilityListener();

    // 2. Initialize audio level analyser if getUserMedia is supported
    await this.initAudioAnalyser();

    // 3. Initialize and configure speech recognition instance
    try {
      this.initRecognition(RecognizerClass);
      this.recognition?.start();
      this.setState("listening");
      playAudioFeedback("start");
    } catch (err) {
      this.setState("error");
      const errorObj = err instanceof Error ? err : new Error(String(err));
      this.options.onError?.(errorObj);
      throw errorObj;
    }
  }

  /** Stops counter speech listening, releases wake lock, and cleans up audio streams. */
  public stop(): void {
    this.isExplicitlyStopped = true;
    this.clearRestartTimer();
    this.restartFailures = 0;
    this.recognitionStartedAt = 0;

    if (this.recognition) {
      try {
        this.recognition.abort();
      } catch {
        // ignore abort errors
      }
      this.recognition = null;
    }

    this.teardownVisibilityListener();
    void this.releaseWakeLock();
    this.teardownAudioAnalyser();
    this.currentTranscript = "";
    this.speechUtterances = [];
    this.currentSessionFinalText = "";
    this.currentAudioLevel = 0;
    this.vadIsSpeaking = false;
    this.vadIsCalibrating = false;
    this.setState("idle");
  }

  private clearRestartTimer(): void {
    if (this.restartTimer !== null) {
      clearTimeout(this.restartTimer);
      this.restartTimer = null;
    }
  }

  private setState(state: CounterListenerState): void {
    if (this.state !== state) {
      this.state = state;
      this.options.onStateChange?.(state);
    }
  }

  private async requestWakeLock(): Promise<void> {
    if (!isWakeLockSupported() || typeof navigator === "undefined") {
      return;
    }
    try {
      const nav = navigator as unknown as {
        wakeLock: { request(type: string): Promise<WakeLockSentinelLike> };
      };
      this.wakeLockSentinel = await nav.wakeLock.request("screen");
      this.wakeLockSentinel.onrelease = () => {
        this.wakeLockSentinel = null;
        this.options.onWakeLockChange?.(false);
      };
      this.options.onWakeLockChange?.(true);
    } catch {
      // Gracefully ignore wake lock failure (e.g. low battery, background tab)
      this.wakeLockSentinel = null;
      this.options.onWakeLockChange?.(false);
    }
  }

  private async releaseWakeLock(): Promise<void> {
    if (this.wakeLockSentinel) {
      try {
        await this.wakeLockSentinel.release();
      } catch {
        // ignore release errors
      }
      this.wakeLockSentinel = null;
      this.options.onWakeLockChange?.(false);
    }
  }

  private setupVisibilityListener(): void {
    if (
      typeof document === "undefined" ||
      typeof document.addEventListener !== "function" ||
      this.onVisibilityChangeBound
    ) {
      return;
    }
    this.onVisibilityChangeBound = () => {
      // Re-request wake lock when user switches back to active tab
      if (document.visibilityState === "visible" && !this.isExplicitlyStopped && this.state === "listening") {
        void this.requestWakeLock();
      }
    };
    document.addEventListener("visibilitychange", this.onVisibilityChangeBound);
  }

  private teardownVisibilityListener(): void {
    if (
      typeof document !== "undefined" &&
      typeof document.removeEventListener === "function" &&
      this.onVisibilityChangeBound
    ) {
      document.removeEventListener("visibilitychange", this.onVisibilityChangeBound);
      this.onVisibilityChangeBound = null;
    }
  }

  private applyGrammar(recognition: SpeechRecognitionLike): void {
    if (this.catalogueWords.length === 0) return;
    const GrammarClass = getSpeechGrammarListConstructor();
    if (!GrammarClass) return;

    try {
      const grammarList = new GrammarClass();
      const grammarTerms = this.catalogueWords.join(" | ");
      const jsgf = `#JSGF V1.0; grammar products; public <product> = ${grammarTerms} ;`;
      grammarList.addFromString(jsgf, 1.0);
      recognition.grammars = grammarList;
    } catch {
      // Non-critical if browser grammar compilation throws
    }
  }

  /**
   * Appends newly finalized speech to the rolling utterance buffer, updating the
   * current session's entry as it grows and starting a new entry after a
   * recognition restart. Bounded to the most recent 20 utterances.
   */
  private recordFinalUtterance(finalText: string, at: number): void {
    const ft = finalText.trim();
    if (!ft || ft === this.currentSessionFinalText) return;

    const growsCurrent =
      this.currentSessionFinalText !== "" &&
      ft.startsWith(this.currentSessionFinalText) &&
      this.speechUtterances.length > 0;

    if (growsCurrent) {
      this.speechUtterances[this.speechUtterances.length - 1] = { text: ft, at };
    } else {
      this.speechUtterances.push({ text: ft, at });
    }
    this.currentSessionFinalText = ft;

    if (this.speechUtterances.length > 20) {
      this.speechUtterances.shift();
    }
  }

  private initRecognition(RecognizerClass: SpeechRecognitionConstructor): void {
    const recognition = new RecognizerClass();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = "en-US";
    recognition.maxAlternatives = 1;

    this.applyGrammar(recognition);

    recognition.onstart = () => {
      this.recognitionStartedAt = Date.now();
      if (!this.isExplicitlyStopped) {
        this.setState("listening");
      }
    };

    recognition.onresult = (event: SpeechRecognitionEventLike) => {
      // Gather the full session transcript. Iterating from the first result (not
      // `resultIndex`) keeps earlier text in the same session, and tracking the
      // finalized text separately lets us buffer utterances across restarts.
      let accumulated = "";
      let finalText = "";
      for (let i = 0; i < event.results.length; i++) {
        const entry = event.results[i];
        const item = entry?.[0];
        if (!item) continue;
        accumulated += item.transcript;
        if (entry.isFinal) {
          finalText += item.transcript;
        }
      }

      const text = accumulated.trim();
      if (!text) return;

      this.recordFinalUtterance(finalText, Date.now());

      this.currentTranscript = text;
      this.options.onInterimTranscript?.(text);

      const boundary = detectClosingBoundary(
        text,
        this.options.salePhrase ?? "thank you",
        this.options.restockPhrase ?? "stock received",
      );

      if (boundary.detected && boundary.type && boundary.closingPhrase && boundary.cleanTranscript) {
        const cleanText = boundary.cleanTranscript;
        const boundaryType = boundary.type;
        const phrase = boundary.closingPhrase;

        // Recover anchors spoken before the closing phrase when it arrives in a
        // later result or a restarted recognition session after a payment pause.
        const assembled = assembleCaptureTranscript(
          this.speechUtterances,
          cleanText,
          Date.now(),
          this.options.anchorLookbackMs,
        );

        this.currentTranscript = "";
        this.speechUtterances = [];
        this.currentSessionFinalText = "";

        this.options.onBoundaryDetected?.({
          transcript: assembled,
          type: boundaryType,
          closingPhrase: phrase,
        });
      }
    };

    recognition.onerror = (event: SpeechRecognitionErrorEventLike) => {
      if (this.isExplicitlyStopped) return;

      if (event.error === "no-speech") {
        // Normal ambient pause — do not fail, keep listening
        return;
      }

      if (event.error === "not-allowed") {
        const error = new Error(
          "Microphone permission was denied. Tap 'Allow' in your browser settings to enable voice counter capture.",
        );
        this.setState("error");
        this.options.onError?.(error);
        return;
      }

      // Log minor errors and attempt recovery on next cycle
      if (event.error !== "aborted") {
        this.options.onError?.(new Error(`Speech recognition notice: ${event.error}`));
      }
    };

    recognition.onend = () => {
      // If recognition stopped unexpectedly (e.g. mobile browser timeout) and user hasn't stopped it:
      if (this.isExplicitlyStopped) {
        this.setState("idle");
        return;
      }

      // A session that stayed up for a while is a normal timeout, not a failure
      // loop, so the next restart goes back to the short delay.
      const sessionMs = this.recognitionStartedAt > 0 ? Date.now() - this.recognitionStartedAt : 0;
      if (sessionMs >= RESTART_BACKOFF_RESET_MS) {
        this.restartFailures = 0;
      }

      if (this.restartFailures >= MAX_RESTART_FAILURES) {
        this.setState("error");
        this.options.onError?.(
          new Error(
            "Voice listening stopped repeatedly and is no longer retrying. Check the connection, then tap Retry Listening.",
          ),
        );
        return;
      }

      const delayMs = Math.min(
        RESTART_BASE_DELAY_MS * 2 ** this.restartFailures,
        RESTART_MAX_DELAY_MS,
      );
      this.restartFailures++;
      this.clearRestartTimer();
      this.restartTimer = setTimeout(() => {
        this.restartTimer = null;
        if (this.isExplicitlyStopped) return;
        try {
          this.recognition?.start();
        } catch {
          // onend fires again and the next backoff step is scheduled
        }
      }, delayMs);
    };

    this.recognition = recognition;
  }

  private async initAudioAnalyser(): Promise<void> {
    if (!isAudioCaptureSupported()) return;

    try {
      this.mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });

      const AudioContextClass =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!AudioContextClass) return;

      this.audioContext = new AudioContextClass();
      if (this.audioContext.state === "suspended") {
        await this.audioContext.resume();
      }

      const source = this.audioContext.createMediaStreamSource(this.mediaStream);
      this.analyser = this.audioContext.createAnalyser();
      this.analyser.fftSize = 256;
      source.connect(this.analyser);

      this.startLevelLoop();
    } catch {
      // Microphone audio metering is non-critical fallback; continue without VU meter
    }
  }

  private startLevelLoop(): void {
    if (!this.analyser) return;
    const freqData = new Uint8Array(this.analyser.frequencyBinCount);
    const floatTimeData =
      typeof this.analyser.getFloatTimeDomainData === "function"
        ? new Float32Array(this.analyser.fftSize)
        : null;

    const updateLevel = () => {
      if (this.isExplicitlyStopped || !this.analyser) {
        return;
      }

      this.analyser.getByteFrequencyData(freqData);
      const bands = calculateSpectralBands(freqData, 16);
      this.currentSpectralBands = bands;
      this.options.onSpectralBands?.(bands);

      let level = 0;
      if (floatTimeData && typeof this.analyser.getFloatTimeDomainData === "function") {
        this.analyser.getFloatTimeDomainData(floatTimeData);
        const rms = calculateAudioRMS(floatTimeData);
        // Normalize RMS: 0.35 RMS is typical loud speech peak
        level = Math.min(1.0, Math.max(0.0, rms / 0.35));
      } else {
        let sum = 0;
        for (let i = 0; i < freqData.length; i++) {
          sum += (freqData[i] ?? 0);
        }
        const avg = sum / freqData.length;
        level = Math.min(1.0, avg / 128.0);
      }

      this.currentAudioLevel = Number(level.toFixed(3));
      this.options.onAudioLevel?.(this.currentAudioLevel);

      // 1. Initial Calibration Window (~1200ms or 35 frames)
      if (this.vadIsCalibrating) {
        this.vadCalibrationSamples.push(this.currentAudioLevel);
        const elapsed = Date.now() - this.vadCalibrationStartTime;
        if (this.vadCalibrationSamples.length >= 35 || elapsed >= 1200) {
          this.vadIsCalibrating = false;
          this.vadNoiseFloor = estimateNoiseFloor(this.vadCalibrationSamples);
          this.vadSpeechThreshold = calculateSpeechThreshold(this.vadNoiseFloor);
          this.vadAdaptiveSilenceTimeoutMs = calculateAdaptiveSilenceTimeout(this.vadNoiseFloor);
        } else {
          this.vadNoiseFloor = estimateNoiseFloor(this.vadCalibrationSamples);
          this.vadSpeechThreshold = calculateSpeechThreshold(this.vadNoiseFloor);
          this.vadAdaptiveSilenceTimeoutMs = calculateAdaptiveSilenceTimeout(this.vadNoiseFloor);
        }
      } else {
        // 2. Slow Continuous Ambient Noise Floor Tracking when idle
        if (!this.vadIsSpeaking && this.currentAudioLevel < this.vadNoiseFloor * 1.35) {
          this.vadNoiseFloor = Number(
            (0.985 * this.vadNoiseFloor + 0.015 * this.currentAudioLevel).toFixed(3)
          );
          this.vadSpeechThreshold = calculateSpeechThreshold(this.vadNoiseFloor);
          this.vadAdaptiveSilenceTimeoutMs = calculateAdaptiveSilenceTimeout(this.vadNoiseFloor);
        }

        // 3. Speech Activity Gating & Debouncing
        if (this.currentAudioLevel >= this.vadSpeechThreshold) {
          this.vadSpeechDebounce++;
          this.vadSilenceDebounce = 0;
          if (this.vadSpeechDebounce >= 2 && !this.vadIsSpeaking) {
            this.vadIsSpeaking = true;
            this.options.onSpeechActivityChange?.(true);
          }
        } else {
          this.vadSilenceDebounce++;
          this.vadSpeechDebounce = 0;
          // ~10 frames (~160-250ms) hangover tolerance for pauses between words
          if (this.vadSilenceDebounce >= 10 && this.vadIsSpeaking) {
            this.vadIsSpeaking = false;
            this.options.onSpeechActivityChange?.(false);
          }
        }
      }

      this.options.onVADChange?.(this.getVADMetrics());

      this.animFrameId = requestAnimationFrame(updateLevel);
    };

    this.animFrameId = requestAnimationFrame(updateLevel);
  }

  private teardownAudioAnalyser(): void {
    if (this.animFrameId !== null) {
      cancelAnimationFrame(this.animFrameId);
      this.animFrameId = null;
    }

    this.currentSpectralBands = new Array(16).fill(0);
    this.options.onSpectralBands?.(this.currentSpectralBands);

    if (this.mediaStream) {
      this.mediaStream.getTracks().forEach((track) => track.stop());
      this.mediaStream = null;
    }

    if (this.audioContext && this.audioContext.state !== "closed") {
      void this.audioContext.close();
      this.audioContext = null;
    }

    this.analyser = null;
    this.currentAudioLevel = 0;
    this.vadIsSpeaking = false;
    this.vadIsCalibrating = false;
    this.options.onAudioLevel?.(0);
    this.options.onSpeechActivityChange?.(false);
    this.options.onVADChange?.(this.getVADMetrics());
  }
}

