import { encryptEnvelope, signEnvelope } from "./envelopes-fetch.js";

const text = new TextEncoder();
const MAX_REQUEST = 16 * 1024;
const MAX_RESPONSE = 64 * 1024;
const MAX_STREAM = 1024 * 1024;
const MAX_PAGES = 128;
const MAX_PENDING = 4;
const MAGIC = "AGD1";
const DATAGRAM_HEADER = 28;
const DATAGRAM_PAYLOAD = 972;

const fail = message => { const error = new Error(message); error.code = "protocol_error"; return error; };
const boundedSignal = (signal, milliseconds) => { const timeout = AbortSignal.timeout(milliseconds); return signal ? AbortSignal.any([signal, timeout]) : timeout; };
async function awaitWithSignal(promise, signal, cancel = () => {}) {
  let abort;
  const stopped = new Promise((_, reject) => {
    abort = () => { cancel(); reject(signal.reason || new DOMException("Aborted", "AbortError")); };
    if (signal.aborted) abort();
    else signal.addEventListener("abort", abort, { once: true });
  });
  try {
    const value = await Promise.race([promise, stopped]);
    if (signal.aborted) throw signal.reason || new DOMException("Aborted", "AbortError");
    return value;
  }
  catch (error) {
    if (signal.aborted) {
      let timer;
      try {
        await Promise.race([Promise.allSettled([promise]), new Promise((_, reject) => {
          timer = setTimeout(() => reject(fail("cancellation did not settle within five seconds")), 5000);
        })]);
      } finally { clearTimeout(timer); }
    }
    throw error;
  } finally { signal.removeEventListener("abort", abort); }
}
function gatewayError(failure) {
  const error = new Error(`gateway ${failure.code}`);
  error.code = failure.code;
  error.transient = failure.transient === true;
  return error;
}
const frame = value => {
  const body = text.encode(JSON.stringify(value));
  if (!body.length || body.length > MAX_REQUEST) throw fail("request frame exceeds bound");
  const out = new Uint8Array(4 + body.length);
  new DataView(out.buffer).setUint32(0, body.length);
  out.set(body, 4);
  return out;
};
const concat = (a, b) => { const out = new Uint8Array(a.length + b.length); out.set(a); out.set(b, a.length); return out; };
async function readChunk(reader, signal) {
  return awaitWithSignal(reader.read(), signal, () => { reader.cancel().catch(() => {}); });
}
async function readFrame(reader, state, signal) {
  while (state.bytes.length < 4) { const x = await readChunk(reader, signal); if (x.done) throw new Error("transport ended before frame"); if (state.bytes.length + x.value.length > MAX_STREAM) throw fail("stream buffer exceeds bound"); state.bytes = concat(state.bytes, x.value); }
  const size = new DataView(state.bytes.buffer, state.bytes.byteOffset).getUint32(0);
  if (!size || size > MAX_RESPONSE) throw fail("response frame exceeds bound");
  while (state.bytes.length < 4 + size) { const x = await readChunk(reader, signal); if (x.done) throw new Error("transport ended in frame"); if (state.bytes.length + x.value.length > MAX_STREAM) throw fail("stream buffer exceeds bound"); state.bytes = concat(state.bytes, x.value); }
  const body = state.bytes.slice(4, 4 + size); state.bytes = state.bytes.slice(4 + size);
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body));
}
function validate(value, datagram = false) {
  if (!value || value.version !== 1 || typeof value.correlation_id !== "string" || !/^[A-Za-z0-9_-]{1,64}$/.test(value.correlation_id)) throw fail("invalid response");
  if (!["result", "page", "end", "error"].includes(value.event)) throw fail("unknown response event");
  if (value.event === "result" && (!value.result || typeof value.result !== "object")) throw fail("missing result");
  if (value.event === "page" && (!value.page || !Array.isArray(value.page.messages) || typeof value.page.cursor !== "string")) throw fail("invalid page");
  if (value.event === "end" && !["resume", "lease_expired", "capacity", "upstream_unavailable"].includes(value.reason)) throw fail("invalid end");
  if (value.event === "error" && (!value.error || typeof value.error.code !== "string")) throw fail("invalid error");
  if (datagram && (!Number.isInteger(value.sequence) || value.sequence < 1)) throw fail("missing datagram response sequence");
  if (!datagram && Object.prototype.hasOwnProperty.call(value, "sequence")) throw fail("unexpected reliable response sequence");
  return value;
}
function request(correlation, profile, envelope, stream = false) {
  if (!["signed", "encrypted"].includes(profile) || typeof envelope !== "string" || !envelope) throw fail("invalid envelope");
  const value = { version: 1, correlation_id: correlation, profile, envelope };
  if (stream) value.stream = true;
  const body = text.encode(JSON.stringify(value));
  if (body.length > MAX_REQUEST) throw fail("request exceeds bound");
  return { value, body };
}
function id() { return `wt-${crypto.randomUUID().replaceAll("-", "").slice(0, 56)}`; }

