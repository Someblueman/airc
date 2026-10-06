# Slow-reader verification

## gRPC flow-control witness

Before source: `1499615`. The existing real TLS gRPC client still receives the
initial page, pauses consumption and generates 40 native messages. A test-only
credential wrapper observes decrypted HTTP/2 frames, verifies the advertised
static 65,535-byte stream window, counts outgoing stream replenishment and
subtracts actual incoming DATA (including flow-controlled padding). It requires
zero remaining credit while part of a gRPC message is still pending: observed
flow-control pressure, rather than inference from a message count. Replenishment
is accounted before forwarding to avoid overstating pressure from scheduling.
Framing state is protected across concurrent reads/writes. Production code,
TLS verification and SDK behavior are unchanged.

Normal repetitions observed 3,911, 3,911 and 3,912 message bytes pending at
exhaustion. The independent native observer must return to baseline at least two
seconds after pressure, within the original two-to-eight-second generation
bound, while the client context remains active. The ordinary native read lease
and client cancellation cannot satisfy those bounds. The fixed message flood
remains unchanged in this checkpoint; reducing it is a separate roadmap item.

Focused normal count-three parent seconds before: 6.71, 3.09, 3.11; after:
6.33, 3.11, 3.12. Package seconds: 13.422/13.250; command wall: 15.22/16.55.
First samples include builds. Timing did not overlap validation; stronger
coverage shows no warm runtime improvement. Focused race count-three and
integration package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/grpc-pressure-*`, including the initial witness run.
These results verify this carrier witness, not whole-suite acceptance or speed.

## gRPC bounded pressure seeding

Before source: `d4c2700`. Stop seeding at the observed pressure event, with the
existing 40-request ceiling retained as a failure guard. Each new post now waits
for its matching native receipt. Pressure arriving during a send can allow at
most one extra acknowledged post. Partial-message pressure, active client context
and original native cleanup bounds remain required.

Normal count-three runs used 18, 17 and 18 acknowledged posts, replacing the fixed
40 submitted requests. Pending message bytes were 3,912, 3,911 and 3,910. This
reduces setup submissions; the earlier test did not await all 40 native receipts,
so it does not establish a comparison of accepted counts. Reused still-valid
prior witness timings are 6.33, 3.11, 3.12 parent seconds; bounded seeding:
7.21, 3.13, 3.16. Package seconds: 13.250/14.078; command wall: 16.55/18.40.
First samples include builds; timing did not overlap validation. Warm duration
shows no improvement because the production write bound remains. Focused race
count-three and integration package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/grpc-pressure-bounded*`; prior measurements remain
in `grpc-pressure-after.*`. No whole-suite speedup is claimed.

## HTTP/3 peer pressure witness

Before source: `8cd90e8`. Keep the real TLS HTTP/3 client and existing 65,536-byte
stream/connection receive windows. A test-only QUIC trace records received peer
`DATA_BLOCKED` or request-stream `STREAM_DATA_BLOCKED` frames. This fixture has
one application request stream; connection pressure also includes its small
HTTP/3 control streams and is labelled separately from request-stream pressure.
Normal repetitions observed connection blocking at 65,536 bytes.

The blocked frame must arrive after generation starts. Native observers must
return to baseline at least two seconds after that event, inside the original
two-to-eight-second generation bound, while client cancellation remains inactive.
The fixed 40-request flood, production write deadline, ordinary native lease,
TLS verification and SDK behavior remain unchanged in this checkpoint. The
trace uses a concurrency-safe nonblocking channel and writes no qlog artifact.

Focused normal count-three parent seconds before: 6.61, 3.11, 3.13; after:
6.52, 3.12, 3.11. Package seconds: 13.274/13.134; command wall: 15.51/15.87.
First samples include builds; timing did not overlap validation. Warm runtime
shows no improvement. Focused race count-three and integration package vet pass.
Raw evidence: `/tmp/airc-roadmap-baseline/http3-pressure-*`, including the initial
witness run. Message reduction remains a separate roadmap item. No whole-suite
speedup or final acceptance is claimed.

## HTTP/3 bounded pressure seeding

Before source: `ccb8f55`. Stop seeding when the peer-pressure event arrives, with
the original 40-request ceiling. Await each matching accepted native receipt;
an event racing one send can permit one extra acknowledged post. The peer
pressure, active client context and original cleanup bounds remain required.

Normal count-three runs needed 17, 16 and 16 accepted native posts, replacing
40 submitted requests. The earlier test did not await all receipts, so this is
a setup-submission reduction rather than a comparison of accepted counts. All
three runs observed connection blocking at 65,536 bytes. Prior witness parent
seconds: 6.52, 3.12, 3.11; bounded seeding: 6.37, 3.09, 3.12. Package seconds:
13.134/13.085; command wall: 15.87/17.30. First samples include builds; timing
did not overlap validation. Warm duration shows no improvement. Focused race
count-three passed in 18.194 package seconds; integration package vet passed.
Raw evidence: `/tmp/airc-roadmap-baseline/http3-pressure-bounded*`. No
whole-suite speedup or final acceptance is claimed.

