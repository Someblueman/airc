# Credential and key fixture setup measurement

At source `363d41d`, a temporary test measured existing fixture helpers
serially, using their actual random generation, encoding, file creation,
credential sync and validation paths. It was compiled once, run with
`-test.run=^TestMeasureFixtureSetup$ -test.v -test.count=1 -test.timeout=120s`
from `integration/gateway`, and then removed. No production or permanent test
source changed. No competing validation or benchmark ran.

| Helper | Samples | Median setup milliseconds | Range milliseconds |
| --- | --- | --- | --- |
| `acceptanceJournalFixture` credential/identity setup | 50 | 4.346834 | 2.383500–7.320208 |
| `provisionDetached` signing/recipient setup | 50 | 0.726749 | 0.459417–2.683875 |
| `testtls.New` Ed25519 TLS material | 50 | 0.989917 | 0.370834–3.000458 |
| `testtls.NewP256` P-256 TLS material | 50 | 0.555125 | 0.395167–1.329709 |
| `gatewayHTTPFixture` HTTP/2 setup | 8 | 43.550500 | 31.263833–1220.802750 |

Each sample owns a fresh test directory. Detached-key timing excludes its
prerequisite credential/identity setup. The credential helper creates the
state directory but does not open a store. TLS timings include generation,
certificate serialization, protected files, parsing and trust configuration.
HTTP timing includes the native daemon and gateway setup; its first sample
includes pinned executable compilation. Timers stop when helpers return,
so cleanup is excluded. This is setup-cost evidence, not end-to-end test time.

Keys and certificates take around one millisecond per fixture; credential
setup takes a few milliseconds and preserves tested private-file and fresh
identity boundaries. These results do not justify caching or sharing secret
material to address the long integration suite. Fresh material remains in
place; no secure-file, random generation or sync checks were weakened.

All samples and fixture cleanups passed with terminal exit 0. Raw evidence
is retained under `/tmp/airc-roadmap-baseline`: `fixture-setup.test`,
`fixture_setup_measurement_test.go`, `fixture-setup.out`,
`fixture-setup.stderr`, and `fixture-setup-samples.json`. Diff checks passed
after the temporary source was removed. The measurement roadmap item is
complete; fresh final acceptance remains outstanding.
