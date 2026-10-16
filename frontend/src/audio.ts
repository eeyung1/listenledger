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
  readonly type: TransactionType;
  readonly closingPhrase: string;
}

export interface BoundaryDetectionResult {
  readonly detected: boolean;
  readonly type?: TransactionType | undefined;
  readonly closingPhrase?: string | undefined;
  readonly cleanTranscript?: string | undefined;
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

  return { detected: false };
}

/**
 * Synthesizes an immediate audio chime feedback using the Web Audio API.
 * Never throws an error if audio autoplay is restricted.
 */
export function playAudioFeedback(type: "sale" | "review" | "error" | "start"): void {
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
    this.setState("idle");
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

  private initRecognition(RecognizerClass: SpeechRecognitionConstructor): void {
    const recognition = new RecognizerClass();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = "en-US";
    recognition.maxAlternatives = 1;

    this.applyGrammar(recognition);

    recognition.onstart = () => {
      if (!this.isExplicitlyStopped) {
        this.setState("listening");
      }
    };

    recognition.onresult = (event: SpeechRecognitionEventLike) => {
      let accumulated = "";
      for (let i = event.resultIndex; i < event.results.length; i++) {
        const item = event.results[i]?.[0];
        if (item) {
          accumulated += item.transcript;
        }
      }

      const text = accumulated.trim();
      if (!text) return;

      this.currentTranscript = text;
      this.options.onInterimTranscript?.(text);

      const boundary = detectClosingBoundary(
        text,
        this.options.salePhrase ?? "thank you",
        this.options.restockPhrase ?? "stock received",
      );

      if (boundary.detected && boundary.type && boundary.closingPhrase && boundary.cleanTranscript) {
        const candidate: BoundaryCaptureCandidate = {
          transcript: boundary.cleanTranscript,
          type: boundary.type,
          closingPhrase: boundary.closingPhrase,
        };

        // Notify listener that an authoritative boundary phrase was spoken
        this.options.onBoundaryDetected?.(candidate);
        this.currentTranscript = "";
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
      if (!this.isExplicitlyStopped) {
        setTimeout(() => {
          if (!this.isExplicitlyStopped) {
            try {
              this.recognition?.start();
            } catch {
              // will restart on next frame
            }
          }
        }, 150);
      } else {
        this.setState("idle");
      }
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
    const dataArray = new Uint8Array(this.analyser.frequencyBinCount);

    const updateLevel = () => {
      if (this.isExplicitlyStopped || !this.analyser) {
        return;
      }

      this.analyser.getByteFrequencyData(dataArray);
      let sum = 0;
      for (let i = 0; i < dataArray.length; i++) {
        sum += (dataArray[i] ?? 0);
      }
      const avg = sum / dataArray.length;
      // Normalize from 0..255 to 0.0..1.0
      const level = Math.min(1.0, avg / 128.0);
      this.options.onAudioLevel?.(level);

      this.animFrameId = requestAnimationFrame(updateLevel);
    };

    this.animFrameId = requestAnimationFrame(updateLevel);
  }

  private teardownAudioAnalyser(): void {
    if (this.animFrameId !== null) {
      cancelAnimationFrame(this.animFrameId);
      this.animFrameId = null;
    }

    if (this.mediaStream) {
      this.mediaStream.getTracks().forEach((track) => track.stop());
      this.mediaStream = null;
    }

    if (this.audioContext && this.audioContext.state !== "closed") {
      void this.audioContext.close();
      this.audioContext = null;
    }

    this.analyser = null;
    this.options.onAudioLevel?.(0);
  }
}