## Raw QUIC peer pressure witness

Before source: `f4dfc09`. Keep the fixed 40-request flood, real TLS raw QUIC
client and existing 65,536-byte stream/connection receive windows. Assert the
request uses client stream zero; the shared test trace records received peer
`DATA_BLOCKED` or request-stream `STREAM_DATA_BLOCKED`. Three normal runs
observed one request-stream and two connection pressure events at 65,536 bytes.

The pressure event must follow generation. Native observers must return to
baseline at least two seconds after pressure, within the original two-to-eight
second generation bound, while the client connection stays active. No stream
reads, cancellation, production deadline or native lease changes are introduced.

Normal count-three parent seconds before: 5.71, 3.15, 3.14; after: 5.63, 3.15,
3.15. Package seconds: 12.432/12.430; command wall: 13.58/16.03. First samples
include builds; timing did not overlap validation. Warm runtime shows no
improvement. Focused race count-three passed in 17.281 package seconds;
integration package vet passed. Raw evidence:
`/tmp/airc-roadmap-baseline/raw-quic-pressure-*`.
Flood reduction remains a separate item; final acceptance remains outstanding.

## Raw QUIC bounded pressure seeding

Before source: `8773da8`. Stop seeding on the peer-pressure event, with the
original 40-request ceiling. Each post now awaits its matching accepted native
receipt. An event racing a send can permit one extra acknowledged post. Pressure,
active connection and both original cleanup bounds remain mandatory.

All three normal runs needed 16 accepted native posts, replacing 40 submitted
requests. The earlier test did not await all receipts, so this compares setup
submissions rather than accepted counts. Two runs observed stream pressure and
one connection pressure, all at 65,536 bytes. Prior witness parent seconds:
5.63, 3.15, 3.15; bounded seeding: 5.31, 3.16, 3.14. Package seconds:
12.430/12.123; command wall: 16.03/15.52. First samples include builds; timing
did not overlap validation. Warm runtime shows no improvement. Focused race
count-three passed in 17.238 package seconds; integration package vet passed.
Raw evidence: `/tmp/airc-roadmap-baseline/raw-quic-pressure-bounded*`. Final acceptance remains
outstanding; no whole-suite speedup is claimed.

## WebTransport peer pressure witness

Before source: `81761e9`. Keep the fixed 40-request flood, real TLS WebTransport
client and 65,536-byte stream/connection receive windows. CONNECT uses stream
zero; assert the actual first application stream is four and configure the trace
for it before dialing. Stream-zero trace consumers retain their defaults.
All three normal runs observed peer connection `DATA_BLOCKED` at 65,536 bytes;
this includes HTTP/3 control and CONNECT traffic, not just the application stream.

Pressure must follow generation. Native observers must return to baseline at
least two seconds after pressure, within the original two-to-eight-second
generation bound, while the session remains active. The existing ten-second
read deadline, unread response, production write bound and native lease remain.

Normal count-three parent seconds before: 5.01, 3.11, 3.09; after: 5.52, 3.12,
3.10. Package seconds: 11.654/12.223; command wall: 12.89/15.84. First samples
include builds; timing did not overlap validation. Warm runtime shows no
improvement. Race count-three for WebTransport plus both shared-trace consumers
(HTTP/3 and raw QUIC) passed in 43.847 package seconds; integration package vet
passed. Raw evidence: `/tmp/airc-roadmap-baseline/webtransport-pressure-*`.
Flood reduction and final acceptance remain outstanding.

## WebTransport bounded pressure seeding

Before source: `fbb623e`. Stop seeding on the peer-pressure event, retaining the
40-request ceiling. Await each matching accepted native receipt. An event
racing a send can permit one extra acknowledged post. Pressure, active session
and both existing cleanup bounds remain required.

Normal runs needed 16, 17 and 16 accepted native posts, replacing 40 submitted
requests. The earlier test did not await all receipts, so this compares setup
submissions rather than accepted counts. All runs observed connection blocking
at 65,536 bytes. Prior witness parent seconds: 5.52, 3.12, 3.10; bounded
seeding: 5.68, 3.11, 3.11. Package seconds: 12.223/12.401; command wall:
15.84/15.86. First samples include builds; timing did not overlap validation.
Warm runtime shows no improvement. Focused race count-three passed in 17.771
package seconds; integration package vet passed. Raw evidence:
`/tmp/airc-roadmap-baseline/webtransport-pressure-bounded*`. No whole-suite
speedup or final acceptance is claimed.
