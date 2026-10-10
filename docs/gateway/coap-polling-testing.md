# CoAPS acceptance polling budget

The real carrier matrix previously polled receipts every 40 milliseconds.
CoAPS retains at most 256 distinct request tokens for an association's lifetime.
Two diagnostic matrix repetitions reproduced an association close exactly at
request 257, both during retract receipt lookup. Raw diagnostic output is in
`/tmp/airc-roadmap-baseline/send-coaps-count.out`.

The acceptance fixture now polls every 100 milliseconds, preserving its existing
eight-second acceptance deadline, actual receipt checks and all production
limits. This limits avoidable lookup traffic while asynchronous native jobs run.
Capacity rejection remains independently exercised by the adapter tests.

Two normal real CoAPS matrices passed together in 59.663 seconds, and the
race matrix passed in 36.738 seconds. These overlapping validation runs are
correctness evidence, not comparable timing samples. The CoAPS adapter tests
passed in 0.552 seconds, and `go vet ./integration/gateway` passed.
