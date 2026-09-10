# Make the llmbridge plugin crash-resilient

## Context

After the gateway runs for a while, all cross-format conversions start failing with:

```
rpc error: code = Unavailable desc = connection error: desc = "transport: error while dialing: dial unix /tmp/plugin1660798163: connect: connection refused"
```

On a unix socket, `connection refused` (ECONNREFUSED), as opposed to `no such file` (ENOENT), means the socket file still exists but **nothing is listening** — the llmbridge plugin subprocess has exited. Two independent defects combine:

1. **No recovery (makes it permanent).** The plugin is spawned exactly once at boot (`llmbridge.New` → `newPluginBridge`, `pkg/llmbridge/plugin_client.go:22`). The resulting `pluginBridge` holds a single long-lived `*plugin.Client` + gRPC conn for the whole server lifetime. There is no health check, `Exited()` check, or restart, so once the subprocess dies every later `BridgeRequest`/`BridgeStream`/`BridgeNonStream`/`AggregateStream` call fails forever until the gateway restarts.

2. **No panic guard (the likely trigger).** `cmd/picotera-llmbridge-plugin/main.go:21` uses `plugin.DefaultGRPCServer` — a bare `grpc.NewServer()` with no interceptors. grpc-go does not recover handler panics by default, so a single payload that trips a nil-deref / index-out-of-range inside an `llmbridgeimpl` (axonhub) transformer crashes the **entire** plugin process, not just that request. This matches "works a while, then breaks": fine until the first panic-triggering payload.

3. **Silent crash.** go-plugin's `ClientConfig.Stderr` defaults to `io.Discard` (go-plugin `client.go:402`); the plugin's stderr (including panic stack traces) is thrown away, which is why the crash is invisible in the logs today.

Outcome: a single bad conversion fails that one request instead of killing the process; if the process dies for any reason, the next request transparently respawns it; and the crash cause is captured in the gateway logs.

## Approach (defense in depth)

### 1. Plugin side — panic recovery interceptor (`cmd/picotera-llmbridge-plugin/main.go`)

Replace `GRPCServer: plugin.DefaultGRPCServer` with a custom factory that installs handwritten unary + stream recovery interceptors (no new dependency — grpc v1.81.1 already provides `grpc.ChainUnaryInterceptor` / `grpc.ChainStreamInterceptor`, confirmed):

```go
GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
    opts = append(opts,
        grpc.ChainUnaryInterceptor(recoverUnary),
        grpc.ChainStreamInterceptor(recoverStream),
    )
    return grpc.NewServer(opts...)
},
```

