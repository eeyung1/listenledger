/**
 * Lightweight browser DOM simulator for frontend unit & acceptance tests.
 * Provides DOM nodes, events, forms, selects, and attribute manipulation
 * without requiring bulky external dependencies.
 */

class DOMTokenList {
  constructor(element) {
    this._element = element;
    this._tokens = new Set();
    this._syncFromClassName();
  }

  _syncFromClassName() {
    this._tokens.clear();
    const str = this._element.className || "";
    for (const part of str.split(/\s+/)) {
      if (part) this._tokens.add(part);
    }
  }

  _syncToClassName() {
    this._element.className = Array.from(this._tokens).join(" ");
  }

  add(...tokens) {
    for (const t of tokens) {
      if (t) this._tokens.add(t);
    }
    this._syncToClassName();
  }

  remove(...tokens) {
    for (const t of tokens) {
      this._tokens.delete(t);
    }
    this._syncToClassName();
  }

  toggle(token, force) {
    let result;
    if (force !== undefined) {
      if (force) {
        this._tokens.add(token);
        result = true;
      } else {
        this._tokens.delete(token);
        result = false;
      }
    } else {
      if (this._tokens.has(token)) {
        this._tokens.delete(token);
        result = false;
      } else {
        this._tokens.add(token);
        result = true;
      }
    }
    this._syncToClassName();
    return result;
  }

  contains(token) {
    return this._tokens.has(token);
  }
}

export class MockEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.bubbles = init.bubbles ?? false;
    this.cancelable = init.cancelable ?? false;
    this.defaultPrevented = false;
    this.target = null;
    this.currentTarget = null;
  }

  preventDefault() {
    this.defaultPrevented = true;
  }

  stopPropagation() {}
}

export class MockCustomEvent extends MockEvent {
  constructor(type, init = {}) {
    super(type, init);
    this.detail = init.detail ?? null;
  }
}

export class MockNode {
  constructor() {
    this.parentNode = null;
    this.childNodes = [];
  }

  get children() {
    return this.childNodes.filter((n) => n instanceof MockElement);
  }

  append(...nodes) {
    for (let node of nodes) {
      if (typeof node === "string") {
        node = new MockText(node);
      }
      if (node.parentNode) {
        node.parentNode.removeChild(node);
      }
      node.parentNode = this;
      this.childNodes.push(node);
    }
  }

  prepend(...nodes) {
    for (let i = nodes.length - 1; i >= 0; i--) {
      let node = nodes[i];
      if (typeof node === "string") {
        node = new MockText(node);
      }
      if (node.parentNode) {
        node.parentNode.removeChild(node);
      }
      node.parentNode = this;
      this.childNodes.unshift(node);
    }
  }

  removeChild(child) {
    const idx = this.childNodes.indexOf(child);
    if (idx !== -1) {
      this.childNodes.splice(idx, 1);
      child.parentNode = null;
    }
    return child;
  }

  remove() {
    if (this.parentNode) {
      this.parentNode.removeChild(this);
    }
  }

  replaceChildren(...nodes) {
    for (const c of [...this.childNodes]) {
      c.parentNode = null;
    }
    this.childNodes = [];
    this.append(...nodes);
  }

  get textContent() {
    return this.childNodes.map((n) => n.textContent).join("");
  }

  set textContent(text) {
    this.replaceChildren(new MockText(String(text)));
  }
}

export class MockText extends MockNode {
  constructor(text = "") {
    super();
    this._text = String(text);
  }

  get textContent() {
    return this._text;
  }

  set textContent(text) {
    this._text = String(text);
  }
}

export class MockElement extends MockNode {
  constructor(tagName) {
    super();
    this.tagName = tagName.toUpperCase();
    this._attributes = new Map();
    this._listeners = new Map();
    this._className = "";
    this.classList = new DOMTokenList(this);
    this.style = {};
    this.dataset = {};
    this.title = "";
    this.hidden = false;
    this.disabled = false;
    this.type = "";
    this.value = "";
    this.required = false;
    this.checked = false;
    this.placeholder = "";
  }

  get className() {
    return this._className;
  }

  set className(val) {
    this._className = val || "";
    if (this.classList) {
      this.classList._syncFromClassName();
    }
  }

  getAttribute(name) {
    if (name === "class") return this._className;
    if (name === "hidden") return this.hidden ? "" : null;
    if (name.startsWith("data-")) {
      const key = name.slice(5);
      return this.dataset[key] !== undefined ? this.dataset[key] : (this._attributes.get(name) ?? null);
    }
    if (this[name] !== undefined && this[name] !== "" && typeof this[name] === "string") {
      return this[name];
    }
    return this._attributes.get(name) ?? null;
  }

  setAttribute(name, value) {
    const strVal = String(value);
    this._attributes.set(name, strVal);
    if (name === "class") {
      this.className = strVal;
    } else if (name === "hidden") {
      this.hidden = true;
    } else if (name.startsWith("data-")) {
      const key = name.slice(5);
      this.dataset[key] = strVal;
    }
  }

