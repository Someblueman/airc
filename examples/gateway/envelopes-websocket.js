import { encryptEnvelope, signEnvelope } from "./envelopes-fetch.js";

const utf8 = new TextEncoder();
const MAX_REQUEST_BYTES = 16 * 1024;
const MAX_RESPONSE_BYTES = 64 * 1024;
const MAX_PENDING = 4;
const MAX_STREAM_BYTES = 1024 * 1024;
const MAX_STREAM_PAGES = 128;
// WebSocket subprotocols are RFC 6455 tokens; the TCP/TLS ALPN retains its
// separate `airc-gateway/1` spelling.
const WEBSOCKET_SUBPROTOCOL = "airc-gateway.v1";
const correlationPattern = /^[A-Za-z0-9_-]{1,64}$/;

function byteLength(value) {
  return utf8.encode(value).byteLength;
}

function protocolError(message) {
  const error = new Error(message);
  error.code = "protocol_error";
  return error;
}

function failureError(failure) {
  const error = new Error(`gateway ${failure.code || "request failed"}`);
  error.code = failure.code || "request_failed";
  error.transient = failure.transient === true;
  error.upstreamCode = failure.upstream_code;
  return error;
}

function correlationID() {
  const random = crypto.randomUUID().replace(/-/g, "");
  return `ws-${random}`.slice(0, 64);
}

function validateResponse(value) {
  if (!value || value.version !== 1 || typeof value.correlation_id !== "string" ||
      !correlationPattern.test(value.correlation_id) ||
      !["result", "page", "end", "error"].includes(value.event)) {
    throw protocolError("invalid WebSocket response");
  }
  const fields = ["result", "page", "reason", "error"];
  const expected = value.event === "result" ? "result" : value.event === "page" ? "page" : value.event === "end" ? "reason" : "error";
  for (const field of fields) {
    if (field !== expected && Object.prototype.hasOwnProperty.call(value, field)) throw protocolError("unexpected response field");
  }
  if (value.event === "result" && (!value.result || typeof value.result !== "object")) throw protocolError("missing result");
  if (value.event === "page" && (!value.page || typeof value.page !== "object" || !Array.isArray(value.page.messages) || typeof value.page.cursor !== "string")) throw protocolError("invalid page");
  if (value.event === "end" && !["resume", "lease_expired", "capacity", "upstream_unavailable"].includes(value.reason)) throw protocolError("invalid stream end");
  if (value.event === "error" && (!value.error || typeof value.error !== "object" || typeof value.error.code !== "string")) throw protocolError("invalid failure");
  return value;
}

