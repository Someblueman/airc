# Framed stdin/stdout pipes

Plain author requests: a frame with `"profile": "plain"` and a `request` naming
its author runs without any principal or envelope; the parent process owning
the pipe is the access gate. See [local authors](local-authors.md).
Signed/encrypted frames below are the older form.

Catalogue row 31 uses the same version-1 four-byte big-endian framing and request/result envelopes as [framed streams](framed-stream-v1.md). Start a dedicated child process with:

```sh
airc-gateway serve-stdio --file /absolute/path/gateway.json
```

Configure exactly one enabled listener with `protocol: "stdio"`, an empty address, and an explicit operations allowlist. No TLS or origin settings apply. Ordinary `serve` rejects this listener before opening the journal. Both stdin and stdout must be pipes: terminal and regular-file redirection are rejected before journal creation. Diagnostics go to stderr; stdout contains frames only, with no readiness record. The parent must read stdout while writing stdin to avoid pipe backpressure.

Every frame requires a provisioned signed or encrypted envelope. Inherited descriptors do not authenticate a principal. Responses are clear on the dedicated local parent/child boundary, including responses to encrypted ingress; this profile does not protect against a parent or process that can read the pipe. Never bind an identity merely because a writer can access a shared FIFO. Opening a pipe descriptor is not a verified named-FIFO deployment recipe.

The adapter exposes the current shared fourteen operations: send, reply, reaction.add, context.read, pins.read, room.read, waiting.read, poll.results, topic.read, receipt.get, history.read, search, watch, and directory. Authenticated capabilities.get reports the configured intersection and framed limits. Room message watches use correlated page/end frames and the existing bounded lease/resume cursor. DM reads and native nonmessage live events remain pending across the shared backend.

Request frames are bounded at 16 KiB, response frames at 64 KiB, with four active correlations per connection and shared global request capacity. The initial header byte has a five-second deadline; later idle frames allow thirty seconds. After the first header byte, the remaining header/body has five seconds. Writes have three seconds. Ordinary admitted handlers are bounded at four seconds, and stream handlers at twenty seconds with existing page/byte limits. Duplicate active correlations and malformed/oversized framing close the stream.

A clean EOF between frames stops ingress and drains admitted handlers before closing stdout. A partial header/body, failed deadline, write error or cancellation cancels active handlers. After clean EOF, the service allows at most five seconds for unresolved durable deliveries while keeping the runner alive; empty journals exit immediately. This grace is not a delivery guarantee. Remaining requests retain stable IDs, retry counters and uncertainty for restart. Signal cancellation interrupts the grace and joins workers; observed outcomes retain the worker's existing bounded persistence window.

Darwin and Linux use owned close-on-exec duplicates above the standard descriptor range. The duplicates use Go's poller for deadlines and interruptible close. Closing them does not close the inherited descriptors, but nonblocking flags are shared through the open file description; descriptors must be dedicated to this child session. Other platforms explicitly reject enabled stdio rather than silently falling back to files or an unbounded reader.

Runtime conformance and platform evidence are recorded in [STATUS.md](STATUS.md). This is a partial adapter: the remaining catalogue evidence is tracked in [ROADMAP.md](ROADMAP.md); additional native operations are optional.