  removeAttribute(name) {
    this._attributes.delete(name);
    if (name === "class") {
      this.className = "";
    } else if (name === "hidden") {
      this.hidden = false;
    } else if (name.startsWith("data-")) {
      delete this.dataset[name.slice(5)];
    }
  }

  hasAttribute(name) {
    return this._attributes.has(name);
  }

  addEventListener(type, listener) {
    if (!this._listeners.has(type)) {
      this._listeners.set(type, []);
    }
    this._listeners.get(type).push(listener);
  }

  removeEventListener(type, listener) {
    const list = this._listeners.get(type);
    if (!list) return;
    const idx = list.indexOf(listener);
    if (idx !== -1) list.splice(idx, 1);
  }

  dispatchEvent(event) {
    event.target = this;
    event.currentTarget = this;
    const list = this._listeners.get(event.type) || [];
    for (const l of [...list]) {
      if (typeof l === "function") {
        l.call(this, event);
      } else if (l && typeof l.handleEvent === "function") {
        l.handleEvent(event);
      }
    }
    return !event.defaultPrevented;
  }

  get innerHTML() {
    return this.childNodes.map((n) => (n instanceof MockElement ? `<${n.tagName.toLowerCase()}>${n.innerHTML}</${n.tagName.toLowerCase()}>` : n.textContent)).join("");
  }

  set innerHTML(html) {
    this.replaceChildren();
    if (html) {
      parseHTMLFragment(html, this);
    }
  }

  querySelector(selector) {
    const all = this.querySelectorAll(selector);
    return all.length > 0 ? all[0] : null;
  }

  querySelectorAll(selector) {
    const results = [];
    const parts = splitSelector(selector.trim());
    if (parts.length === 1) {
      this._findMatching(this, parts[0], results);
    } else if (parts.length === 2) {
      const intermediates = [];
      this._findMatching(this, parts[0], intermediates);
      for (const inter of intermediates) {
        inter._findMatching(inter, parts[1], results);
      }
    }
    return results;
  }

  _findMatching(root, part, results) {
    for (const child of root.children) {
      if (matchesSimpleSelector(child, part)) {
        results.push(child);
      }
      child._findMatching(child, part, results);
    }
  }
}

export class MockSelectElement extends MockElement {
  constructor() {
    super("select");
    if (!this.options) this.options = [];
  }

  append(...nodes) {
    super.append(...nodes);
    this._syncOptions();
  }

  _syncOptions() {
    this.options = [];
    for (const child of this.children) {
      if (child.tagName === "OPTION") {
        this.options.push(child);
      }
    }
  }

  get value() {
    if (!this.options) return "";
    const selected = this.options.find((o) => o.selected);
    if (selected) return selected.value;
    return this.options[0]?.value ?? "";
  }

  set value(val) {
    if (!this.options) this.options = [];
    for (const o of this.options) {
      o.selected = o.value === String(val);
    }
  }
}

export class MockOptionElement extends MockElement {
  constructor(text = "", value = "") {
    super("option");
    this.text = String(text);
    this.value = String(value ?? text);
    this.textContent = this.text;
    this.selected = false;
  }
}

function splitSelector(selector) {
  const parts = [];
  let current = "";
  let inQuotes = false;
  let quoteChar = "";
  for (let i = 0; i < selector.length; i++) {
    const char = selector[i];
    if ((char === '"' || char === "'") && (!inQuotes || quoteChar === char)) {
      inQuotes = !inQuotes;
      quoteChar = inQuotes ? char : "";
      current += char;
    } else if (/\s/.test(char) && !inQuotes) {
      if (current) {
        parts.push(current);
        current = "";
      }
    } else {
      current += char;
    }
  }
  if (current) parts.push(current);
  return parts;
}

