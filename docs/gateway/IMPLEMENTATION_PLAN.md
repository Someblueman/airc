# Connection gateway scope

Use [ROADMAP.md](ROADMAP.md) as the only active work list. Its minimum is authenticated message submission, with bounded reading prioritized wherever a usable return path exists. Further AIRC operations are optional. Do not implement new AIRC features or require every transport to mirror the native API.

The previous comprehensive plan expanded that scope and is superseded. Its full text remains in Git at `28d255e:docs/gateway/IMPLEMENTATION_PLAN.md`.

Existing wire and durable-data contracts remain documented in [contract-v1.md](contract-v1.md), [configuration.md](configuration.md), [authentication.md](authentication.md), [journal-v1.md](journal-v1.md), and the individual transport specifications. A scope correction does not silently change those contracts or discard stored operations.
