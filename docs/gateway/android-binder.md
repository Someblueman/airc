# Android Binder: bounded app-to-app gateway

The chosen target is Android 10 or later (API 29+), using an ordinary exported
bound service and a companion client app. No root access, hidden Binder service
manager, system application, native daemon or SELinux change is required. The
two APKs communicate using an actual synchronous Binder transaction. The server
forwards the original framed-v1 JSON request to one configured TLS gateway.

Source is in `native/android-binder`. The client offers a profile selector,
correlation field, compact-envelope input and a complete selectable JSON result.
It does not create native identities or signatures. Create the original signed
or encrypted envelope with the existing gateway tools, then paste it unchanged.
Send, receipt recovery and bounded reads retain their existing grants, author,
freshness, replay and journal rules. Streaming Watch is rejected.

## Package and caller admission

The APK IDs are `org.airc.gateway.binder.client` and
`org.airc.gateway.binder.server`. Sign both with the same dedicated operator key:
the server requires `org.airc.gateway.binder.ACCESS`, a signature-level
permission. Each peer also pins the other APK's current signing-certificate
SHA-256 digest. Historical signing certificates are not sufficient. Update
packaged pins and both apps together when changing keys.

The client binds the explicit `org.airc.gateway.binder.GatewayService` component
and verifies its package identity and Binder descriptor. For every exchange,
the server captures `Binder.getCallingUidOrThrow()` before clearing caller
identity, checks the signature permission, and requires that UID to belong to
exactly the named client package with its pinned current signer. Shared UIDs,
multiple APK signers and debuggable peer apps are rejected. Manifest package
visibility declarations make these checks work on newer Android versions.

The Android package/UID boundary admits the local app; the original envelope
establishes the native AIRC author and operations. A forwarded Binder handle or
a caller-supplied UID, package name, intent, target or certificate cannot replace
these checks. The trusted boundary includes the installed peer APKs, operator
signing key, Android package manager/kernel and device administration.

## Provision and build later

Use a provisioned JDK 17, Gradle 8.11.1, Android SDK platform 35 and build tools
35.0.0. Android Gradle Plugin is pinned to 8.9.2; both apps compile/target API 35
and run from API 29. Build tools and dependencies were not downloaded during
implementation. No wrapper binary, test double SDK or generated APK is included.

Copy the `.example` assets to these ignored, operator-owned files before building:

| File under `native/android-binder` | Required contents |
|---|---|
| `client/src/main/assets/client.json` | `server_certificate_sha256`: lowercase 64-character current server certificate digest |
| `server/src/main/assets/server.json` | `address_ipv4`, decimal string `port`, certificate DNS `server_name`, and `client_certificate_sha256` |
| `server/src/main/assets/gateway-ca.pem` | 1–16 trusted X.509 CA certificates, at most 1 MiB |

These assets are part of the signed APK. They contain public trust and routing
configuration, not private native signing keys or bearer credentials. Missing
configuration fails closed. The numeric IPv4 address fixes the destination;
the separate DNS name supplies SNI and certificate hostname verification.
Unspecified, multicast and noncanonical address forms are rejected.

Configure a reachable TLS-enabled framed gateway with the actual native
bindings, verification keys, exact grants, journal and cursor keys. TLS 1.3,
certificate verification against only the packaged CA bundle, HTTPS hostname
verification and ALPN `airc-gateway/1` are mandatory. No cleartext or default
system-root fallback is provided.

From the project directory, later build unsigned release APKs:

```sh
gradle --offline :server:assembleRelease :client:assembleRelease
```

This requires the pinned build dependencies already provisioned in the cache.
Sign the APKs using the existing authorized Android signing/distribution process
and verify their certificate digests against the packaged pins. The release
outputs are `server/build/outputs/apk/release/server-release-unsigned.apk` and
`client/build/outputs/apk/release/client-release-unsigned.apk` before signing.
Install both only on an authorized fixture and launch **AIRC Gateway Client**.
No app installation or signing-key provisioning was performed here.

## Transaction and lifetime bounds

The descriptor is `org.airc.gateway.binder.IBoundedGateway.v1` and the exchange
code is `IBinder.FIRST_CALL_TRANSACTION`. The request Parcel contains the
interface token followed by one `writeByteArray` value. The response contains
the normal Binder exception header followed by one byte array. Arrays carry
the JSON itself; only the subsequent TLS stream adds its four-byte big-endian
length prefix. This is a project-specific Binder contract, not an Android
system service or AIDL compatibility promise.

Only synchronous exchanges (`flags == 0` with a reply Parcel) are admitted.
Request JSON is at most 16 KiB and complete response JSON at most 64 KiB.
Parcels have a bounded allowance for their interface/exception header, prohibit
file descriptors, validate the declared array length before allocation, and
reject trailing data. JSON parsing rejects duplicate members, excessive nesting,
malformed UTF-8, wrong wrapper fields and uncorrelated/nonterminal responses.
The TLS gateway still verifies the original signed/encrypted envelope itself.

The service permits one active upstream socket and queues no submissions. A
three-second connect/handshake read timeout and independent 30-second socket
close deadline bound network I/O, including writes. Service destruction closes
the active socket and stops the deadline scheduler; later calls fail closed.
Android owns the Binder pool threads. Per-message application checks happen
after the kernel receives a Parcel and do not replace Android's Binder limits.

The client permits one outstanding call per app process and displays a deadline
after 40 seconds. Unbinding, activity destruction or thread interruption cannot
cancel an already running synchronous Binder transaction. A late result is
suppressed after the UI deadline, and another call is refused until the prior
worker returns. A destroyed activity does not receive a result. No automatic
retry occurs after reconnect, timeout or process loss.

Inspect the returned terminal event and native result: a completed exchange can
contain an AIRC error and is not proof of accepted Send. If a result is lost,
recover with the original signed mutation and durable request ID; do not refresh
its timestamp, author or identity to retry.

## Deferred verification

Implementation and source review are complete; Android SDK/Gradle compilation,
lint and all device/runtime checks remain deferred. No APK, Binder transaction,
TLS session, app identity refusal or native AIRC request was executed.

Later acceptance must build both APKs with the pinned toolchain and verify
signature permission, current-signer/UID mapping, shared-UID and wrong-signer
refusals, one-way/malformed/oversized Parcels, TLS trust/hostname/ALPN failures,
whole Send/read/receipt results, replay, process/rebinding/rotation boundaries,
deadlines and activity/service destruction. Use an independent AIRC observer
to verify author, target and body.

Primary references: Android [Binder caller identity](https://developer.android.com/reference/android/os/Binder),
[Parcel API](https://developer.android.com/reference/android/os/Parcel),
[service permissions](https://developer.android.com/guide/topics/manifest/service-element),
[package visibility](https://developer.android.com/training/package-visibility/declaring),
[APK signing information](https://developer.android.com/reference/android/content/pm/SigningInfo),
[TLS socket API](https://developer.android.com/reference/javax/net/ssl/SSLSocket)
and [AGP 8.9 toolchain compatibility](https://developer.android.com/build/releases/agp-8-9-0-release-notes).
