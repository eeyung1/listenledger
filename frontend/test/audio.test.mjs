import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { setupDOMEnvironment, MockEvent } from "./test_dom.mjs";
import {
  detectClosingBoundary,
  isSpeechRecognitionSupported,
  isAudioCaptureSupported,
  CounterSpeechListener,
  playAudioFeedback,
} from "../dist/audio.js";
import { postCapture } from "../dist/api.js";
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
    });

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
    recognitionInstance.onresult({
      resultIndex: 0,
      results: [
        [
          {
            transcript: "Two Peak Milk, seven thousand naira. Thank you.",
            confidence: 0.92,
          },
        ],
      ],
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
});
