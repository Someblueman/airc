// Fixed gateway profiles. Keys are caller-provisioned CryptoKeys kept in memory;
// this module never stores keys/tokens or negotiates a weaker algorithm.
import { eventFrames } from "./events-fetch.js";

const utf8 = new TextEncoder();
const bytes = value => new Uint8Array(value);
const b64 = value => btoa(Array.from(bytes(value), b => String.fromCharCode(b)).join(""))
  .replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_");
const json64 = value => b64(utf8.encode(JSON.stringify(value)));
const uint32 = value => { const data = new Uint8Array(4); new DataView(data.buffer).setUint32(0, value); return data; };
const join = (...parts) => { const result = new Uint8Array(parts.reduce((n, p) => n + p.length, 0)); let offset = 0; for (const part of parts) { result.set(part, offset); offset += part.length; } return result; };

export async function signEnvelope(key, kid, audience, request, { issuedAt = Math.floor(Date.now() / 1000), expiresAt = issuedAt + 120 } = {}) {
  if (key.type !== "private" || key.algorithm.name !== "Ed25519" || !kid || !audience) throw new Error("invalid signing profile");
  const intent = { ...request };
  if (["send", "reply", "reaction.add", "profile.update", "presence.set", "topic.set", "room.update", "pins.add", "pins.remove", "activity.set", "reply_signal.set", "reply_signal.cancel", "poll.create", "poll.vote", "poll.close", "message.action", "message.correct", "message.retract"].includes(intent.operation)) {
    if (intent.issued_at !== undefined && intent.issued_at !== issuedAt || intent.expires_at !== undefined && intent.expires_at !== expiresAt) throw new Error("mutation validity mismatch");
    intent.issued_at = issuedAt;
    intent.expires_at = expiresAt;
  }
  const payload = utf8.encode(JSON.stringify({ version: 1, audience, issued_at: issuedAt, expires_at: expiresAt, request: intent }));
  if (payload.length > 16384) throw new Error("envelope exceeds payload bound");
  const input = json64({ alg: "EdDSA", kid }) + "." + b64(payload);
  const signature = await crypto.subtle.sign("Ed25519", key, utf8.encode(input));
  const token = input + "." + b64(signature);
  if (token.length > 32768) throw new Error("signed envelope exceeds bound");
  return token;
}

export async function encryptEnvelope(recipient, kid, signed) {
  if (recipient.type !== "public" || recipient.algorithm.name !== "ECDH" || recipient.algorithm.namedCurve !== "P-256" || !kid || typeof signed !== "string" || signed.length > 32768 || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(signed)) throw new Error("invalid encryption profile");
  const ephemeral = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
  const publicKey = await crypto.subtle.exportKey("jwk", ephemeral.publicKey);
  const header = json64({ alg: "ECDH-ES", enc: "A256GCM", kid, epk: { kty: "EC", crv: "P-256", x: publicKey.x, y: publicKey.y } });
  const shared = bytes(await crypto.subtle.deriveBits({ name: "ECDH", public: recipient }, ephemeral.privateKey, 256));
  // RFC 7518 section 4.6.2: one SHA-256 Concat KDF round, AlgorithmID is
  // "A256GCM", absent PartyU/PartyVInfo are empty, keydatalen is 256 bits.
  const algorithm = utf8.encode("A256GCM");
  const kdfInput = join(uint32(1), shared, uint32(algorithm.length), algorithm, uint32(0), uint32(0), uint32(256));
  const rawKey = bytes(await crypto.subtle.digest("SHA-256", kdfInput));
  shared.fill(0); kdfInput.fill(0);
  const key = await crypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["encrypt"]);
  rawKey.fill(0);
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const encrypted = bytes(await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: utf8.encode(header), tagLength: 128 }, key, utf8.encode(signed)));
  const token = [header, "", b64(iv), b64(encrypted.slice(0, -16)), b64(encrypted.slice(-16))].join(".");
  if (token.length > 65536) throw new Error("encrypted envelope exceeds bound");
  return token;
}

export function callEnvelope(base, token, { encrypted = false, stream = false, signal } = {}) {
  const profile = encrypted ? "encrypted" : "signed";
  if (typeof token !== "string" || token.length > (encrypted ? 65536 : 32768)) throw new Error("envelope exceeds bound");
  return fetch(new URL(`/v1/envelopes/${profile}${stream ? "/events" : ""}`, base), {
    method: "POST", headers: { "Content-Type": "application/jose", Accept: stream ? "text/event-stream" : "application/json" },
    body: token, signal, credentials: "omit", redirect: "error", cache: "no-store"
  });
}

export async function* envelopeEvents(base, token, options = {}) {
  yield* eventFrames(await callEnvelope(base, token, { ...options, stream: true }));
}