async function writeReliable(stream, body, signal) {
  const writer = stream.writable.getWriter();
  try {
    await awaitWithSignal(writer.write(body), signal, () => { writer.abort().catch(() => {}); });
    await awaitWithSignal(writer.close(), signal, () => { writer.abort().catch(() => {}); });
  } finally { writer.releaseLock(); }
}
function fragments(body) {
  const count = Math.ceil(body.length / DATAGRAM_PAYLOAD); const nonce = crypto.getRandomValues(new Uint8Array(16)); const out = [];
  for (let i = 0; i < count; i++) {
    const part = body.slice(i * DATAGRAM_PAYLOAD, Math.min(body.length, (i + 1) * DATAGRAM_PAYLOAD));
    const d = new Uint8Array(DATAGRAM_HEADER + part.length); d.set(text.encode(MAGIC)); d.set(nonce, 4);
    const view = new DataView(d.buffer); view.setUint32(20, body.length); view.setUint16(24, i); view.setUint16(26, count); d.set(part, DATAGRAM_HEADER); out.push(d);
  }
  return out;
}
async function readDatagram(reader, pending, completed, signal, correlation) {
  for (;;) {
    const now = Date.now();
    for (const [id, expiry] of completed) if (expiry <= now) completed.delete(id);
    let receiveSignal = signal;
    if (pending.size) {
      const remaining = Math.min(...Array.from(pending.values(), entry => entry.deadline - now));
      if (remaining <= 0) throw fail("datagram fragment assembly expired");
      receiveSignal = AbortSignal.any([signal, AbortSignal.timeout(remaining)]);
    }
    const x = await readChunk(reader, receiveSignal); if (x.done) throw new Error("datagrams ended"); const d = x.value;
    if (d.length < DATAGRAM_HEADER || new TextDecoder().decode(d.slice(0, 4)) !== MAGIC || d.length > 1000) continue;
    const view = new DataView(d.buffer, d.byteOffset); const key = Array.from(d.slice(4, 20)).join(","); const total = view.getUint32(20), index = view.getUint16(24), count = view.getUint16(26);
    if (!total || total > MAX_RESPONSE || !count || index >= count || count !== Math.ceil(total / DATAGRAM_PAYLOAD)) continue;
    if (completed.has(key)) continue;
    let entry = pending.get(key); if (!entry) { if (pending.size >= MAX_PENDING) continue; entry = { total, count, parts: [], got: 0, deadline: Date.now() + 5000 }; pending.set(key, entry); }
    if (Date.now() > entry.deadline) { pending.delete(key); continue; }
    const expected = Math.min(DATAGRAM_PAYLOAD, total - index * DATAGRAM_PAYLOAD);
    if (d.length - DATAGRAM_HEADER !== expected) { pending.delete(key); continue; }
    if (entry.total !== total || entry.count !== count) { pending.delete(key); continue; }
    if (entry.parts[index]) { if (entry.parts[index].length !== d.length - DATAGRAM_HEADER || entry.parts[index].some((v, i) => v !== d[DATAGRAM_HEADER + i])) pending.delete(key); continue; }
    entry.parts[index] = d.slice(DATAGRAM_HEADER); entry.got++;
    if (entry.got !== count) continue;
    pending.delete(key); completed.set(key, Date.now() + 30000); while (completed.size > 64) completed.delete(completed.keys().next().value);
    let body = new Uint8Array(); for (const part of entry.parts) body = concat(body, part);
    if (body.length !== total || body.length > MAX_RESPONSE) continue;
    const response = validate(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body)), true);
    if (response.correlation_id !== correlation) continue;
    return response;
  }
}
async function readDatagramContiguous(reader, fragmentsPending, completed, reorder, expected, signal, correlation) {
  let combined = signal, gapStarted = false;
  for (;;) {
    const response = await readDatagram(reader, fragmentsPending, completed, combined, correlation);
    if (response.sequence < expected) continue;
    if (response.sequence > expected) {
      if (reorder.size >= 4) throw fail("datagram response reorder buffer exceeded");
      reorder.set(response.sequence, response);
      if (!gapStarted) {
        gapStarted = true;
        const gap = AbortSignal.timeout(5000);
        combined = AbortSignal.any([signal, gap]);
      }
      continue;
    }
    return response;
  }
}