// A single bounded, multiplexed WebSocket. Closing it is the cancellation
// primitive: the gateway then releases every active read lease on disconnect.
export async function openEnvelopeSocket(url, { signal } = {}) {
  if (typeof url !== "string" || !url || signal?.aborted) throw new Error("invalid WebSocket connection");
  const socket = new WebSocket(url, WEBSOCKET_SUBPROTOCOL);
  socket.binaryType = "arraybuffer";
  const pending = new Map();
  let opened = false;
  let closing = false;
  let resolveClosed;
  let rejectOpened;
  let resolveOpened;
  const closed = new Promise(resolve => { resolveClosed = resolve; });
  const openedPromise = new Promise((resolve, reject) => { resolveOpened = resolve; rejectOpened = reject; });
  const openTimer = setTimeout(() => terminate(new Error("WebSocket open deadline exceeded")), 5000);

  const cleanupEntry = entry => {
    clearTimeout(entry.timer);
    if (entry.signal && entry.cancel) entry.signal.removeEventListener("abort", entry.cancel);
  };

  const rejectPending = error => {
    for (const entry of pending.values()) {
      cleanupEntry(entry);
      if (entry.kind === "watch") {
        entry.done = true;
        entry.error = error;
        if (entry.waiter) { const waiter = entry.waiter; entry.waiter = null; waiter(Promise.reject(error)); }
      } else entry.reject(error);
    }
    pending.clear();
  };
  const terminate = error => {
    if (error) {
      rejectOpened(error);
      rejectPending(error);
    }
    if (!closing) {
      closing = true;
      try { socket.close(4000, "client error"); } catch { /* already closed */ }
    }
  };
  const abort = () => terminate(new DOMException("The operation was aborted", "AbortError"));
  if (signal) signal.addEventListener("abort", abort, { once: true });

  socket.onopen = () => {
    clearTimeout(openTimer);
    opened = true;
    resolveOpened();
  };
  socket.onerror = () => {
    clearTimeout(openTimer);
    const error = new Error("WebSocket transport failed");
    rejectOpened(error);
    rejectPending(error);
  };
  socket.onclose = () => {
    clearTimeout(openTimer);
    const error = new Error("WebSocket closed");
    if (!opened) rejectOpened(error);
    rejectPending(error);
    if (signal) signal.removeEventListener("abort", abort);
    resolveClosed();
  };
  socket.onmessage = event => {
    if (typeof event.data !== "string" || byteLength(event.data) > MAX_RESPONSE_BYTES) {
      terminate(protocolError("oversized or non-text WebSocket response"));
      return;
    }
    let response;
    try { response = validateResponse(JSON.parse(event.data)); } catch (error) { terminate(error); return; }
    const entry = pending.get(response.correlation_id);
    if (!entry) { terminate(protocolError("uncorrelated WebSocket response")); return; }
    if (response.event === "result") {
      if (entry.kind !== "call") { terminate(protocolError("unexpected result event")); return; }
      pending.delete(response.correlation_id);
      cleanupEntry(entry);
      entry.resolve(response.result);
      return;
    }
    if (response.event === "error") {
      pending.delete(response.correlation_id);
      cleanupEntry(entry);
      const error = failureError(response.error);
      if (entry.kind === "call") { entry.reject(error); return; }
      if (entry.waiter) { const waiter = entry.waiter; entry.waiter = null; waiter(Promise.reject(error)); }
      entry.error = error;
      entry.done = true;
      return;
    }
    if (entry.kind !== "watch") { terminate(protocolError("unexpected stream event")); return; }
    if (response.event === "page") {
      const size = byteLength(event.data);
      if (++entry.pages > MAX_STREAM_PAGES || (entry.bytes += size) > MAX_STREAM_BYTES) { terminate(protocolError("WebSocket stream exceeds bound")); return; }
      if (entry.waiter) { const waiter = entry.waiter; entry.waiter = null; waiter({ done: false, value: { event: "page", value: response.page } }); }
      else {
        if (entry.queue.length >= MAX_STREAM_PAGES) { terminate(protocolError("WebSocket stream queue is full")); return; }
        entry.queue.push({ event: "page", value: response.page });
      }
      return;
    }
    pending.delete(response.correlation_id);
    cleanupEntry(entry);
    const end = { event: "end", value: { reason: response.reason } };
    if (entry.waiter) { const waiter = entry.waiter; entry.waiter = null; waiter({ done: false, value: end }); }
    else entry.terminal = end;
    entry.done = true;
  };

  await openedPromise;
  const send = (profile, envelope, stream, callSignal) => {
    if (closing || socket.readyState !== WebSocket.OPEN) throw new Error("WebSocket is closed");
    if (callSignal?.aborted) {
      const error = new DOMException("The operation was aborted", "AbortError");
      terminate(error);
      throw error;
    }
    if (profile !== "signed" && profile !== "encrypted" || typeof envelope !== "string") throw new Error("invalid WebSocket request");
    if (byteLength(envelope) > (profile === "encrypted" ? 65536 : 32768)) throw new Error("envelope exceeds bound");
    if (pending.size >= MAX_PENDING) throw new Error("too many pending WebSocket requests");
    const id = correlationID();
    const request = { version: 1, correlation_id: id, profile, envelope };
    if (stream) request.stream = true;
    const body = JSON.stringify(request);
    if (byteLength(body) > MAX_REQUEST_BYTES) throw new Error("WebSocket request exceeds bound");
    const entry = { kind: stream ? "watch" : "call", resolve: null, reject: null, queue: [], terminal: null, waiter: null, pages: 0, bytes: 0, done: false, error: null, timer: null, signal: callSignal, cancel: null };
    const promise = new Promise((resolve, reject) => { entry.resolve = resolve; entry.reject = reject; });
    pending.set(id, entry);
    const deadline = stream ? 22000 : 5000;
    entry.timer = setTimeout(() => terminate(new Error("WebSocket request deadline exceeded")), deadline);
    const cancel = () => terminate(new DOMException("The operation was aborted", "AbortError"));
    entry.cancel = cancel;
    if (callSignal) {
      callSignal.addEventListener("abort", cancel, { once: true });
    }
    try {
      if (socket.bufferedAmount > MAX_RESPONSE_BYTES) throw new Error("WebSocket write buffer is full");
      socket.send(body);
    } catch (error) {
      pending.delete(id); cleanupEntry(entry); throw error;
    }
    if (!stream) return promise;
    const iterator = {
      next: () => {
        if (entry.error) return Promise.reject(entry.error);
        if (entry.queue.length) return Promise.resolve({ done: false, value: entry.queue.shift() });
        if (entry.terminal) { const terminal = entry.terminal; entry.terminal = null; return Promise.resolve({ done: false, value: terminal }); }
        if (entry.done) return Promise.resolve({ done: true, value: undefined });
        if (entry.waiter) return Promise.reject(new Error("concurrent WebSocket iterator.next()"));
        return new Promise(resolve => { entry.waiter = resolve; });
      },
      return: async () => { await close(); return { done: true, value: undefined }; },
      [Symbol.asyncIterator]() { return this; }
    };
    return iterator;
  };
  const close = async () => {
    if (!closing) { closing = true; try { socket.close(1000, ""); } catch { /* already closed */ } }
    const joined = await new Promise(resolve => {
      const timer = setTimeout(() => resolve(false), 5000);
      closed.then(() => { clearTimeout(timer); resolve(true); });
    });
    if (!joined) throw new Error("WebSocket close deadline exceeded");
  };
  return {
    call: (profile, envelope, options = {}) => send(profile, envelope, false, options.signal),
    watch: (profile, envelope, options = {}) => send(profile, envelope, true, options.signal),
    close
  };
}

export { encryptEnvelope, signEnvelope };
