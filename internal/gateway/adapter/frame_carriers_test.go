//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// frameCarrier carries the shared request frame ({"version",
// "correlation_id","profile","request"}) and answers with a FrameResponse.
type frameCarrier struct {
	name string
	// open returns a deliver function: it carries body (attempt counts
	// deliveries of the same frame) and returns the published response.
	open func(t *testing.T, f plainFixture) func(t *testing.T, body []byte, attempt int) FrameResponse
	// claimed is a mailbox's in-progress claim: put and claim body, then
	// return the processing step, the claim's path and the result's path.
	claimed func(t *testing.T, f plainFixture, body []byte) (process func(context.Context) error, claim, result string)
}

func frameCarriers() []frameCarrier {
	readResponse := func(t *testing.T, path string) FrameResponse {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return response
	}
	write := func(t *testing.T, path string, body []byte) {
		t.Helper()
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return []frameCarrier{
		{
			name: "directory-mailbox",
			open: func(t *testing.T, f plainFixture) func(*testing.T, []byte, int) FrameResponse {
				path, m := directoryMailboxFixture(t)
				return func(t *testing.T, body []byte, _ int) FrameResponse {
					write(t, filepath.Join(path, "inbox", "native-rpc.json"), body)
					if err := ProcessDirectoryMailbox(t.Context(), m, f.d, nil); err != nil {
						t.Fatal(err)
					}
					return readResponse(t, filepath.Join(path, "results", "native-rpc.json"))
				}
			},
			claimed: func(t *testing.T, f plainFixture, body []byte) (func(context.Context) error, string, string) {
				path, m := directoryMailboxFixture(t)
				write(t, filepath.Join(path, "inbox", "native-rpc.json"), body)
				if _, _, err := m.Claim(); err != nil {
					t.Fatal(err)
				}
				return func(ctx context.Context) error { return ProcessDirectoryMailbox(ctx, m, f.d, nil) }, filepath.Join(path, "processing", "native-rpc.json"), filepath.Join(path, "results", "native-rpc.json")
			},
		},
		{
			name: "file-mailbox",
			open: func(t *testing.T, f plainFixture) func(*testing.T, []byte, int) FrameResponse {
				path := mailboxDirectory(t)
				// Each delivery reopens the mailbox, as a restarted owner would.
				return func(t *testing.T, body []byte, _ int) FrameResponse {
					write(t, filepath.Join(path, "request.json"), body)
					m, err := OpenFileMailbox(path)
					if err != nil {
						t.Fatal(err)
					}
					err = ProcessFileMailbox(t.Context(), m, f.d, nil)
					if err := errors.Join(err, m.Close()); err != nil {
						t.Fatal(err)
					}
					return readResponse(t, filepath.Join(path, "response.json"))
				}
			},
			claimed: func(t *testing.T, f plainFixture, body []byte) (func(context.Context) error, string, string) {
				path := mailboxDirectory(t)
				write(t, filepath.Join(path, "request.json"), body)
				m, err := OpenFileMailbox(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = m.Close() })
				if _, err := m.Claim(); err != nil {
					t.Fatal(err)
				}
				return func(ctx context.Context) error { return ProcessFileMailbox(ctx, m, f.d, nil) }, filepath.Join(path, "processing.json"), filepath.Join(path, "response.json")
			},
		},
		{
			name: "append-log-mailbox",
			open: func(t *testing.T, f plainFixture) func(*testing.T, []byte, int) FrameResponse {
				var path string
				var m *AppendLogMailbox
				// Replays are appended records of the same frame.
				return func(t *testing.T, body []byte, attempt int) FrameResponse {
					if attempt == 0 {
						path, m = appendMailboxFixture(t, string(body)+"\n")
					} else {
						file, err := os.OpenFile(filepath.Join(path, "logs", appendLogName(1)), os.O_APPEND|os.O_WRONLY, 0600)
						if err != nil {
							t.Fatal(err)
						}
						_, err = file.Write(append(body, '\n'))
						if err := errors.Join(err, file.Close()); err != nil {
							t.Fatal(err)
						}
					}
					if err := ProcessAppendLogMailbox(t.Context(), m, f.d, nil); err != nil {
						t.Fatal(err)
					}
					return readResponse(t, filepath.Join(path, "response.json"))
				}
			},
			claimed: func(t *testing.T, f plainFixture, body []byte) (func(context.Context) error, string, string) {
				path, m := appendMailboxFixture(t, string(body)+"\n")
				if _, err := m.Claim(); err != nil {
					t.Fatal(err)
				}
				return func(ctx context.Context) error { return ProcessAppendLogMailbox(ctx, m, f.d, nil) }, filepath.Join(path, "claim.json"), filepath.Join(path, "response.json")
			},
		},
		{
			name: "oscore-rpc",
			open: func(t *testing.T, f plainFixture) func(*testing.T, []byte, int) FrameResponse {
				requests := make(chan struct{}, 1)
				return func(t *testing.T, body []byte, _ int) FrameResponse {
					code, response := oscoreRPCRequest(t.Context(), body, f.d, requests, f.ingress())
					want := codes.Content
					if response.Error != nil {
						want = codes.BadRequest
					}
					if code != want || response.Error == nil && response.Sequence != 1 || len(requests) != 0 {
						t.Fatal("CoAP code, sequence or admission slot", code, response)
					}
					return response
				}
			},
		},
	}
}