function matchesSimpleSelector(element, selector) {
  const attrBracket = selector.indexOf("[");
  if (attrBracket > 0 && selector.endsWith("]")) {
    const tagPart = selector.slice(0, attrBracket);
    if (element.tagName.toLowerCase() !== tagPart.toLowerCase()) {
      return false;
    }
    selector = selector.slice(attrBracket);
  }

  if (selector.startsWith("#")) {
    return element.getAttribute("id") === selector.slice(1);
  }
  if (selector.startsWith(".")) {
    return element.classList.contains(selector.slice(1));
  }
  if (selector.startsWith("[") && selector.endsWith("]")) {
    const inner = selector.slice(1, -1);
    if (inner.includes("*=")) {
      let [name, val] = inner.split("*=");
      val = val.replace(/^["']|["']$/g, "");
      const attr = element.getAttribute(name);
      return typeof attr === "string" && attr.includes(val);
    }
    if (inner.includes("=")) {
      let [name, val] = inner.split("=");
      val = val.replace(/^["']|["']$/g, "");
      return element.getAttribute(name) === val;
    }
    return element.hasAttribute(inner);
  }
  return element.tagName.toLowerCase() === selector.toLowerCase();
}

/**
 * Parses basic HTML template strings into MockElement hierarchy.
 */
export function parseHTMLFragment(html, rootElement) {
  const tagRegex = /<(\/)?([a-zA-Z0-9-]+)([^>]*)>|([^<]+)/g;
  let match;
  const stack = [rootElement];

  while ((match = tagRegex.exec(html)) !== null) {
    const isClosing = match[1] === "/";
    const tagName = match[2];
    const attrsStr = match[3];
    const textContent = match[4];

    if (textContent) {
      const trimmed = textContent;
      if (trimmed.length > 0) {
        stack[stack.length - 1].append(new MockText(trimmed));
      }
      continue;
    }

    if (isClosing) {
      if (stack.length > 1 && stack[stack.length - 1].tagName.toLowerCase() === tagName.toLowerCase()) {
        stack.pop();
      }
    } else {
      let elem;
      if (tagName.toLowerCase() === "select") {
        elem = new MockSelectElement();
      } else if (tagName.toLowerCase() === "option") {
        elem = new MockOptionElement();
      } else {
        elem = new MockElement(tagName);
      }

      // Parse attributes
      if (attrsStr) {
        const attrRegex = /([a-zA-Z0-9-]+)(?:=["']([^"']*)["'])?/g;
        let attrMatch;
        while ((attrMatch = attrRegex.exec(attrsStr)) !== null) {
          const attrName = attrMatch[1];
          const attrVal = attrMatch[2] !== undefined ? attrMatch[2] : "";
          elem.setAttribute(attrName, attrVal);
          if (attrName === "id") elem.id = attrVal;
          if (attrName === "type") elem.type = attrVal;
          if (attrName === "value") elem.value = attrVal;
          if (attrName === "disabled") elem.disabled = true;
          if (attrName === "required") elem.required = true;
        }
      }

      stack[stack.length - 1].append(elem);

      const isVoid = /^(input|img|br|hr|meta|link)$/i.test(tagName) || attrsStr.endsWith("/");
      if (!isVoid) {
        stack.push(elem);
      }
    }
  }
}

export function setupDOMEnvironment() {
  const docListeners = new Map();
  const document = {
    createElement(tag) {
      const lower = tag.toLowerCase();
      if (lower === "select") return new MockSelectElement();
      if (lower === "option") return new MockOptionElement();
      return new MockElement(tag);
    },
    createTextNode(text) {
      return new MockText(text);
    },
    getElementById(id) {
      return document.body.querySelector(`#${id}`);
    },
    querySelector(sel) {
      return document.body.querySelector(sel);
    },
    querySelectorAll(sel) {
      return document.body.querySelectorAll(sel);
    },
    body: new MockElement("body"),
    visibilityState: "visible",
    addEventListener(type, listener) {
      if (!docListeners.has(type)) docListeners.set(type, new Set());
      docListeners.get(type).add(listener);
    },
    removeEventListener(type, listener) {
      docListeners.get(type)?.delete(listener);
    },
    dispatchEvent(event) {
      const set = docListeners.get(event.type);
      if (set) {
        for (const l of Array.from(set)) l.call(document, event);
      }
      return !event.defaultPrevented;
    },
  };

  const winListeners = new Map();
  const window = {
    document,
    prompt: () => null,
    alert: () => {},
    confirm: () => true,
    location: {
      origin: "http://localhost:8080",
      href: "http://localhost:8080/#/home",
      hash: "#/home",
    },
    addEventListener(type, listener) {
      if (!winListeners.has(type)) winListeners.set(type, new Set());
      winListeners.get(type).add(listener);
    },
    removeEventListener(type, listener) {
      winListeners.get(type)?.delete(listener);
    },
    dispatchEvent(event) {
      const set = winListeners.get(event.type);
      if (set) {
        for (const l of Array.from(set)) l.call(window, event);
      }
      return !event.defaultPrevented;
    },
  };

  try {
    if (typeof navigator !== "undefined") {
      Object.defineProperty(navigator, "onLine", { value: true, configurable: true, writable: true });
    }
  } catch {
    // ignore
  }

  globalThis.document = document;
  globalThis.window = window;
  globalThis.prompt = (...args) => window.prompt(...args);
  globalThis.alert = (...args) => window.alert(...args);
  globalThis.confirm = (...args) => window.confirm(...args);
  globalThis.Event = MockEvent;
  globalThis.CustomEvent = MockCustomEvent;
  globalThis.HTMLElement = MockElement;
  globalThis.HTMLSelectElement = MockSelectElement;
  globalThis.HTMLInputElement = MockElement;
  globalThis.HTMLButtonElement = MockElement;
  globalThis.HTMLOptionElement = MockOptionElement;
  globalThis.HTMLTableCellElement = MockElement;
  globalThis.HTMLTableRowElement = MockElement;

  globalThis.Option = class Option extends MockOptionElement {
    constructor(text = "", value = "") {
      super(text, value);
    }
  };

  return { document, window };
}
