# Inherited connected Unix-stream descriptors

Catalogue row 34 also includes inherited descriptors. This connector's concrete
contract is a dedicated, already connected AF_UNIX SOCK_STREAM endpoint passed
by the parent. Named Unix connections and unnamed socket pairs are accepted;
TCP, datagram sockets, pipes, files and unconnected Unix sockets are rejected.
The adapter opens no socket path and establishes no connection itself.

`OpenInheritedUnixStream` validates socket type and both Unix endpoints, then
returns an owned close-on-exec duplicate attached to Go's network poller. Reads
and writes support deadlines. Closing the duplicate preserves the source
handle; the caller must close its inherited copy when ownership transfers.
Nonblocking flags may be shared through the open file description, so the parent
must dedicate that endpoint to the child without concurrent reading or writing.

Descriptor possession defines the local channel boundary, not an AIRC identity.
Service activation uses existing signed/encrypted frame authentication,
bounded requests and joined delivery/journal ownership. Clear responses on this
local channel can be read by processes that can access the endpoint. The parent
owns connection establishment and any socket-path permissions. Arbitrary files,
network endpoints and general-purpose FD forwarding are outside this contract.

Darwin arm64 adapter controls use a real named Unix connection for bidirectional
I/O, deadline expiry and duplicate ownership. Shared descriptor controls exercise
both inherited-stream and strict socket-pair entry points with nil/file/pipe/TCP/
datagram refusals; an additional unconnected Unix endpoint is refused. The
existing socket-pair named-connection refusal remains intact. Race count-three,
package vet and staticcheck pass. These descriptor controls alone do not establish native delivery.
Darwin and Linux native service qualification is recorded below; remaining
work lives in [ROADMAP.md](ROADMAP.md).


Activate the service with `airc-gateway serve-inherited --file CONFIG`. The
parent passes the connected endpoint as descriptor 3 (the first
`exec.Cmd.ExtraFiles` entry). Configure exactly one enabled listener with
`protocol: "inherited"`, empty address and an explicit operations allowlist.
TLS and origin options are rejected. Ordinary `serve` refuses this listener
before journal startup. The gateway owns its inherited source descriptor and
its pollable duplicate until exit. The parent owns its other endpoint and must
read responses while writing requests; no readiness or frames go to stdout.
Diagnostics go to stderr.

This mode shares authenticated framing and joined workers/journal with stdio
and socket pairs. Clean EOF drains admitted handlers and retains the bounded
five-second durable-delivery grace. Partial framing, transport failures and
signal cancellation cancel and join work. Size, correlation and time bounds
remain those in [stdio.md](stdio.md) and [framed-stream-v1.md](framed-stream-v1.md).

Actual Darwin arm64 Go 1.27.1 CLI activation/refusal controls passed under race
detection with socket-pair and stdio startup boundaries (7.985 seconds). The
child executable SHA-256 was
`4098b8428d85e3c2da8bf63724fd352ecc5acef21e52d6b5997d766a4365cc77`.
A parent-established named Unix endpoint was inherited, invalid authentication
received its correlated refusal, and clean EOF exited successfully with no
stdout and released journal ownership for a fresh owner. Missing/regular FD 3
and ordinary-command startup were rejected without journal creation. Full
affected configuration/service/CLI race suites, vet, staticcheck and command
builds also passed. Those activation controls do not themselves prove native delivery.


At executable source `36c4894`, signed/encrypted named inherited-Unix-stream
native acceptance passed under race detection. The actual child executable
SHA-256 was
`cbcfb63b9419cd92fd5bf854b62361b0e5be31eca5ed275600cba5ec5d337e62`.
The checksum-verified daemon baseline was
`96f16c3bbb8a3faac2e69684f591af63ceb58450`, archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
The parent established a named Unix connection; the client endpoint retained a
nonempty remote Unix address, and the accepted endpoint was passed as FD 3.

Each profile refused invalid authentication before healthy native sends. An
independent observer checked exact author, room, Unicode/newline body and
accepted message ID for two sends. Same-session envelope replay retained the
message IDs and one send attempt. History limit-one pages returned both records
with exact ID, author, target and body, explicit `more` then `ok`, nonempty
cursors and no gap; a final cursor returned an empty caught-up page. Signed
SIGINT and encrypted SIGTERM shutdown joined within three seconds, emitted no
stdout and released journal ownership. Those controls exercise same-session replay and clean signals; they do not
cover framing failures or admitted-intent restart/termination recovery.

The named inherited-stream controls and all six socket-pair controls passed
together in 31.190 seconds after replacing their owned parent-FD fixture in
place. Package vet and staticcheck passed. Separate tests and actual endpoint
creation preserve the distinction between named inheritance and socket pairs.


