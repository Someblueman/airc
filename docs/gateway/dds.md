# DDS signed-bundle ingress

The chosen provider is Eclipse Cyclone DDS 0.10.5 on Linux AMD64 or ARM64,
using its native C API and generated IDL type. This profile transfers one
signed bundle to a receiver, then uses the existing `airc-gateway import-bundle`
command for authentication and durable admission. It is a one-way Send
connector; DDS acknowledgements do not report AIRC delivery, and this profile
does not define a conversation-read or result topic.

The native helper is in `native/dds`. `scripts/dds-bundle.py` supervises it,
pins the executable by digest and publishes received bytes to a private file
only after the native process exits successfully. Keep `gateway_device_io.py`
and `gateway_native_io.py` beside that script. Python 3.10+ is required.
The gateway itself needs no new library or listener contract.

## Provisioning and use

Provision the exact Cyclone DDS 0.10.5 development package, IDL compiler,
runtime libraries and a C11 compiler on each Linux endpoint through the
operator's trusted build process. The build requires the exact package version;
it does not download dependencies. Build the native helper:

```sh
cmake -S native/dds -B /absolute/private/dds-build \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH=/absolute/cyclonedds-prefix
cmake --build /absolute/private/dds-build
```

Record the built `airc-dds` executable's SHA-256 from trusted provisioning.
The script requires an absolute canonical path without symlinks, a regular ELF
file no larger than 16 MiB, root/current-user ownership, executable permission,
and no set-ID or group/other write bits. It hashes the open descriptor and
executes that same descriptor after checking file identity and metadata.
Keep the binary immutable while it runs. The digest pins the executable;
the system loader, dynamic libraries and their search paths remain trusted
deployment inputs. DDS execution receives a clean environment without
`CYCLONEDDS_URI` or loader overrides.

Choose an isolated DDS domain from 0 through 230 and each endpoint's actual
local IPv4 interface address. Supply the other endpoint's numeric IPv4 address
as its discovery peer; the two addresses must be distinct. Start the receiver first:

```sh
python3 scripts/dds-bundle.py read \
  --helper /absolute/dds-build/airc-dds --sha256 EXPECTED_SHA256 \
  --domain 42 --local 192.0.2.10 --peer 192.0.2.11 \
  --output /absolute/private/received-bundle.json
```

On the sender, reverse the local/peer addresses and transmit the original
signed-bundle file:

```sh
python3 scripts/dds-bundle.py emit \
  --helper /absolute/dds-build/airc-dds --sha256 EXPECTED_SHA256 \
  --domain 42 --local 192.0.2.11 --peer 192.0.2.10 \
  --input /absolute/path/signed-bundle.json
```

Then authenticate and import at the receiver:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Use exactly one enabled `offline_bundle` listener, with the sender keys, native
bindings and explicit Send grants described in [offline-bundle.md](offline-bundle.md).
The received file is fresh mode 0600 in a current-user-owned mode-0700
directory. Existing output files are refused. Input must be a bounded regular
file; final-component symlinks are refused. Use the public `read` and `emit`
commands; underscore-prefixed child commands are supervisor plumbing.

## Wire, identity and resource boundaries

The fixed topic is `AIRC_SignedBundle_v1`, partition `AIRC/gateway/v1`, with
non-keyed final IDL type `AIRC::SignedBundleV1`. It contains an unsigned
32-bit `length` and a fixed 32,768-octet `payload`. Length must be 1–32,768;
the unused array suffix must contain only zero bytes. The payload contains
the existing signed-bundle JSON unchanged. DDS serialization handles the
IDL representation; no custom C struct layout is sent directly on the wire.

Both endpoints use reliable delivery, volatile durability, KEEP_LAST depth
one and one-sample/one-instance resource limits. The emitter waits for a
matching reader, writes once and waits for DDS acknowledgements. The receiver
takes one valid sample into caller-owned fixed storage, validates its length
and padding, deletes its DDS entities, then returns the exact payload bytes.
There is no retained backlog, automatic retry or partial-bundle admission.
Start the receiver for each transfer. Keep the original signed intent when
retrying an uncertain transfer; do not regenerate its ID or original validity.

Configuration selects UDP/IPv4, one explicit local interface, multicast
disabled and one explicit discovery peer. Domain, interface, peer, partition,
topic and DDS source metadata are routing information, not sender credentials.
The discovery peer is not a source allowlist: DDS participants can advertise
other endpoint locators. Provision network isolation/access rules for the DDS
network separately. This profile does not enable DDS Security or encrypt the
bundle. An untrusted participant can inject, observe or disrupt carrier traffic;
it cannot acquire native authority from a DDS match or acknowledgement.

Only the importer verifies the envelopes' signatures, current principal and
target grants, original freshness and durable replay identity. All signatures
are checked before journal admission. A received file or successful emitter
exit is not an accepted Send. Inspect the import report and native receipt;
use an independent AIRC observer during later acceptance.

The Python supervisor bounds each child to 30 seconds, including pinning,
discovery, transfer and cleanup, with termination/kill and process reaping on
timeout or interruption. The native helper additionally bounds address space
to 512 MiB, descriptors to 64, core dumps to zero and lifetime with an alarm.
The process limit covers middleware/discovery allocations as well as the fixed
application sample. Oversized stdout, invalid samples, nonzero exit or interrupted
cleanup cannot publish a received file. The helper installs no library, service,
firewall rule or network configuration.

## Deferred verification

Python source compilation and source review are available. The exact Cyclone
DDS development installation is not present locally, so native compilation,
IDL generation and linked-library compatibility remain pending. No DDS
participant, network exchange, importer or native AIRC delivery was executed.

Later acceptance needs paired provisioned endpoints: real discovery and
reliability, type/QoS compatibility, loss/replacement/late-reader behavior,
size/padding refusals, binary pinning, signature/grant/freshness/replay checks,
resource exhaustion and joined shutdown. Verify native author, target and body
independently. Other broker qualifications do not establish DDS support.

Primary API references are the chosen release's [C entity/data API](https://github.com/eclipse-cyclonedds/cyclonedds/blob/0.10.5/src/core/ddsc/include/dds/dds.h),
[QoS setters](https://github.com/eclipse-cyclonedds/cyclonedds/blob/0.10.5/src/core/ddsc/include/dds/ddsc/dds_public_qos.h)
and [IDL/CMake integration example](https://github.com/eclipse-cyclonedds/cyclonedds/blob/0.10.5/examples/helloworld/CMakeLists.txt).
