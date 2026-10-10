// One bounded SSE lease. Keep the bearer in caller memory; never put it in URLs
// or browser storage. Renew explicitly with the last completely decoded page ID.
export async function* roomEvents(base, token, { target, cursor = "", start = "", signal } = {}) {
  const url = new URL("/v1/events", base);
  url.searchParams.set("kind", "room");
  url.searchParams.set("target", target);
  if (start) url.searchParams.set("start", start);
  const headers = { Authorization: `Bearer ${token}`, Accept: "text/event-stream" };
  if (cursor) headers["Last-Event-ID"] = cursor;
  const response = await fetch(url, { headers, signal, credentials: "omit", redirect: "error", cache: "no-store" });
  yield* eventFrames(response);
}

// Shared bounded parser for bearer and detached Fetch streams.
export async function* eventFrames(response) {
  if (!response.ok) throw new Error(`gateway HTTP ${response.status}`);
  if (response.headers.get("content-type") !== "text/event-stream; charset=utf-8" || !response.body) throw new Error("invalid event stream");
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const encoder = new TextEncoder();
  let pending = "", total = 0, frames = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        pending += decoder.decode();
        if (pending) throw new Error("truncated event frame");
        return;
      }
      total += value.byteLength;
      if (total > 1048576) throw new Error("event lease exceeds byte bound");
      pending += decoder.decode(value, { stream: true });
      let end;
      while ((end = pending.indexOf("\n\n")) !== -1) {
        const frame = pending.slice(0, end);
        pending = pending.slice(end + 2);
        if (encoder.encode(frame).byteLength + 2 > 65536 || ++frames > 129) throw new Error("event frame exceeds bound");
        let event = "", id = "", data = [];
        for (const line of frame.split("\n")) {
          if (line.startsWith(":")) continue;
          const colon = line.indexOf(":");
          if (colon === -1) throw new Error("invalid event field");
          const name = line.slice(0, colon), value = line.slice(colon + 1).replace(/^ /, "");
          if (name === "event") event = value;
          else if (name === "id") id = value;
          else if (name === "data") data.push(value);
          else if (name !== "retry") throw new Error("unknown event field");
        }
        if (!event && !data.length) continue;
        if (event !== "page" && event !== "end") throw new Error("unknown event type");
        const value = JSON.parse(data.join("\n"));
        if (event === "page" && (value.cursor !== id || !Array.isArray(value.messages))) throw new Error("invalid page checkpoint");
        if (event === "end" && id) throw new Error("terminal checkpoint forbidden");
        yield { event, id, value };
        if (event === "end") return;
      }
      if (encoder.encode(pending).byteLength > 65536) throw new Error("unfinished event frame exceeds bound");
    }
  } finally {
    try { await reader.cancel(); } catch { /* An aborted reader is already closed. */ } finally { reader.releaseLock(); }
  }
}

// MessageMetadata preserves the upstream URL-safe unpadded UTF-8 body encoding.
export function messageText(message) {
  const encoded = message.body_base64;
  if (typeof encoded !== "string" || encoded.length > 5462 || !/^[A-Za-z0-9_-]*$/.test(encoded) || encoded.length % 4 === 1) throw new Error("invalid message body");
  const raw = atob(encoded.replace(/-/g, "+").replace(/_/g, "/"));
  if (raw.length > 4096 || btoa(raw).replace(/=/g, "").replace(/\+/g, "-").replace(/\//g, "_") !== encoded) throw new Error("noncanonical message body");
  return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(Uint8Array.from(raw, char => char.charCodeAt(0)));
}
