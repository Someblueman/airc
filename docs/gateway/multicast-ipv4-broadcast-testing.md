# IPv4 multicast reply broadcast permission

Inspection of Go 1.27.1's Darwin/Linux `net` socket defaults showed that UDP
sockets enable `SO_BROADCAST`. Rejecting only the limited broadcast address in
the reply writer therefore did not enforce the unicast-only contract for
subnet-directed broadcast destinations.

The owned multicast socket now clears `SO_BROADCAST` immediately after creation,
before exposing the writer. Failure closes the socket and fails construction.
Darwin and Linux implement this through the existing native syscall dependency;
other platforms refuse ownership pending a qualified implementation.

`TestIPv4MulticastNativeSocketHasNoBroadcastPermission` reads the option from
the actual owned socket and requires zero. All five multicast socket tests
passed three times on Darwin ARM64 (normal 0.526s, race 1.649s), with vet and
staticcheck passing. Linux ARM64 cross-compilation and vet passed; the same five
tests passed three times in the existing pinned, isolated Ubuntu fixture.
Logs and binary/command provenance are retained in
`/tmp/airc-ipv4-multicast-1cb3069/broadcast.log` and
`broadcast-provenance.json`.

The activation acceptance run started at source hash
`fac3530ab8468666b7f90cb7e87a38459e627c8f011a36ccdafcdfcc58e8dc33`
was deliberately terminated after discovering the defect. Its normal phase
ended with signal 15 after 105.145s; it is not acceptance evidence. The metadata
and raw partial output are retained under
`/tmp/airc-gateway-ipv4-multicast-activation-7fed0e0-20261008T171519`.
Activation still requires fresh full normal/race and static acceptance.
