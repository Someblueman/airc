// JSON-RPC correlation IDs are separate from params.request_id. Keep the bearer
// in caller memory. Retry uncertain mutations with the same durable request_id.
export async function rpcCall(base, token, method, params, { id, signal } = {}) {
  const request = { jsonrpc: "2.0", method };
  if (params !== undefined) request.params = params;
  if (id !== undefined) request.id = id;
  const response = await fetch(new URL("/v1/rpc", base), {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify(request), signal, credentials: "omit", redirect: "error", cache: "no-store"
  });
  if (id === undefined) {
    if (response.status !== 204) throw new Error(`notification HTTP ${response.status}`);
    return;
  }
  if (!response.ok) throw new Error(`RPC HTTP ${response.status}`);
  const value = JSON.parse(await boundedText(response.body, 1048576));
  checkResponse(value, id);
  if (value.error) throw new Error(`RPC ${value.error.code}: ${value.error.message}`);
  return value.result;
}

// One bounded lease. Renew with the last complete page.cursor inside a new
// gateway request. RPC/SSE frames themselves do not invent resumable event IDs.
export async function* rpcEvents(base, token, request, { id, signal } = {}) {
  if (id === undefined || !["watch", "events.watch"].includes(request.operation)) throw new Error("watch call requires correlation id");
  const response = await fetch(new URL("/v1/rpc/events", base), {
    method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", method: "airc.request", params: request, id }),
    signal, credentials: "omit", redirect: "error", cache: "no-store"
  });
  if (!response.ok) throw new Error(`RPC stream HTTP ${response.status}`);
  if (response.headers.get("content-type") !== "text/event-stream; charset=utf-8" || !response.body) throw new Error("invalid RPC stream");
  const reader = response.body.getReader(), decoder = new TextDecoder("utf-8", { fatal: true });
  const encoder = new TextEncoder();
  let pending = "", total = 0, frames = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        pending += decoder.decode();
        if (pending) throw new Error("truncated RPC frame");
        throw new Error("RPC stream ended without terminal response");
      }
      total += value.byteLength;
      if (total > 1048576) throw new Error("RPC stream exceeds lease bound");
      pending += decoder.decode(value, { stream: true });
      let end;
      while ((end = pending.indexOf("\n\n")) !== -1) {
        const frame = pending.slice(0, end);
        pending = pending.slice(end + 2);
        if (encoder.encode(frame).byteLength + 2 > 65536 || ++frames > 129) throw new Error("RPC frame exceeds bound");
        const lines = frame.split("\n");
        if (lines.every(line => line.startsWith(":"))) continue;
        if (lines[0] !== "event: message" || lines.length !== 2 || !lines[1].startsWith("data: ")) throw new Error("invalid RPC SSE envelope");
        const rpc = JSON.parse(lines[1].slice(6));
        if (rpc.jsonrpc !== "2.0") throw new Error("invalid RPC version");
        if (rpc.method === "airc.page") {
          if ("id" in rpc || rpc.params?.id !== id || !Array.isArray(rpc.params?.page?.messages)) throw new Error("invalid RPC page correlation");
          yield { page: rpc.params.page };
        } else {
          checkResponse(rpc, id);
          if (rpc.error) throw new Error(`RPC ${rpc.error.code}: ${rpc.error.message}`);
          yield { result: rpc.result };
          return;
        }
      }
      if (encoder.encode(pending).byteLength > 65536) throw new Error("unfinished RPC frame exceeds bound");
    }
  } finally {
    try { await reader.cancel(); } catch { /* An aborted reader is already closed. */ } finally { reader.releaseLock(); }
  }
}

function checkResponse(value, id) {
  if (value?.jsonrpc !== "2.0" || value.id !== id || ("result" in value) === ("error" in value)) throw new Error("invalid RPC response correlation");
}

async function boundedText(body, limit) {
  if (!body) throw new Error("missing RPC body");
  const reader = body.getReader(), decoder = new TextDecoder("utf-8", { fatal: true });
  let bytes = 0, text = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) return text + decoder.decode();
      if ((bytes += value.byteLength) > limit) throw new Error("RPC response exceeds bound");
      text += decoder.decode(value, { stream: true });
    }
  } finally {
    try { await reader.cancel(); } finally { reader.releaseLock(); }
  }
}