Each interceptor `defer`s a `recover()` that:
- writes the panic value + `debug.Stack()` to `os.Stderr` (so it is captured by the host's new Stderr writer, step 3), and
- converts the panic into `status.Errorf(codes.Internal, "llmbridge: panic: %v", r)` so the call fails cleanly and the process survives.

### 2. Host side — self-healing `pluginBridge` (`pkg/llmbridge/plugin_client.go`)

- Factor the spawn sequence (`plugin.NewClient` → `Client()` → `Dispense` → `GetInfo` → `validatePluginABI`) out of `newPluginBridge` into a standalone `startPlugin(cfg Config, stderr io.Writer) (*plugin.Client, LLMBridgeClient, error)`. Use `context.WithTimeout(context.Background(), timeout)` for the `GetInfo` handshake so a restart is never tied to a request's (possibly cancelled) context.
- Extend the struct to hold what restart needs and guard it with a mutex:

  ```go
  type pluginBridge struct {
      cfg    Config
      stderr io.Writer
      mu     sync.Mutex
      client *plugin.Client
      grpc   LLMBridgeClient
  }
  ```

- `acquire() (*plugin.Client, LLMBridgeClient, error)`: lock; if `client != nil && grpc != nil && !client.Exited()` return them; otherwise `restartLocked()`.
- `reacquire(stale *plugin.Client) (...)`: lock; if the current `client` is **not** `stale` and is healthy, another goroutine already restarted — return the current one (this dedupes concurrent restarts so N simultaneous failures spawn only one process); otherwise `restartLocked()`.
- `restartLocked()`: `Kill()` the old client if present, call `startPlugin`, store and return the new pair (clearing fields on error).
- Unary calls go through a helper:

  ```go
  func (b *pluginBridge) callUnary(ctx, do func(LLMBridgeClient) error) error {
      client, grpc, err := b.acquire()
      if err != nil { return err }
      err = do(grpc)
      if err == nil || status.Code(err) != codes.Unavailable { return err }
      _, grpc, rerr := b.reacquire(client)
      if rerr != nil { return err } // surface original Unavailable
      return do(grpc)               // retry once on the fresh process
  }
  ```

  `BridgeRequest`, `BridgeNonStream`, `AggregateStream` wrap their existing gRPC call in `callUnary` (the `do` closure assigns the response to an outer variable). Identity/`Unknown`-format short circuits stay exactly as they are.
- `BridgeStream`: `acquire()`, open the stream (`grpc.BridgeStream(ctx)` + send the start frame) in a small `openStream` helper; if that returns `codes.Unavailable`, `reacquire(client)` and retry the open once. The existing pump goroutines are unchanged. A mid-stream death still surfaces as a stream error (cannot retry once bytes have been written to the client) — the **next** request recovers via `acquire()`. Note this honestly in a comment.
- `Close` unchanged (`client.Kill()` under the lock).

### 3. Diagnostics — capture plugin stderr (`pkg/llmbridge/plugin_client.go`)

Add a tiny `io.Writer` that splits incoming bytes on newlines and logs each non-empty line via `logx.New().WithField("source", "llmbridge-plugin").Warn(line)` (logx is the logrus wrapper at `pkg/logx`). Pass it as `ClientConfig.Stderr` in `startPlugin` and keep it on the struct so restarts reuse it. This surfaces panic stack traces (emitted by the step-1 interceptors and by any truly fatal exit) in the gateway log and confirms the trigger.

## Files

- `cmd/picotera-llmbridge-plugin/main.go` — custom `GRPCServer` factory + `recoverUnary`/`recoverStream` interceptors (add imports `runtime/debug`, `os`; `grpc`, `codes`, `status` already present).
- `pkg/llmbridge/plugin_client.go` — `startPlugin`, struct fields, `acquire`/`reacquire`/`restartLocked`/`callUnary`, stderr log writer, rework of the four bridge methods (add imports `sync`, `os`?-no, `google.golang.org/grpc/codes`, `google.golang.org/grpc/status`, `picotera/pkg/logx`).

No changes to `client.go`, `server.go` wiring, configx, or the proto.

## Tests (`pkg/llmbridge/plugin_client_test.go`, `cmd/picotera-llmbridge-plugin/main_test.go`)

Follow the existing harness (`buildTestPlugin` builds the real plugin binary; tests run in `package llmbridge` so they can type-assert `*pluginBridge`).

1. **Self-heal after crash** (`plugin_client_test.go`): build the plugin, `New` the bridge, type-assert to `*pluginBridge`, capture `b.client`, call `b.client.Kill()` to simulate the crash, then issue a real cross-format `BridgeRequest` and assert it succeeds and `b.client` is a new (different) client. Add a streaming variant: kill, then `BridgeStream` a cross-format conversion and assert it completes.
2. **Recovery interceptor** (`main_test.go`, `package main`): call `recoverUnary` directly with a handler that panics; assert it returns `status.Code == codes.Internal` and does not propagate the panic. Same for `recoverStream` with a fake stream (reuse the existing `fakeBridgeStream`).

## Verification

```bash
mise run llmbridge-plugin                 # build the plugin target
go test ./pkg/llmbridge/ ./cmd/picotera-llmbridge-plugin/   # unit + self-heal tests
go build ./...                            # whole tree compiles
```

End-to-end smoke: `mise run server`, drive a cross-format request (e.g. `POST /api/picotera/v1/messages` routed to an OpenAI upstream), `kill` the `picotera-llmbridge-plugin` child PID, then repeat the request — it should succeed after a one-shot respawn, and the gateway log should show a `source=llmbridge-plugin` line if the child had panicked.
