import { encryptEnvelope, signEnvelope } from "./envelopes-fetch.js";

const encoder = new TextEncoder();
const decoder = new TextDecoder("utf-8", { fatal: true });
const modes = new Set(["reliable_ordered", "reliable_unordered", "unreliable_ordered", "unreliable_unordered"]);
const HEADER = 28, PAYLOAD = 972, MAX_REQUEST = 16384, MAX_RESPONSE = 65536;
const failure = message => Object.assign(new Error(message), { code: "delivery_uncertain" });
const bounded = (signal, ms) => signal ? AbortSignal.any([signal, AbortSignal.timeout(ms)]) : AbortSignal.timeout(ms);

async function wait(promise, signal) {
  let abort;
  const stopped = new Promise((_, reject) => {
    abort = () => reject(signal.reason || new DOMException("Aborted", "AbortError"));
    if (signal.aborted) abort(); else signal.addEventListener("abort", abort, { once: true });
  });
  try { return await Promise.race([promise, stopped]); }
  finally { signal.removeEventListener("abort", abort); }
}

async function boundedBody(response, signal) {
  const reader = response.body.getReader();
  const chunks = []; let size = 0;
  try {
    for (;;) {
      const part = await wait(reader.read(), signal);
      if (part.done) break;
      size += part.value.length;
      if (size > 65536) throw failure("signaling response exceeds bound");
      chunks.push(part.value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    return decoder.decode(bytes);
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}

function fragments(body) {
  if (!body.length || body.length > MAX_REQUEST) throw failure("request exceeds 16 KiB");
  const nonce = crypto.getRandomValues(new Uint8Array(16));
  const count = Math.ceil(body.length / PAYLOAD);
  return Array.from({ length: count }, (_, index) => {
    const part = body.slice(index * PAYLOAD, (index + 1) * PAYLOAD);
    const out = new Uint8Array(HEADER + part.length), view = new DataView(out.buffer);
    out.set(encoder.encode("AGD1")); out.set(nonce, 4);
    view.setUint32(20, body.length); view.setUint16(24, index); view.setUint16(26, count);
    out.set(part, HEADER); return out;
  });
}

// Signaling credentials select the peer owner; every call still needs its own
// signed or encrypted envelope. No automatic mutation retry occurs here.
export async function connectWebRTC(base, { bearer, mode = "reliable_ordered", iceServers = [], iceTransportPolicy = "all", signal } = {}) {
  const endpoint = new URL(base);
  if (endpoint.protocol !== "https:" || endpoint.username || endpoint.password || !modes.has(mode) || typeof bearer !== "string" || !bearer) throw failure("invalid protected signaling options");
  endpoint.pathname = "/v1/webrtc/offer"; endpoint.search = ""; endpoint.hash = "";
  const peer = new RTCPeerConnection({ iceServers, iceTransportPolicy });
  const options = { ordered: !mode.endsWith("unordered"), protocol: "agd1" };
  if (mode.startsWith("unreliable_")) options.maxRetransmits = 0;
  const channel = peer.createDataChannel("airc-gateway-v1", options);
  channel.binaryType = "arraybuffer";
  const active = new Map(), pending = new Map(), completed = new Map();
  let closed = false, timer;
  const close = (error = failure("WebRTC peer closed")) => {
    if (closed) return; closed = true; clearInterval(timer);
    for (const state of active.values()) {
      state.error = Object.assign(new Error(error.message), { code: error.code || "delivery_uncertain", cursor: state.cursor });
      state.wake?.();
    }
    pending.clear(); completed.clear(); channel.close(); peer.close();
    signal?.removeEventListener("abort", stop);
  };
  const stop = () => close(failure("WebRTC peer cancelled"));
  signal?.addEventListener("abort", stop, { once: true });
  if (signal?.aborted) stop();
  channel.onclose = () => close();
  channel.onerror = () => close(failure("DataChannel failed"));
  peer.onconnectionstatechange = () => { if (["failed", "closed"].includes(peer.connectionState)) close(); };
  const receive = body => {
    const response = JSON.parse(decoder.decode(body));
    if (!response || response.version !== 1 || !/^[A-Za-z0-9_-]{1,64}$/.test(response.correlation_id) || !Number.isSafeInteger(response.sequence) || response.sequence < 1 || !["result", "page", "end", "error"].includes(response.event)) throw failure("invalid correlated response");
    const state = active.get(response.correlation_id);
    if (!state) return;
    if (response.sequence < state.next || state.frames.has(response.sequence)) return;
    if (response.sequence > state.next + 8 || state.frames.size >= 8 || state.buffered + body.length > 262144) throw failure("response sequencing buffer exceeds bound");
    if (response.event === "result" && (!response.result || typeof response.result !== "object")) throw failure("missing result");
    if (response.event === "page" && (!response.page || !Array.isArray(response.page.messages) || typeof response.page.cursor !== "string")) throw failure("invalid page");
    if (response.event === "end" && !["resume", "lease_expired", "capacity", "upstream_unavailable"].includes(response.reason)) throw failure("invalid watch completion");
    if (response.event === "error" && (!response.error || typeof response.error.code !== "string")) throw failure("invalid gateway failure");
    state.frames.set(response.sequence, { response, bytes: body.length }); state.buffered += body.length; state.wake?.();
  };
  channel.onmessage = event => {
    try {
      if (!(event.data instanceof ArrayBuffer)) throw failure("expected binary AGD1 fragment");
      const data = new Uint8Array(event.data), view = new DataView(event.data);
      if (data.length < HEADER || data.length > 1000 || decoder.decode(data.slice(0, 4)) !== "AGD1") throw failure("invalid fragment");
      const key = Array.from(data.slice(4, 20)).join(","), total = view.getUint32(20), index = view.getUint16(24), count = view.getUint16(26);
      if (!total || total > MAX_RESPONSE || count !== Math.ceil(total / PAYLOAD) || index >= count || data.length !== HEADER + Math.min(PAYLOAD, total - index * PAYLOAD)) throw failure("invalid fragment bounds");
      if (completed.has(key)) return;
      let entry = pending.get(key);
      if (!entry) {
        if (pending.size >= 4) throw failure("too many incomplete responses");
        entry = { total, count, parts: [], got: 0, expiry: Date.now() + 5000 }; pending.set(key, entry);
      }
      if (entry.total !== total || entry.count !== count) throw failure("conflicting response fragments");
      const part = data.slice(HEADER);
      if (entry.parts[index]) {
        if (entry.parts[index].some((byte, i) => byte !== part[i])) throw failure("conflicting duplicate fragment");
        return;
      }
      entry.parts[index] = part; entry.got++;
      if (entry.got !== count) return;
      const body = new Uint8Array(total); entry.parts.forEach((part, i) => body.set(part, i * PAYLOAD));
      pending.delete(key); completed.set(key, Date.now() + 30000);
      while (completed.size > 64) completed.delete(completed.keys().next().value);
      receive(body);
    } catch (error) { close(error); }
  };
  timer = setInterval(() => {
    const now = Date.now();
    for (const entry of pending.values()) if (entry.expiry <= now) { close(failure("response fragment assembly expired")); return; }
    for (const [key, expiry] of completed) if (expiry <= now) completed.delete(key);
  }, 1000);
  try {
    const opening = bounded(signal, 15000);
    const opened = new Promise((resolve, reject) => {
      channel.onopen = resolve;
      channel.addEventListener("close", () => reject(failure("channel closed before opening")), { once: true });
    });
    opened.catch(() => {});
    await peer.setLocalDescription(await peer.createOffer());
    if (peer.iceGatheringState !== "complete") await wait(new Promise(resolve => {
      const gathered = () => { if (peer.iceGatheringState === "complete") { peer.removeEventListener("icegatheringstatechange", gathered); resolve(); } };
      peer.addEventListener("icegatheringstatechange", gathered); gathered();
    }), opening);
    const response = await fetch(endpoint, { method: "POST", credentials: "omit", redirect: "error", cache: "no-store", headers: { Authorization: `Bearer ${bearer}`, "Content-Type": "application/json" }, body: JSON.stringify({ version: 1, sdp: peer.localDescription.sdp, mode }), signal: opening });
    if (!response.ok) throw failure(`signaling status ${response.status}`);
    const text = await boundedBody(response, opening);
    const answer = JSON.parse(text);
    if (answer.version !== 1 || typeof answer.sdp !== "string" || !answer.sdp || answer.sdp.length > 61440) throw failure("invalid signaling answer");
    await peer.setRemoteDescription({ type: "answer", sdp: answer.sdp });
    await wait(opened, opening);
  } catch (error) { close(error); throw error; }

  const begin = async (profile, envelope, streaming, operationSignal) => {
    if (closed || active.size >= 4 || !["signed", "encrypted"].includes(profile) || typeof envelope !== "string" || !envelope) throw failure("invalid or unavailable request");
    const correlation = `rtc-${crypto.randomUUID().replaceAll("-", "")}`;
    const body = encoder.encode(JSON.stringify({ version: 1, correlation_id: correlation, profile, envelope, ...(streaming ? { stream: true } : {}) }));
    const parts = fragments(body), state = { frames: new Map(), next: 1, buffered: 0, bytes: 0, pages: 0, cursor: "", error: null, wake: null };
    active.set(correlation, state);
    try {
      for (const part of parts) {
        while (channel.bufferedAmount + part.length > 65536) {
          await wait(new Promise(resolve => setTimeout(resolve, 20)), operationSignal);
          if (closed) throw state.error || failure("peer closed during write");
        }
        if (operationSignal.aborted) throw operationSignal.reason;
        channel.send(part);
      }
    } catch (error) { active.delete(correlation); close(error); throw error; }
    const next = async () => {
      while (!state.frames.has(state.next)) {
        if (state.error) throw state.error;
        await wait(new Promise(resolve => { state.wake = resolve; }), operationSignal);
        state.wake = null;
      }
      const frame = state.frames.get(state.next++); state.frames.delete(state.next - 1); state.buffered -= frame.bytes; state.bytes += frame.bytes;
      if (state.bytes > 1048576) throw failure("watch byte budget exceeded");
      return frame.response;
    };
    return { correlation, state, next };
  };
  const call = async (profile, envelope, { signal } = {}) => {
    const operationSignal = bounded(signal, 4000);
    const request = await begin(profile, envelope, false, operationSignal);
    try {
      const response = await request.next();
      if (response.event !== "result" && response.event !== "error") throw failure("ordinary call returned stream data");
      return response;
    } catch (error) { close(error); throw error; }
    finally { active.delete(request.correlation); }
  };
  const watch = async function* (profile, envelope, { signal } = {}) {
    const operationSignal = bounded(signal, 22000);
    const request = await begin(profile, envelope, true, operationSignal);
    let terminal = false;
    try {
      for (;;) {
        const response = await request.next();
        if (response.event === "page") {
          if (++request.state.pages > 128) throw failure("watch page budget exceeded");
          request.state.cursor = response.page.cursor; yield response.page;
        } else if (response.event === "end") { terminal = true; return response.reason; }
        else if (response.event === "error") { terminal = true; throw Object.assign(new Error(`gateway ${response.error.code}`), response.error); }
        else throw failure("watch returned ordinary result");
      }
    } catch (error) { error.cursor = request.state.cursor; throw error; }
    finally { active.delete(request.correlation); if (!terminal) close(failure("watch cancelled or delivery uncertain")); }
  };
  return { peer, channel, call, watch, close };
}

export { encryptEnvelope, signEnvelope };
