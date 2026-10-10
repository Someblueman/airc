# MCP Streamable HTTP acceptance evidence

The integration fixture starts a standalone gateway process and connects the
official Go MCP SDK through `StreamableClientTransport` over HTTPS. The SDK
client runs outside the gateway process; this is a real network transport on
loopback, not an in-process MCP transport or external-host deployment.

`TestMCPHTTPListsAuthenticatedGatewayToolsAndSends` requires exactly the reduced
send/read/wait/status tool interface, scoped discovery and durable send acceptance
checked against an independent native observer. `TestMCPHTTPReadsCompleteBoundedPages`
submits three native-observed multiline Unicode messages, reads two bounded
pages and verifies exact IDs, bodies, authors and targets. Saved-cursor resume
must return an empty complete page. The wait tool reads retained records and
rejects a cursor borrowed from the read tool.

The MCP HTTP group also covers HTTP/1.1 and HTTP/2 negotiation, reconnect replay
without a native duplicate, unauthenticated denial, cross-principal session
takeover rejection, progress delivery, cancellation and gateway shutdown.
The group passed under race detection (terminal exit 0, 34.711 seconds):

```sh
go test -race ./integration/gateway -run '^TestMCPHTTP' -count=1 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/mcp-http-qualification-race.out` and
`mcp-http-qualification-vet.out`. This qualifies the gateway's reduced interface
with a real SDK client on Darwin. The separate local `airc mcp` tool inventory
and owner decisions remain open. Fresh final repository acceptance, including
the incomplete full race gate, remains outstanding.
