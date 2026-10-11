# Gateway gRPC contract

`gateway.proto` is the versioned cross-process contract. Generated clients call
`Call`, `Capabilities`, and the server-streaming `Watch` method. Without
bearer metadata the envelope is a plain contract request naming its author
(over TLS or cleartext HTTP/2) and results are the plain DetachedResult JSON;
with bearer metadata it is the strict gateway v1 JSON schema; durable request IDs
live inside that envelope. gRPC call correlation does not replace those IDs.

Generated source is checked in. This revision was generated with `protoc 33.4`,
`protoc-gen-go v1.36.12` and `protoc-gen-go-grpc v1.6.2`. Regenerate from the
repository root with the plugins in a task-owned directory:

```sh
mkdir -p .tmp/grpc-tools
# Use the actual Go executable; some version-manager shims override GOBIN.
GOROOT=/absolute/path/to/go-root GOTOOLCHAIN=local GOBIN="$PWD/.tmp/grpc-tools" /absolute/path/to/go-root/bin/go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOROOT=/absolute/path/to/go-root GOTOOLCHAIN=local GOBIN="$PWD/.tmp/grpc-tools" /absolute/path/to/go-root/bin/go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
protoc \
  --plugin=protoc-gen-go="$PWD/.tmp/grpc-tools/protoc-gen-go" \
  --plugin=protoc-gen-go-grpc="$PWD/.tmp/grpc-tools/protoc-gen-go-grpc" \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  api/gateway/v1/gateway.proto
```

The adapter rejects unknown protobuf request fields and validates the embedded
JSON independently. A normal watch frame has one page; a terminal frame has a
reason and the last delivered page or a typed failure. No new replay or receipt
semantics are implied by this carrier.