export async function openEnvelopeTransport(url, { serverCertificateHashes, signal } = {}) {
  if (typeof url !== "string" || !url || signal?.aborted) throw fail("invalid WebTransport URL");
  const session = new WebTransport(url, {
    allowPooling: false, requireUnreliable: true, protocols: ["airc-gateway.v1"], serverCertificateHashes
  });
  const close = async () => {
    session.close({ closeCode: 0, reason: "client close" });
    let timer;
    try {
      await Promise.race([session.closed.catch(() => {}), new Promise((_, reject) => {
        timer = setTimeout(() => reject(fail("session close deadline exceeded")), 5000);
      })]);
    } finally { clearTimeout(timer); }
  };
  const abort = () => { session.close({ closeCode: 0, reason: "client abort" }); };
  signal?.addEventListener("abort", abort, { once: true });
  try { await awaitWithSignal(session.ready, boundedSignal(signal, 5000), abort); }
  catch (error) { await close(); signal?.removeEventListener("abort", abort); throw error; }

  const pending = new Set();
  let exclusive = false;
  function admit(mode, correlation) {
    if (!["stream", "uni", "datagram"].includes(mode)) throw fail("unknown WebTransport mode");
    if (pending.size >= MAX_PENDING || (mode !== "stream" && exclusive)) throw fail("request capacity exceeded");
    if (mode !== "stream") exclusive = true;
    pending.add(correlation);
  }
  function release(mode, correlation) {
    pending.delete(correlation);
    if (mode !== "stream") exclusive = false;
  }
  async function reliable(value, mode, operationSignal) {
    const writeSignal = boundedSignal(operationSignal, 3000);
    if (mode === "stream") {
      const stream = await awaitWithSignal(session.createBidirectionalStream(), operationSignal, abort);
      try { await writeReliable(stream, frame(value), writeSignal); }
      catch (error) { await stream.readable.cancel().catch(() => {}); throw error; }
      return stream.readable;
    }
    const send = await awaitWithSignal(session.createUnidirectionalStream(), operationSignal, abort);
    await writeReliable({ writable: send }, frame(value), writeSignal);
    const incoming = session.incomingUnidirectionalStreams.getReader();
    try {
      // Canceling this session-wide incoming stream would disable subsequent uni
      // calls. A timeout closes the session to settle the outstanding accept.
      const next = await awaitWithSignal(incoming.read(), operationSignal, abort);
      if (next.done) throw fail("missing unidirectional response");
      return next.value;
    } finally { incoming.releaseLock(); }
  }
  async function sendDatagrams(writer, body, operationSignal) {
    const writeSignal = boundedSignal(operationSignal, 3000);
    for (const part of fragments(body)) await awaitWithSignal(writer.write(part), writeSignal, abort);
  }
  const call = async (profile, envelope, { mode = "stream", signal: callSignal } = {}) => {
    const correlation = id(), { value, body } = request(correlation, profile, envelope);
    admit(mode, correlation);
    const operationSignal = boundedSignal(callSignal, 5000);
    let reader, writer;
    try {
      let answer;
      if (mode === "datagram") {
        writer = session.datagrams.writable.getWriter();
        reader = session.datagrams.readable.getReader();
        await sendDatagrams(writer, body, operationSignal);
        answer = await readDatagram(reader, new Map(), new Map(), operationSignal, correlation);
        if (answer.sequence !== 1) throw fail("unexpected datagram result sequence");
      } else {
        reader = (await reliable(value, mode, operationSignal)).getReader();
        answer = validate(await readFrame(reader, { bytes: new Uint8Array() }, operationSignal));
        if (answer.correlation_id !== correlation) throw fail("uncorrelated response");
      }
      if (answer.event === "error") throw gatewayError(answer.error);
      if (answer.event !== "result") throw fail("ordinary call returned a stream event");
      return answer.result;
    } catch (error) {
      // A datagram receive queue is session-wide. Closing the session both
      // cancels the uncertain request and settles an interrupted queue read.
      if (mode === "datagram") await close();
      throw error;
    } finally {
      if (reader) {
        if (mode !== "datagram") await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      writer?.releaseLock();
      release(mode, correlation);
    }
  };
  const watch = async function* (profile, envelope, { mode = "stream", signal: watchSignal } = {}) {
    const correlation = id(), { value, body } = request(correlation, profile, envelope, true);
    admit(mode, correlation);
    const operationSignal = boundedSignal(watchSignal, 22000);
    let reader, writer, pages = 0, bytes = 0, cursor = "", terminal = false;
    try {
      let next;
      if (mode === "datagram") {
        writer = session.datagrams.writable.getWriter();
        reader = session.datagrams.readable.getReader();
        await sendDatagrams(writer, body, operationSignal);
        const partial = new Map(), completed = new Map(), reorder = new Map();
        let expected = 1;
        next = async () => {
          const answer = reorder.get(expected) || await readDatagramContiguous(reader, partial, completed, reorder, expected, operationSignal, correlation);
          reorder.delete(expected++);
          return answer;
        };
      } else {
        reader = (await reliable(value, mode, operationSignal)).getReader();
        const state = { bytes: new Uint8Array() };
        next = async () => {
          const answer = validate(await readFrame(reader, state, operationSignal));
          if (answer.correlation_id !== correlation) throw fail("uncorrelated watch response");
          return answer;
        };
      }
      for (;;) {
        const answer = await next();
        bytes += text.encode(JSON.stringify(answer)).length + 4;
        if (bytes > MAX_STREAM) throw fail("watch exceeds byte bound");
        if (answer.event === "page") {
          if (++pages > MAX_PAGES) throw fail("watch exceeds page bound");
          cursor = answer.page.cursor;
          yield answer.page;
        } else if (answer.event === "end") { terminal = true; return answer.reason; }
        else if (answer.event === "error") throw gatewayError(answer.error);
        else throw fail("watch returned ordinary result");
      }
    } catch (error) {
      if (mode === "datagram") { error.code ||= "delivery_uncertain"; error.cursor = cursor; }
      throw error;
    } finally {
      if (mode === "datagram" && !terminal) await close();
      if (reader) {
        if (mode !== "datagram") await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      writer?.releaseLock();
      release(mode, correlation);
    }
  };
  return { session, call, watch, close: async () => {
    signal?.removeEventListener("abort", abort);
    await close();
  } };
}
export { encryptEnvelope, signEnvelope };