// TestFrameCarriers exercises the shared request frame once per carrier:
// durable intake whose replay keeps one upstream identity, frame refusals
// answered before intake, and, for mailboxes, a cancelled owner that keeps
// its claim and publishes nothing.
func TestFrameCarriers(t *testing.T) {
	for _, c := range frameCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel() // Each carrier has its own journal and sockets.
			t.Run("intake-and-replay", func(t *testing.T) {
				f, body, _ := oscoreRPCFixture(t)
				deliver := c.open(t, f)
				var upstream string
				for i := range 2 {
					response := deliver(t, body, i)
					if response.Error != nil || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
						t.Fatal("intake failed", response)
					}
					job, err := f.lookup(t, "oscore-rpc-message")
					if err != nil || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 || upstream != "" && upstream != job.UpstreamID {
						t.Fatal("wrong durable intake or replay", job, err)
					}
					upstream = job.UpstreamID
				}
			})
			// Refusals leave the journal untouched, so they share one.
			f, body, _ := oscoreRPCFixture(t)
			for _, kind := range []string{"request", "stream", "profile", "version", "correlation", "malformed"} {
				t.Run("refusal/"+kind, func(t *testing.T) {
					var frame map[string]any
					if err := json.Unmarshal(body, &frame); err != nil {
						t.Fatal(err)
					}
					switch kind {
					case "request":
						frame["request"] = map[string]any{"version": 1}
					case "stream":
						frame["stream"] = true
					case "profile":
						frame["profile"] = "authorSpec"
					case "version":
						frame["version"] = 2
					case "correlation":
						frame["correlation_id"] = ""
					}
					body, err := json.Marshal(frame)
					if err != nil {
						t.Fatal(err)
					}
					if kind == "malformed" {
						body = []byte("{")
					}
					if response := c.open(t, f)(t, body, 0); response.Error == nil || response.Error.Code != core.InvalidRequest || response.Result != nil {
						t.Fatal("wrong refusal", response)
					}
					if _, err := f.lookup(t, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
						t.Fatal("refusal reached durable intake", err)
					}
				})
			}
			if c.claimed != nil {
				t.Run("cancellation-retains-claim", func(t *testing.T) {
					f, body, _ := oscoreRPCFixture(t)
					process, claim, result := c.claimed(t, f, body)
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					if err := process(ctx); !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation hidden", err)
					}
					if _, err := os.Stat(claim); err != nil {
						t.Fatal("cancellation lost claim", err)
					}
					if _, err := os.Stat(result); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("cancellation published result", err)
					}
				})
			}
		})
	}
}