At source `a0b374e`, all six named inherited-stream CLI/native controls and all
six socket-pair controls passed together under race detection (50.965 seconds).
The actual child executable SHA-256 was
`8e5dcb2422e8c8a5014d8583641c01189290e9e4e8743c0d1f4a4a9fe81d0c24`.
Vet and staticcheck passed. The shared failure/replay/recovery controls each
create separate actual named or socket-pair endpoints and child processes;
one mode's result is not substituted for the other.

Partial headers, partial valid signed bodies, malformed JSON, zero length and
16,385-byte declared bodies each closed without response data, exited with an
error within three seconds and left zero jobs across all principals. Fresh
named-endpoint children using the same journal then completed encrypted native
delivery and cursor reads. An accepted signed intent also survived joined
shutdown and fresh-child encrypted replay with unchanged upstream ID, message
ID and one send attempt; independent native history contained exactly one record.

Pending controls held an owned upstream connection after durable signed intake
and verified zero native records before SIGKILL, SIGINT or SIGTERM. Actual
SIGKILL wait status and successful joined signal exits were checked. Fresh
journal ownership found the same unaccepted durable intent and nonempty upstream
ID. After new relay connections could forward, a fresh named-endpoint child
replayed the intent with an encrypted envelope, retained the upstream ID and
reached acceptance. An independent observer and native history query verified
exactly one matching author, room, message body and accepted ID. This covers
pending termination before native acceptance; lost native acknowledgement and
power failure are not simulated. Linux runtime was still open at this Darwin
checkpoint; its separate acceptance is recorded below.


## Linux descriptor checkpoint

Source `8f1f9c30f95885bc2db9cd92a2648d49af483e1f` was cross-compiled with
installed Go 1.27.1 using `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c
-p=1 ./internal/gateway/adapter`. The resulting test executable ran in the
already-installed Ubuntu 24.04 ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`
on Docker 29.7.2 Linux aarch64, with no network, a read-only root and disposable
`/tmp`. No image or toolchain was downloaded.

The combined `^(TestFIFO|TestSocketPair|TestInheritedUnix)` selection exited 1:
all FIFO and inherited Unix-stream tests passed, but
`TestSocketPairStreamOwnershipAndDeadlines` rejected a real unnamed socket pair
with `socket pair requires connected unnamed Unix endpoints`. Socket-pair
refusal tests passed. The captured output is
`/tmp/airc-gateway-linux-adapter-8f1f9c3.log`.

This is actual Linux descriptor evidence without race instrumentation. It
does not qualify authenticated native AIRC delivery, complete cursor reads,
or durable restart acceptance on Linux. Later inherited-stream native
acceptance is recorded below; other modes retain their own boundaries.


## Linux inherited-stream native acceptance

The dedicated, parent-established named Unix-stream endpoint passed as child
descriptor 3 was separately qualified on Ubuntu 24.04 ARM64, Docker 29.7.2
and Linux `7.0.12-linuxkit`. The run reused the checksum-pinned native daemon
and digest-verified cross-compiled executables from the
[Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery),
with unchanged production source `159afc40c4e7607fe871443002cd39596f3fb41e`
and integration Go/module source digest
`2f3e17077b032078fb140be5ab8678bdac236910bdada0e6da80cf13c0244d01`.
Artifact digests were rechecked before execution. The container retained no
network, a read-only root/artifact mount and an automatically removed anonymous
local-filesystem volume for journal state.

`-test.run '^TestInheritedUnix' -test.v -test.count=1 -test.timeout=180s`
completed with terminal exit 0. Six real controls verified:

- Signed/encrypted native sends, invalid authentication refusal, exact whole
  ID/author/room/body, stable same-session replay and complete bounded cursor
  pages through a caught-up boundary (1.10s).
- Authentication refusal followed by clean EOF and released journal ownership,
  plus missing/regular descriptors and ordinary-serve refusal before journal
  creation (0.03s).
- Partial header/body, malformed JSON, oversized and zero-length frames,
  zero admitted jobs and healthy fresh-child native sends/reads (2.31s).
- Durable pending intent through SIGKILL/SIGINT/SIGTERM, bounded child joins,
  retained upstream identity, encrypted fresh-child recovery and exactly one
  native record (1.62s).
- Accepted replay after a fresh child with stable message ID and one durable
  attempt, independently checked against the complete native record (0.26s).

Each child used `inheritedNamedUnixFiles`, not the unnamed socket-pair factory.
Earlier actual Linux adapter controls separately verify named-endpoint
read/write deadlines, duplicate ownership and invalid/unconnected descriptor
refusal. Raw native evidence is
`/tmp/airc-linux-native-159afc4/inherited-unix-native.log`; the shared artifact
manifest is `/tmp/airc-linux-native-159afc4/provenance.json`. This qualifies the
Linux inherited Unix-stream mode on this local fixture without Linux race
instrumentation. It does not qualify arbitrary inherited FD types, lost
acknowledgements or power-loss durability. Final full acceptance remains
in the roadmap.
