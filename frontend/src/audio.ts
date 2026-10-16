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
  readonly onStateChange?: ((state: CounterListenerState) => void) | undefined;
  readonly onInterimTranscript?: ((transcript: string) => void) | undefined;
  readonly onBoundaryDetected?: ((candidate: BoundaryCaptureCandidate) => void) | undefined;
  readonly onError?: ((error: Error) => void) | undefined;
  readonly onAudioLevel?: ((level: number) => void) | undefined;
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

interface SpeechRecognitionLike extends EventTarget {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  maxAlternatives: number;
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

/**
 * Continuous Counter Ambient Speech Listener.
 *
 * Manages the browser microphone and SpeechRecognition session, maintaining
 * an active listening state, detecting closing boundaries, and calculating audio
 * levels for visual display.
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

  constructor(options: CounterSpeechListenerOptions = {}) {
    this.options = options;
  }

  public getState(): CounterListenerState {
    return this.state;
  }

  public isListening(): boolean {
    return this.state === "listening";
  }

  public getCurrentTranscript(): string {
    return this.currentTranscript;
  }

  /**
   * Starts continuous ambient counter listening.
   * Requests microphone access if needed and initializes speech recognition.
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

    // 1. Initialize audio level analyser if getUserMedia is supported
    await this.initAudioAnalyser();

    // 2. Initialize and configure speech recognition instance
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

  /** Stops counter speech listening and releases microphone streams. */
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

  private initRecognition(RecognizerClass: SpeechRecognitionConstructor): void {
    const recognition = new RecognizerClass();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = "en-US";
    recognition.maxAlternatives = 1;

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
