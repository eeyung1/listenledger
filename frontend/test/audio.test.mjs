import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  detectClosingBoundary,
  utteranceHasAnchors,
  assembleCaptureTranscript,
  DEFAULT_ANCHOR_LOOKBACK_MS,
  isSpeechRecognitionSupported,
  isAudioCaptureSupported,
  isWakeLockSupported,
  isSpeechGrammarListSupported,
  isSpeechSynthesisSupported,
  speakFeedback,
  CounterSpeechListener,
  playAudioFeedback,
  calculateAudioRMS,
  estimateNoiseFloor,
  calculateSpeechThreshold,
  calculateAdaptiveSilenceTimeout,
  classifyEnvironmentMode,
  calculateSNR,
  calculateSpectralBands,
} from "../dist/audio.js";
import { postCapture } from "../dist/api.js";
import { clearQueue, getQueuedCount } from "../dist/offline_queue.js";
import { mountHome } from "../dist/views/home.js";

const HOME_HTML = fs.readFileSync(
  new URL("../src/components/home.html", import.meta.url),
  "utf8"
);

test("Counter Audio & Speech-to-Text Pipeline Suite", async (t) => {
  const { document, window } = setupDOMEnvironment();
  const originalFetch = globalThis.fetch;

  t.afterEach(() => {
    globalThis.fetch = originalFetch;
    document.body.replaceChildren();
    delete window.SpeechRecognition;
    delete window.webkitSpeechRecognition;
  });

  await t.test("1. Closing Boundary Detection: normalizes punctuation and identifies phrases", () => {
    // Standard sale phrase
    const res1 = detectClosingBoundary("Two Milo, four thousand naira. Thank you.", "Thank you", "Stock received");
    assert.equal(res1.detected, true);
    assert.equal(res1.type, "sale");
    assert.equal(res1.closingPhrase, "Thank you");

    // Case-insensitivity & trailing punctuation
    const res2 = detectClosingBoundary("Two Peak Milk, seven thousand naira! THANK YOU!!!", "thank you", "stock received");
    assert.equal(res2.detected, true);
    assert.equal(res2.type, "sale");

    // Restock boundary
    const res3 = detectClosingBoundary("Three cartons of Milo. Stock received.", "Thank you", "Stock received");
    assert.equal(res3.detected, true);
    assert.equal(res3.type, "restock");
    assert.equal(res3.closingPhrase, "Stock received");

    // Negative case: casual counter banter without closing phrase
    const res4 = detectClosingBoundary("Good morning, do you have Milo?", "Thank you", "Stock received");
    assert.equal(res4.detected, false);

    // Negative case: phrase appears in middle but does not conclude sentence
    const res5 = detectClosingBoundary("Thank you for coming, how much is the milk?", "Thank you", "Stock received");
    assert.equal(res5.detected, false);

    // Empty text
    const res6 = detectClosingBoundary("   ", "Thank you", "Stock received");
    assert.equal(res6.detected, false);
  });

  await t.test("2. Speech Feature Detection & Audio Feedback", () => {
    // When no SpeechRecognition is in window
    delete window.SpeechRecognition;
    delete window.webkitSpeechRecognition;
    assert.equal(isSpeechRecognitionSupported(), false);

    // When SpeechRecognition is mocked on window
    class MockSpeechRecognition {}
    window.SpeechRecognition = MockSpeechRecognition;
    assert.equal(isSpeechRecognitionSupported(), true);

    // playAudioFeedback should execute without errors even if audio context is unsupported in test env
    assert.doesNotThrow(() => {
      playAudioFeedback("start");
      playAudioFeedback("sale");
      playAudioFeedback("review");
      playAudioFeedback("error");
    });
  });

  await t.test("3. API Client: postCapture sends intake request and returns result", async () => {
    let calledUrl = "";
    let calledMethod = "";
    let calledBody = null;

    globalThis.fetch = async (url, init = {}) => {
      calledUrl = String(url);
      calledMethod = init.method || "GET";
      calledBody = init.body ? JSON.parse(String(init.body)) : null;

      return {
        ok: true,
        status: 201,
        json: async () => ({
          capture_id: "cap_test_123",
          shop_id: "shop_test",
          type: "sale",
          transactions: [
            {
              id: "txn_001",
              shop_id: "shop_test",
              type: "sale",
              product_name: "Milo 500g",
              quantity: 2,
              unit: "pack",
              amount: { amount: 400000, currency: "NGN" },
              confidence: { overall: 0.95 },
              status: "confirmed",
              occurred_at: new Date().toISOString(),
              created_at: new Date().toISOString(),
            },
          ],
          requires_review: false,
        }),
      };
    };

    const result = await postCapture({
      transcript: "Two Milo, four thousand naira. Thank you.",
      type: "sale",
      closing_phrase: "Thank you",
      audio_data: "data:audio/webm;base64,legacy-snippet",
    });

    assert.equal(calledBody.audio_data, undefined);
    assert.equal(calledUrl, "/api/captures");
    assert.equal(calledMethod, "POST");
    assert.equal(calledBody.transcript, "Two Milo, four thousand naira. Thank you.");
    assert.equal(calledBody.type, "sale");
    assert.equal(result.capture_id, "cap_test_123");
    assert.equal(result.requires_review, false);
    assert.equal(result.transactions[0].product_name, "Milo 500g");
  });

  await t.test("4. CounterSpeechListener: state transitions and boundary detection callback", async () => {
    let recognitionInstance = null;

    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {
        this.onend?.(new MockEvent("end"));
      }
      stop() {
        this.onend?.(new MockEvent("end"));
      }
    }
    window.SpeechRecognition = MockSpeechRecognition;

    let currentState = "idle";
    let capturedCandidate = null;
    let interimHeard = "";

    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      restockPhrase: "Stock received",
      onStateChange: (st) => {
        currentState = st;
      },
      onInterimTranscript: (text) => {
        interimHeard = text;
      },
      onBoundaryDetected: (cand) => {
        capturedCandidate = cand;
      },
    });

    await listener.start();
    assert.equal(listener.isListening(), true);
    assert.equal(currentState, "listening");

    // Simulate speech event with boundary phrase
    const speechResult = [{ transcript: "Two Peak Milk, seven thousand naira. Thank you.", confidence: 0.92 }];
    speechResult.isFinal = true; // P4.3: boundary detection acts only on finalized results
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [speechResult],
    });

    assert.equal(interimHeard, "Two Peak Milk, seven thousand naira. Thank you.");
    assert.notEqual(capturedCandidate, null);
    assert.equal(capturedCandidate.type, "sale");
    assert.equal(capturedCandidate.closingPhrase, "Thank you");
    assert.equal(capturedCandidate.transcript, "Two Peak Milk, seven thousand naira. Thank you.");

    listener.stop();
    assert.equal(listener.isListening(), false);
    assert.equal(currentState, "idle");
  });

  await t.test("5. Home Overview: mounts Counter Audio Dock with interactive controls", async () => {
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 5,
            today_sales_amount: { amount: 2000000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 2000000, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            transactions: [],
            total: 0,
            limit: 20,
            offset: 0,
          }),
        };
      }
      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [],
            low_stock_count: 0,
          }),
        };
      }
      if (u.includes("/api/onboarding/status")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            is_complete: true,
            completed_steps: 6,
            total_steps: 6,
            steps: {
              profile: { completed: true, title: "Profile" },
              catalogue: { completed: true, title: "Catalogue" },
              devices: { completed: true, title: "Devices" },
              staff_training: { completed: true, title: "Staff" },
              target_gates: { completed: true, title: "Gates" },
              test_capture: { completed: true, title: "Test", count: 1 },
            },
          }),
        };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            total_customers_with_debt: 0,
            total_outstanding_debt: { amount: 0, currency: "NGN" },
            today_credit_sales_amount: { amount: 0, currency: "NGN" },
            today_repayments_amount: { amount: 0, currency: "NGN" },
          }),
        };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");

    const counterDock = document.querySelector("[data-hook='counter-dock']");
    const micDot = document.querySelector("[data-hook='counter-mic-dot']");
    const micTitle = document.querySelector("[data-hook='counter-mic-title']");
    const micToggleBtn = document.querySelector("[data-hook='counter-mic-toggle-btn']");
    const micBtnLabel = document.querySelector("[data-hook='counter-mic-btn-label']");

    assert.notEqual(counterDock, null, "counter dock element must exist");
    assert.notEqual(micDot, null, "mic dot element must exist");
    assert.equal(micTitle.textContent, "Counter Microphone");
    assert.equal(micBtnLabel.textContent, "Start Listening");

    // Click toggle button to start listening
    micToggleBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(micTitle.textContent, "Counter Microphone: Active");
    assert.equal(micBtnLabel.textContent, "Stop Listening");

    // Click toggle button to stop listening
    micToggleBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(micTitle.textContent, "Counter Microphone");
    assert.equal(micBtnLabel.textContent, "Start Listening");

    cleanup();
  });

  await t.test("6. Onboarding Modal: renders Phone Mic & Text tabs and evaluates spoken test", async () => {
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/onboarding/test-capture")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            status: "recognized",
            recognized: true,
            type: "sale",
            closing_phrase: "Thank you",
            boundary_detected: true,
            lines: [
              {
                product_name: "Peak Milk",
                matched_by: "alias",
                quantity: 2,
                unit: "pack",
                amount: { amount: 700000, currency: "NGN" },
                confidence: { overall: 0.96 },
                status: "confirmed",
                review_reasons: [],
              },
            ],
            summary: "Recognized 1 item: 2 Peak Milk",
          }),
        };
      }
      if (u.includes("/api/onboarding/status")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            is_complete: false,
            completed_steps: 5,
            total_steps: 6,
            steps: {
              profile: { completed: true, title: "Profile" },
              catalogue: { completed: true, title: "Catalogue" },
              devices: { completed: true, title: "Devices" },
              staff_training: { completed: true, title: "Staff" },
              target_gates: { completed: true, title: "Gates" },
              test_capture: { completed: false, title: "Sound & Capture Test", description: "Verify speech recognition" },
            },
          }),
        };
      }
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 0,
            today_sales_amount: { amount: 0, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 0, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return { ok: true, status: 200, json: async () => ({ transactions: [], total: 0, limit: 20, offset: 0 }) };
      }
      if (u.includes("/api/inventory")) {
        return { ok: true, status: 200, json: async () => ({ products: [], low_stock_count: 0 }) };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            shop_id: "shop_test",
            currency: "NGN",
            total_customers_with_debt: 0,
            total_outstanding_debt: { amount: 0, currency: "NGN" },
            today_credit_sales_amount: { amount: 0, currency: "NGN" },
            today_repayments_amount: { amount: 0, currency: "NGN" },
          }),
        };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");
    await new Promise((r) => setTimeout(r, 20));

    // Find "Run Audio Test" button in onboarding step 6
    const buttons = Array.from(document.querySelectorAll("button"));
    const testAudioBtn = buttons.find((b) => b.textContent?.includes("Run Audio Test") || b.textContent?.includes("Test Sound"));
    assert.notEqual(testAudioBtn, undefined, "Run Audio Test button must exist in onboarding card");

    // Open test capture modal
    testAudioBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    // Modal elements
    const modal = document.querySelector(".modal");
    assert.notEqual(modal, null, "test capture modal must open");

    // Check tab switchers exist
    const tabBtns = Array.from(document.querySelectorAll(".test-tab-btn"));
    assert.equal(tabBtns.length, 2, "both tabs (Phone Mic and Text Simulator) must exist");

    // Check Live Mic button exists
    const liveMicBtn = document.querySelector(".live-mic-btn");
    assert.notEqual(liveMicBtn, null, "live microphone button must exist in modal");

    cleanup();
  });

  await t.test("7. Screen Wake Lock: acquires lock on start, releases on stop, and re-requests on visibilitychange", async () => {
    let requestCount = 0;
    let released = false;

    class MockWakeLockSentinel {
      constructor() {
        this.released = false;
        this.type = "screen";
      }
      async release() {
        this.released = true;
        released = true;
        this.onrelease?.(new MockEvent("release"));
      }
    }

    navigator.wakeLock = {
      request: async (type) => {
        if (type === "screen") {
          requestCount++;
          return new MockWakeLockSentinel();
        }
        throw new Error("unsupported type");
      },
    };

    assert.equal(isWakeLockSupported(), true);

    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const wakeLockEvents = [];
    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      onWakeLockChange: (active) => {
        wakeLockEvents.push(active);
      },
    });

    await listener.start();
    assert.equal(requestCount, 1);
    assert.equal(listener.isWakeLockActive(), true);
    assert.deepEqual(wakeLockEvents, [true]);

    // Simulate tab visibility change: hide then restore
    document.visibilityState = "hidden";
    document.dispatchEvent(new MockEvent("visibilitychange"));
    document.visibilityState = "visible";
    document.dispatchEvent(new MockEvent("visibilitychange"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(requestCount, 2, "wake lock must be re-requested when returning to tab");

    listener.stop();
    assert.equal(listener.isWakeLockActive(), false);
    assert.equal(released, true);
    assert.equal(wakeLockEvents[wakeLockEvents.length - 1], false);

    delete navigator.wakeLock;
    assert.equal(isWakeLockSupported(), false);
  });

  await t.test("8. Speech Grammar List: compiles catalogue vocabulary boosting grammar", async () => {
    const addedGrammarStrings = [];
    const addedGrammarWeights = [];

    class MockSpeechGrammarList {
      constructor() {
        this.length = 0;
      }
      addFromString(str, weight = 1.0) {
        addedGrammarStrings.push(str);
        addedGrammarWeights.push(weight);
      }
      addFromURI() {}
    }
    window.SpeechGrammarList = MockSpeechGrammarList;
    assert.equal(isSpeechGrammarListSupported(), true);

    let assignedGrammars = null;
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.grammars = null;
      }
      start() {
        assignedGrammars = this.grammars;
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const listener = new CounterSpeechListener({
      catalogueWords: ["Peak Milk 400g", "Milo", "Indomie Onion", "carton", "pack"],
    });

    await listener.start();

    assert.notEqual(assignedGrammars, null);
    assert.equal(addedGrammarStrings.length, 1);
    assert.ok(addedGrammarStrings[0].includes("peak milk 400g"));
    assert.ok(addedGrammarStrings[0].includes("milo"));
    assert.ok(addedGrammarStrings[0].includes("indomie onion"));
    assert.equal(addedGrammarWeights[0], 1.0);

    // Test updating catalogue words dynamically
    listener.setCatalogueWords(["Golden Morn", "Cornflakes"]);
    assert.equal(addedGrammarStrings.length, 2);
    assert.ok(addedGrammarStrings[1].includes("golden morn"));

    listener.stop();
    delete window.SpeechGrammarList;
    assert.equal(isSpeechGrammarListSupported(), false);
  });

  await t.test("9. Speech Synthesis: speakFeedback speaks with brisk counter cadence and handles missing synth gracefully", () => {
    // When no speechSynthesis exists
    delete window.speechSynthesis;
    assert.equal(isSpeechSynthesisSupported(), false);
    assert.equal(speakFeedback("Test message"), false);

    // Mock speechSynthesis
    let spokenText = "";
    let spokenRate = 0;
    let spokenLang = "";
    let cancelled = false;

    window.SpeechSynthesisUtterance = class {
      constructor(text) {
        this.text = text;
        this.rate = 1.0;
        this.lang = "en-US";
      }
    };

    window.speechSynthesis = {
      cancel: () => {
        cancelled = true;
      },
      speak: (utterance) => {
        spokenText = utterance.text;
        spokenRate = utterance.rate;
        spokenLang = utterance.lang;
      },
    };

    assert.equal(isSpeechSynthesisSupported(), true);
    const spoken = speakFeedback("Sale: 2 Milo", { rate: 1.2, lang: "en-NG" });
    assert.equal(spoken, true);
    assert.equal(cancelled, true, "ongoing speech should be cancelled first");
    assert.equal(spokenText, "Sale: 2 Milo");
    assert.equal(spokenRate, 1.2);
    assert.equal(spokenLang, "en-NG");

    delete window.speechSynthesis;
    delete window.SpeechSynthesisUtterance;
  });

  await t.test("10. Voice Readout & Restock Feedback in Home Command Centre: toggles TTS button and emits restock announcements", async () => {
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const spokenUtterances = [];
    window.SpeechSynthesisUtterance = class {
      constructor(text) {
        this.text = text;
      }
    };
    window.speechSynthesis = {
      cancel: () => {},
      speak: (utterance) => {
        spokenUtterances.push(utterance.text);
      },
    };

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 5,
            today_sales_amount: { amount: 2000000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 2000000, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return { ok: true, status: 200, json: async () => ({ transactions: [], total: 0, limit: 20, offset: 0 }) };
      }
      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            products: [
              { id: "prd_milo", name: "Milo 500g", aliases: ["Milo", "Choco"], price: { amount: 200000, currency: "NGN" } },
            ],
            low_stock_count: 0,
          }),
        };
      }
      if (u.includes("/api/onboarding/status")) {
        return { ok: true, status: 200, json: async () => ({ is_complete: true, steps: {} }) };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return { ok: true, status: 200, json: async () => ({ total_outstanding_debt: { amount: 0, currency: "NGN" } }) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");
    await new Promise((r) => setTimeout(r, 20));

    const ttsBtn = document.querySelector("[data-hook='counter-tts-toggle-btn']");
    const ttsIcon = document.querySelector("[data-hook='counter-tts-btn-icon']");
    const ttsLabel = document.querySelector("[data-hook='counter-tts-btn-label']");

    assert.notEqual(ttsBtn, null, "TTS toggle button must be mounted");
    assert.equal(ttsLabel.textContent, "Voice: Off");

    // Click TTS toggle button to turn voice feedback on
    ttsBtn.dispatchEvent(new MockEvent("click"));
    assert.equal(ttsLabel.textContent, "Voice: On");
    assert.equal(ttsIcon.textContent, "🔊");
    assert.ok(spokenUtterances.includes("Voice readout enabled"));

    cleanup();
    delete window.speechSynthesis;
    delete window.SpeechSynthesisUtterance;
  });

  await t.test("11. Ambient VAD Mathematical Algorithms: RMS, noise floor, speech threshold & adaptive silence", () => {
    // 1. RMS Energy
    assert.equal(calculateAudioRMS([]), 0);
    const silence = new Float32Array([0, 0, 0, 0]);
    assert.equal(calculateAudioRMS(silence), 0);
    const constantDC = new Float32Array([0.2, 0.2, 0.2, 0.2]);
    assert.ok(Math.abs(calculateAudioRMS(constantDC) - 0.2) < 0.0001);
    const wave = new Float32Array([0.1, -0.1, 0.1, -0.1]);
    assert.ok(Math.abs(calculateAudioRMS(wave) - 0.1) < 0.0001);

    // 2. Noise Floor Estimation (30th percentile filtering)
    assert.equal(estimateNoiseFloor([]), 0.05);
    const quietStall = [0.03, 0.04, 0.04, 0.05, 0.06, 0.12, 0.25];
    const floorQuiet = estimateNoiseFloor(quietStall);
    assert.ok(floorQuiet <= 0.06 && floorQuiet >= 0.03);

    const noisyMarket = [0.32, 0.35, 0.36, 0.38, 0.40, 0.55, 0.65];
    const floorNoisy = estimateNoiseFloor(noisyMarket);
    assert.ok(floorNoisy >= 0.34 && floorNoisy <= 0.40);

    // Clamping limits
    assert.equal(estimateNoiseFloor([0.001]), 0.02); // min floor clamp
    assert.equal(estimateNoiseFloor([0.95]), 0.60); // max floor clamp

    // 3. Speech Threshold
    const speechQuiet = calculateSpeechThreshold(0.04);
    assert.ok(speechQuiet >= 0.11, "quiet speech threshold should be well above ambient floor");
    const speechNoisy = calculateSpeechThreshold(0.35);
    assert.ok(speechNoisy >= 0.54, "noisy speech threshold should guard against market background");

    // 4. Adaptive Silence Timeout
    // Quiet environment (floor <= 0.06) -> 1200ms
    assert.equal(calculateAdaptiveSilenceTimeout(0.04), 1200);
    assert.equal(calculateAdaptiveSilenceTimeout(0.06), 1200);
    // Noisy market (floor >= 0.40) -> 2500ms
    assert.equal(calculateAdaptiveSilenceTimeout(0.40), 2500);
    assert.equal(calculateAdaptiveSilenceTimeout(0.50), 2500);
    // Moderate noise floor (e.g. 0.23) -> intermediate timeout
    const moderateTimeout = calculateAdaptiveSilenceTimeout(0.23);
    assert.ok(moderateTimeout > 1600 && moderateTimeout < 2100);

    // 5. Environment Classification
    assert.equal(classifyEnvironmentMode(0.05), "quiet");
    assert.equal(classifyEnvironmentMode(0.18), "moderate");
    assert.equal(classifyEnvironmentMode(0.35), "noisy");
  });

  await t.test("12. CounterSpeechListener VAD Engine: Real-time Audio Analyser Calibration, Active Speech Detection & Gating", async () => {
    let frameCallbacks = [];
    const origRAF = globalThis.requestAnimationFrame;
    const origCAF = globalThis.cancelAnimationFrame;
    globalThis.requestAnimationFrame = (cb) => {
      frameCallbacks.push(cb);
      return frameCallbacks.length;
    };
    globalThis.cancelAnimationFrame = (id) => {
      frameCallbacks[id - 1] = null;
    };

    class MockSpeechRecognition {
      constructor() {
        this.continuous = true;
        this.interimResults = true;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {
        this.onend?.(new MockEvent("end"));
      }
      stop() {
        this.onend?.(new MockEvent("end"));
      }
    }
    window.SpeechRecognition = MockSpeechRecognition;

    let mockAnalyser = null;
    class MockAnalyserNode {
      constructor() {
        mockAnalyser = this;
        this.frequencyBinCount = 128;
        this.fftSize = 256;
        this.samples = new Float32Array(256).fill(0.03); // baseline quiet stall
      }
      getFloatTimeDomainData(target) {
        target.set(this.samples);
      }
      getByteFrequencyData(target) {
        target.fill(15);
      }
    }

    class MockAudioContext {
      constructor() {
        this.state = "running";
      }
      createMediaStreamSource() {
        return { connect: () => {} };
      }
      createAnalyser() {
        return new MockAnalyserNode();
      }
      async resume() {}
      async close() {
        this.state = "closed";
      }
    }
    window.AudioContext = MockAudioContext;

    navigator.mediaDevices = {
      getUserMedia: async () => ({
        getTracks: () => [{ stop: () => {} }],
      }),
    };

    const vadUpdates = [];
    const speechActivityEvents = [];
    const audioLevels = [];

    const listener = new CounterSpeechListener({
      salePhrase: "thank you",
      onAudioLevel: (lvl) => audioLevels.push(lvl),
      onSpeechActivityChange: (active) => speechActivityEvents.push(active),
      onVADChange: (m) => vadUpdates.push(m),
    });

    await listener.start();
    assert.equal(listener.isListening(), true);

    const initialMetrics = listener.getVADMetrics();
    assert.equal(initialMetrics.isCalibrating, true);
    assert.equal(listener.isSpeechActive(), false);

    // Run calibration frames (36 frames to satisfy calibration window)
    for (let f = 0; f < 36; f++) {
      const cb = frameCallbacks.shift();
      cb?.();
    }

    const calibratedMetrics = listener.getVADMetrics();
    assert.equal(calibratedMetrics.isCalibrating, false);
    assert.ok(calibratedMetrics.noiseFloor >= 0.02);
    assert.ok(calibratedMetrics.speechThreshold > calibratedMetrics.noiseFloor);
    assert.equal(calibratedMetrics.environmentMode, "quiet");

    // Simulate voice onset: cashier begins speaking ("Two Milo...")
    mockAnalyser.samples.fill(0.25); // high energy above speech threshold
    for (let f = 0; f < 3; f++) {
      const cb = frameCallbacks.shift();
      cb?.();
    }

    assert.equal(listener.isSpeechActive(), true);
    assert.ok(speechActivityEvents.includes(true));
    assert.equal(listener.getVADMetrics().isSpeaking, true);

    // Simulate return to silence (pause between words or finished sentence)
    mockAnalyser.samples.fill(0.02); // quiet pause
    for (let f = 0; f < 12; f++) {
      const cb = frameCallbacks.shift();
      cb?.();
    }

    assert.equal(listener.isSpeechActive(), false);
    assert.equal(speechActivityEvents[speechActivityEvents.length - 1], false);

    // Test calibrateNoiseFloor resets calibration
    listener.calibrateNoiseFloor();
    assert.equal(listener.getVADMetrics().isCalibrating, true);

    listener.stop();
    assert.equal(listener.isSpeechActive(), false);
    assert.equal(listener.getAudioLevel(), 0);

    globalThis.requestAnimationFrame = origRAF;
    globalThis.cancelAnimationFrame = origCAF;
    delete window.AudioContext;
    delete navigator.mediaDevices;
  });

  await t.test("13. Home Overview Counter Dock VAD UI: Displays Noise Floor Marker, Active Waveform & Adaptive Silence Badges", async () => {
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 2,
            today_sales_amount: { amount: 1000000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 1000000, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return { ok: true, status: 200, json: async () => ({ transactions: [], total: 0, limit: 20, offset: 0 }) };
      }
      if (u.includes("/api/inventory")) {
        return { ok: true, status: 200, json: async () => ({ products: [], low_stock_count: 0 }) };
      }
      if (u.includes("/api/onboarding/status")) {
        return { ok: true, status: 200, json: async () => ({ is_complete: true, steps: {} }) };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return { ok: true, status: 200, json: async () => ({ total_outstanding_debt: { amount: 0, currency: "NGN" } }) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");
    await new Promise((r) => setTimeout(r, 20));

    const thresholdMarker = document.querySelector("[data-hook='counter-threshold-marker']");
    const vadBadge = document.querySelector("[data-hook='counter-vad-badge']");
    const waveform = document.querySelector("[data-hook='counter-waveform']");
    const micToggleBtn = document.querySelector("[data-hook='counter-mic-toggle-btn']");

    assert.notEqual(thresholdMarker, null, "VAD noise floor threshold marker must exist");
    assert.notEqual(vadBadge, null, "VAD status badge must exist");
    assert.notEqual(waveform, null, "waveform container must exist");

    // Click start listening
    micToggleBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(vadBadge.textContent, "Calibrating Ambient Noise…");
    assert.ok(vadBadge.className.includes("badge--calibrating"));

    // Click stop listening
    micToggleBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(vadBadge.textContent, "Ambient VAD Idle");

    cleanup();
  });

  await t.test("14. Offline Spoken Capture Queue & Auto-Sync in Home Command Centre", async () => {
    await clearQueue();
    let recognitionInstance = null;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    let isOffline = true;
    let postCaptureCalls = [];
    globalThis.fetch = async (url, init = {}) => {
      const u = String(url);
      if (u.includes("/api/captures")) {
        if (isOffline) {
          throw new TypeError("Failed to fetch"); // Network failure
        }
        postCaptureCalls.push(JSON.parse(String(init.body)));
        return {
          ok: true,
          status: 201,
          json: async () => ({
            capture_id: "cap_synced",
            shop_id: "shop_test",
            type: "sale",
            transactions: [
              {
                id: "txn_001",
                shop_id: "shop_test",
                type: "sale",
                product_name: "Milo 500g",
                quantity: 2,
                unit: "pack",
                amount: { amount: 400000, currency: "NGN" },
                status: "confirmed",
                occurred_at: new Date().toISOString(),
                created_at: new Date().toISOString(),
              },
            ],
            requires_review: false,
          }),
        };
      }
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 1,
            today_sales_amount: { amount: 400000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 400000, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return { ok: true, status: 200, json: async () => ({ transactions: [], total: 0, limit: 20, offset: 0 }) };
      }
      if (u.includes("/api/inventory")) {
        return { ok: true, status: 200, json: async () => ({ products: [], low_stock_count: 0 }) };
      }
      if (u.includes("/api/onboarding/status")) {
        return { ok: true, status: 200, json: async () => ({ is_complete: true, steps: {} }) };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return { ok: true, status: 200, json: async () => ({ total_outstanding_debt: { amount: 0, currency: "NGN" } }) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");
    await new Promise((r) => setTimeout(r, 20));

    const micToggleBtn = document.querySelector("[data-hook='counter-mic-toggle-btn']");
    const offlineBadge = document.querySelector("[data-hook='counter-offline-badge']");
    const toast = document.querySelector("[data-hook='counter-capture-toast']");

    assert.notEqual(offlineBadge, null, "offline badge must be mounted");
    assert.equal(offlineBadge.style.display, "none", "offline badge is initially hidden");

    // Start listening
    micToggleBtn.dispatchEvent(new MockEvent("click"));
    await new Promise((r) => setTimeout(r, 20));

    // Simulate cashier speaking a sale when network is offline
    const offlineSpeechResult = [{ transcript: "Two Milo four thousand naira thank you", confidence: 0.95 }];
    offlineSpeechResult.isFinal = true; // P4.3: boundary detection acts only on finalized results
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [offlineSpeechResult],
    });

    await new Promise((r) => setTimeout(r, 40));

    // Verify capture was saved to offline queue
    assert.equal(await getQueuedCount(), 1);
    assert.equal(offlineBadge.style.display, "inline-flex");
    assert.ok(offlineBadge.textContent.includes("1 Queued Offline"));
    assert.ok(toast.textContent.includes("Offline Mode: Capture saved to device queue"));

    // Now restore network connection and trigger online sync
    isOffline = false;
    window.dispatchEvent(new MockEvent("online"));
    await new Promise((r) => setTimeout(r, 50));

    // Queue should now be empty and postCapture called
    assert.equal(await getQueuedCount(), 0);
    assert.equal(postCaptureCalls.length, 1);
    assert.equal(postCaptureCalls[0].transcript, "Two Milo four thousand naira thank you");
    assert.equal(offlineBadge.style.display, "none");

    cleanup();
    await clearQueue();
  });

  await t.test("16. calculateSNR: computes decibel ratio with clamping and zero safeguards", () => {
    // 20 * log10(0.1 / 0.01) = 20.0 dB
    assert.equal(calculateSNR(0.1, 0.01), 20.0);
    // 20 * log10(0.05 / 0.05) = 0.0 dB
    assert.equal(calculateSNR(0.05, 0.05), 0.0);
    // Severe background noise: signal 0.001, noise floor 0.1 -> ratio 0.01 -> -40 dB -> clamped to -6.0 dB
    assert.equal(calculateSNR(0.001, 0.1), -6.0);
    // Pristine high SNR: signal 0.8, noise floor 0.001 -> ratio 800 -> 58 dB -> clamped to 30.0 dB
    assert.equal(calculateSNR(0.8, 0.001), 30.0);
    // Zero noise floor safeguard: should not throw or return NaN/Infinity
    const zeroFloorSNR = calculateSNR(0.05, 0);
    assert.ok(!Number.isNaN(zeroFloorSNR));
    assert.ok(Number.isFinite(zeroFloorSNR));
    assert.ok(zeroFloorSNR >= -6.0 && zeroFloorSNR <= 30.0);
  });

  await t.test("17. calculateSpectralBands: groups FFT frequency bins into normalized discrete bands", () => {
    // Empty frequency data
    const emptyBands = calculateSpectralBands([], 16);
    assert.equal(emptyBands.length, 16);
    assert.deepEqual(emptyBands, new Array(16).fill(0));

    // Full scale 255 uniform frequency data (32 bins into 16 bands = 2 bins per band)
    const fullBins = new Uint8Array(32).fill(255);
    const fullBands = calculateSpectralBands(fullBins, 16);
    assert.equal(fullBands.length, 16);
    assert.ok(fullBands.every((v) => v === 1.0));

    // Half scale 128 uniform frequency data
    const halfBins = new Uint8Array(32).fill(128);
    const halfBands = calculateSpectralBands(halfBins, 16);
    assert.equal(halfBands.length, 16);
    assert.ok(halfBands.every((v) => Math.abs(v - 0.502) < 0.01));

    // Non-uniform distribution: low frequency energy, high frequency silence
    const shapedBins = new Uint8Array(16);
    shapedBins[0] = 255;
    shapedBins[1] = 255;
    for (let i = 2; i < 16; i++) shapedBins[i] = 0;
    const shapedBands = calculateSpectralBands(shapedBins, 8);
    assert.equal(shapedBands.length, 8);
    assert.equal(shapedBands[0], 1.0);
    assert.equal(shapedBands[1], 0.0);
    assert.equal(shapedBands[7], 0.0);
  });

  await t.test("18. Pilot listener never creates a recorder or attaches raw audio", async () => {
    let recognitionInstance = null;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {
        this.onend?.(new MockEvent("end"));
      }
      stop() {
        this.onend?.(new MockEvent("end"));
      }
    }
    window.SpeechRecognition = MockSpeechRecognition;

    let mockAnalyser = null;
    class MockAnalyserNode {
      constructor() {
        mockAnalyser = this;
        this.frequencyBinCount = 128;
        this.fftSize = 256;
        this.samples = new Float32Array(256).fill(0.03);
      }
      getFloatTimeDomainData(target) {
        target.set(this.samples);
      }
      getByteFrequencyData(target) {
        target.fill(15);
      }
    }

    class MockAudioContext {
      constructor() {
        this.state = "running";
      }
      createMediaStreamSource() {
        return { connect: () => {} };
      }
      createAnalyser() {
        return new MockAnalyserNode();
      }
      async resume() {}
      async close() {
        this.state = "closed";
      }
    }
    window.AudioContext = MockAudioContext;

    navigator.mediaDevices = {
      getUserMedia: async () => ({
        getTracks: () => [{ stop: () => {} }],
      }),
    };

    let mediaRecorderInstance = null;
    class MockMediaRecorder {
      static isTypeSupported(type) {
        return true;
      }
      constructor(stream, options) {
        mediaRecorderInstance = this;
        this.stream = stream;
        this.mimeType = options?.mimeType || "audio/webm";
        this.state = "inactive";
        this.ondataavailable = null;
      }
      start(timeslice) {
        this.state = "recording";
      }
      stop() {
        this.state = "inactive";
      }
    }
    window.MediaRecorder = MockMediaRecorder;
    globalThis.MediaRecorder = MockMediaRecorder;

    class MockFileReader {
      readAsDataURL(blob) {
        setTimeout(() => {
          this.result = "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=";
          this.onloadend?.();
        }, 5);
      }
    }
    window.FileReader = MockFileReader;
    globalThis.FileReader = MockFileReader;

    let receivedSpectralBands = null;
    let capturedCandidate = null;

    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      onSpectralBands: (bands) => {
        receivedSpectralBands = bands;
      },
      onBoundaryDetected: (cand) => {
        capturedCandidate = cand;
      },
    });

    await listener.start();
    assert.equal(listener.isListening(), true);

    // Even when recording is supported, the pilot must never create a recorder.
    assert.equal(mediaRecorderInstance, null);
    assert.equal(await listener.exportRecentAudioSnippet(), undefined);

    // Simulate recognition boundary event
    const pilotSpeechResult = [{ transcript: "One peak milk five hundred naira thank you", confidence: 0.95 }];
    pilotSpeechResult.isFinal = true; // P4.3: boundary detection acts only on finalized results
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [pilotSpeechResult],
    });

    // Await async snippet attachment
    await new Promise((r) => setTimeout(r, 30));

    assert.notEqual(capturedCandidate, null);
    assert.equal(capturedCandidate.type, "sale");
    assert.equal(capturedCandidate.audioData, undefined);

    // Check VAD metrics SNR
    const vadMetrics = listener.getVADMetrics();
    assert.ok(typeof vadMetrics.snrDb === "number");
    assert.ok(vadMetrics.snrDb >= -6.0 && vadMetrics.snrDb <= 30.0);

    listener.stop();
    delete window.MediaRecorder;
    delete globalThis.MediaRecorder;
    delete window.FileReader;
    delete globalThis.FileReader;
    delete window.AudioContext;
    delete navigator.mediaDevices;
  });

  await t.test("19. Home Overview: renders spectral visualizer bars and SNR diagnostic badge", async () => {
    class MockSpeechRecognition {
      constructor() {
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    globalThis.fetch = async (url) => {
      const u = String(url);
      if (u.includes("/api/summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            today_sales_count: 5,
            today_sales_amount: { amount: 2000000, currency: "NGN" },
            pending_reviews: 0,
            low_stock_count: 0,
            active_devices_count: 1,
            today_expenses_count: 0,
            today_expenses_amount: { amount: 0, currency: "NGN" },
            today_net_profit: { amount: 2000000, currency: "NGN" },
          }),
        };
      }
      if (u.includes("/api/transactions")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ transactions: [], total: 0 }),
        };
      }
      if (u.includes("/api/inventory")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ products: [], low_stock_count: 0 }),
        };
      }
      if (u.includes("/api/onboarding/status")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ is_complete: true, steps: {} }),
        };
      }
      if (u.includes("/api/devices")) {
        return { ok: true, status: 200, json: async () => ({ devices: [] }) };
      }
      if (u.includes("/api/customers/credit-summary")) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            total_customers_with_debt: 0,
            total_outstanding_debt: { amount: 0, currency: "NGN" },
          }),
        };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    document.body.innerHTML = HOME_HTML;
    const cleanup = mountHome(document.body, "shop_test");
    await new Promise((r) => setTimeout(r, 20));

    const spectralContainer = document.querySelector("[data-hook='counter-spectral-bars']");
    const snrBadge = document.querySelector("[data-hook='counter-snr-badge']");

    assert.notEqual(spectralContainer, null, "spectral bars container must exist");
    assert.notEqual(snrBadge, null, "snr badge must exist");

    const bars = spectralContainer.querySelectorAll(".spectral-bar");
    assert.equal(bars.length, 16, "must mount 16 spectral bars");

    // Check SNR badge initial state
    assert.equal(snrBadge.textContent, "SNR: Calibrating…");

    cleanup();
  });

  await t.test("20. P4.7 restart backoff: escalates then stops retrying after repeated immediate failures", async () => {
    const origSetTimeout = globalThis.setTimeout;
    let recognitionInstance = null;
    let startCount = 0;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = true;
        this.interimResults = true;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        startCount++;
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    // Record the requested restart delays and run them immediately so the
    // backoff sequence can be exercised without real waits.
    const delays = [];
    globalThis.setTimeout = (cb, ms, ...args) => {
      delays.push(ms);
      return origSetTimeout(cb, 0, ...args);
    };

    const errors = [];
    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      onError: (e) => errors.push(e),
    });

    try {
      await listener.start();
      // Each session ends ~immediately, so every restart counts as a failure.
      for (let i = 0; i < 6; i++) {
        recognitionInstance.onend();
        await new Promise((r) => origSetTimeout(r, 10));
      }

      // 150ms doubling to 2400ms, then it gives up (no sixth timer).
      assert.deepEqual(delays, [150, 300, 600, 1200, 2400]);
      assert.equal(listener.getState(), "error");
      assert.equal(errors.length, 1, "onError fires once when retrying gives up");
      assert.equal(startCount, 6, "initial start plus five bounded restarts");
    } finally {
      listener.stop();
      globalThis.setTimeout = origSetTimeout;
      delete window.SpeechRecognition;
    }
  });

  await t.test("21. P4.7 stop() cancels a pending restart", async () => {
    let recognitionInstance = null;
    let startCount = 0;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = true;
        this.interimResults = true;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        startCount++;
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const listener = new CounterSpeechListener({ salePhrase: "Thank you" });
    await listener.start();
    const startsAfterStart = startCount;

    recognitionInstance.onend(); // schedules a restart in 150ms
    listener.stop(); // must clear the pending timer
    await new Promise((r) => setTimeout(r, 220));

    assert.equal(startCount, startsAfterStart, "no restart fires after stop()");
    delete window.SpeechRecognition;
  });

  await t.test("22. P4.1 assembleCaptureTranscript & utteranceHasAnchors: recover anchors across a payment pause", () => {
    // Anchor detection: quantity/amount words and digits anchor; closing phrases do not.
    assert.equal(utteranceHasAnchors("Two Milo, four thousand naira"), true);
    assert.equal(utteranceHasAnchors("seven thousand"), true);
    assert.equal(utteranceHasAnchors("₦5,000"), true);
    assert.equal(utteranceHasAnchors("Thank you"), false);
    assert.equal(utteranceHasAnchors(""), false);

    // Closing utterance already carries anchors: used verbatim.
    assert.equal(
      assembleCaptureTranscript([], "Two Milo four thousand naira Thank you", 1_000),
      "Two Milo four thousand naira Thank you",
    );

    // Standalone closing phrase recovers the most recent anchor utterance.
    assert.equal(
      assembleCaptureTranscript(
        [{ text: "Two Milo, four thousand naira", at: 1_000 }],
        "Thank you",
        2_000,
      ),
      "Two Milo, four thousand naira Thank you",
    );

    // Does not duplicate a closing phrase already at the end of the buffered text.
    assert.equal(
      assembleCaptureTranscript(
        [{ text: "Two Milo four thousand naira Thank you", at: 1_000 }],
        "Thank you",
        2_000,
      ),
      "Two Milo four thousand naira Thank you",
    );

    // Anchors older than the look-back window are ignored; only the closing phrase remains.
    assert.equal(
      assembleCaptureTranscript(
        [{ text: "Two Milo, four thousand naira", at: 1_000 }],
        "Thank you",
        1_000 + DEFAULT_ANCHOR_LOOKBACK_MS + 1,
      ),
      "Thank you",
    );

    // No anchors anywhere: closing phrase returned unchanged.
    assert.equal(
      assembleCaptureTranscript([{ text: "Good morning", at: 1_000 }], "Thank you", 2_000),
      "Thank you",
    );
  });

  await t.test("23. P4.1 CounterSpeechListener: a pause before the closing phrase does not lose the sale", async () => {
    let recognitionInstance = null;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = false;
        this.interimResults = false;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const finalResult = (transcript) => {
      const r = [{ transcript, confidence: 0.9 }];
      r.isFinal = true;
      return r;
    };

    const candidates = [];
    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      restockPhrase: "Stock received",
      onBoundaryDetected: (cand) => candidates.push(cand),
    });

    await listener.start();

    // Session 1: the cashier states the anchors, no closing phrase yet.
    recognitionInstance.onresult({ resultIndex: 0, results: [finalResult("Two Milo, four thousand naira ")] });
    assert.equal(candidates.length, 0, "no capture until a closing phrase arrives");

    // The session ends (pause while the customer pays) and recognition restarts.
    recognitionInstance.onend?.();
    await new Promise((r) => setTimeout(r, 200));

    // Session 2: a standalone closing phrase with no anchors.
    recognitionInstance.onresult({ resultIndex: 0, results: [finalResult("Thank you")] });
    await new Promise((r) => setTimeout(r, 20));

    assert.equal(candidates.length, 1, "exactly one sale is recorded");
    assert.equal(candidates[0].type, "sale");
    assert.equal(candidates[0].closingPhrase, "Thank you");
    assert.equal(candidates[0].transcript, "Two Milo, four thousand naira Thank you");

    // The recovered anchors are consumed, so a later standalone closing phrase
    // does not re-merge them. It is emitted on its own (suppressing anchorless
    // captures is P4.2, tracked separately). The second sale starts a new
    // recognition session, which is what resets the P4.3 replay guard for the
    // same result position (done deterministically instead of waiting out the
    // escalating P4.7 restart delay).
    recognitionInstance.onend?.();
    await new Promise((r) => setTimeout(r, 10));
    listener.stop();
    await listener.start();
    await new Promise((r) => setTimeout(r, 20));
    recognitionInstance.onresult({ resultIndex: 0, results: [finalResult("Thank you")] });
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(candidates.length, 2, "a later closing phrase is a separate capture");
    assert.equal(candidates[1].transcript, "Thank you");

    listener.stop();
  });

  await t.test("24. P4.3 interim results never capture; a final result captures once and a replayed final is ignored", async () => {
    let recognitionInstance = null;
    class MockSpeechRecognition {
      constructor() {
        recognitionInstance = this;
        this.continuous = true;
        this.interimResults = true;
        this.lang = "en-US";
        this.onstart = null;
        this.onresult = null;
        this.onerror = null;
        this.onend = null;
      }
      start() {
        this.onstart?.(new MockEvent("start"));
      }
      abort() {}
      stop() {}
    }
    window.SpeechRecognition = MockSpeechRecognition;

    const result = (transcript, isFinal) => {
      const entry = [{ transcript, confidence: 0.9 }];
      entry.isFinal = isFinal;
      return entry;
    };

    const candidates = [];
    const listener = new CounterSpeechListener({
      salePhrase: "Thank you",
      onBoundaryDetected: (cand) => candidates.push(cand),
    });

    await listener.start();

    // Chrome emits an interim result that already ends in the closing phrase.
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [result("Two Milo, four thousand naira Thank you", false)],
    });
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(candidates.length, 0, "an interim result must not record a sale");

    // The finalized result records exactly one sale with a browser capture id.
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [result("Two Milo, four thousand naira Thank you", true)],
    });
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(candidates.length, 1, "the final result records the sale");
    assert.equal(candidates[0].transcript, "Two Milo, four thousand naira Thank you");
    assert.match(candidates[0].captureId, /^cap_/);

    // Replaying the same finalized result (same position, same results length)
    // must not record it again.
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [result("Two Milo, four thousand naira Thank you", true)],
    });
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(candidates.length, 1, "a replayed final result is ignored");

    // A genuinely new finalized result can record a separate sale. Start a new
    // recognition session deterministically (the escalating P4.7 restart delay
    // makes waiting for it flaky); onstart resets the replay guard.
    recognitionInstance.onend?.();
    await new Promise((r) => setTimeout(r, 10));
    listener.stop();
    await listener.start();
    await new Promise((r) => setTimeout(r, 20));
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [result("Thank you", true)],
    });
    await new Promise((r) => setTimeout(r, 10));
    assert.equal(candidates.length, 2, "a separate sale is recorded");
    assert.notEqual(candidates[1].captureId, candidates[0].captureId, "each capture gets its own id");

    listener.stop();
  });
});

