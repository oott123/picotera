package llmbridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"picotera/pkg/logx"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultPluginStartTimeout = 10 * time.Second

var errClosed = fmt.Errorf("llmbridge: bridge closed")

type pluginBridge struct {
	cfg    Config
	stderr io.Writer

	mu     sync.Mutex
	client *plugin.Client
	grpc   LLMBridgeClient
	closed bool
}

// startPlugin spawns the plugin subprocess and performs the handshake. It is
// used both for the initial start and for transparent restarts after a crash,
// so the handshake is anchored to context.Background() rather than any single
// request's (possibly cancelled) context.
func startPlugin(cfg Config, stderr io.Writer) (*plugin.Client, LLMBridgeClient, error) {
	timeout := cfg.PluginStartTimeout
	if timeout == 0 {
		timeout = defaultPluginStartTimeout
	}
	cmd := exec.Command(cfg.PluginPath)
	cmd.Env = append(os.Environ(), "PICOTERA_HEAP_DUMP_DIR="+cfg.HeapDumpDir)
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  pluginHandshake,
		Plugins:          pluginMap(nil),
		Cmd:              cmd,
		StartTimeout:     timeout,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Stderr:           stderr,
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(MaxGRPCMessageSize),
				grpc.MaxCallSendMsgSize(MaxGRPCMessageSize),
			),
		},
	})
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("llmbridge: start plugin: %w", err)
	}
	raw, err := rpcClient.Dispense(pluginName)
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("llmbridge: dispense plugin: %w", err)
	}
	grpcClient, ok := raw.(LLMBridgeClient)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("llmbridge: plugin returned unexpected client %T", raw)
	}
	infoCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	info, err := grpcClient.GetInfo(infoCtx, &GetInfoRequest{})
	if err != nil {
		client.Kill()
		return nil, nil, fmt.Errorf("llmbridge: validate plugin: %w", err)
	}
	if err := validatePluginABI(info.GetAbiVersion()); err != nil {
		client.Kill()
		return nil, nil, err
	}
	return client, grpcClient, nil
}

func newPluginBridge(_ context.Context, cfg Config) (Bridge, error) {
	stderr := newPluginLogWriter()
	client, grpcClient, err := startPlugin(cfg, stderr)
	if err != nil {
		return nil, err
	}
	return &pluginBridge{cfg: cfg, stderr: stderr, client: client, grpc: grpcClient}, nil
}

// acquire returns a live gRPC client, restarting the subprocess if it has
// exited since the last call.
func (b *pluginBridge) acquire() (*plugin.Client, LLMBridgeClient, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, nil, errClosed
	}
	if b.client != nil && b.grpc != nil && !b.client.Exited() {
		return b.client, b.grpc, nil
	}
	return b.restartLocked()
}

// reacquire is called after a call failed with codes.Unavailable. It restarts
// the subprocess only if the failed client (stale) is still the current one;
// if another goroutine already restarted, the fresh client is returned. This
// dedupes concurrent restarts so N simultaneous failures spawn one process.
func (b *pluginBridge) reacquire(stale *plugin.Client) (*plugin.Client, LLMBridgeClient, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, nil, errClosed
	}
	if b.client != stale && b.client != nil && b.grpc != nil && !b.client.Exited() {
		return b.client, b.grpc, nil
	}
	return b.restartLocked()
}

func (b *pluginBridge) restartLocked() (*plugin.Client, LLMBridgeClient, error) {
	if b.closed {
		return nil, nil, errClosed
	}
	if b.client != nil {
		b.client.Kill()
	}
	client, grpcClient, err := startPlugin(b.cfg, b.stderr)
	if err != nil {
		b.client = nil
		b.grpc = nil
		return nil, nil, err
	}
	b.client = client
	b.grpc = grpcClient
	return client, grpcClient, nil
}

// callUnary runs a unary gRPC call against a live plugin, restarting and
// retrying exactly once if the plugin turns out to be dead.
func (b *pluginBridge) callUnary(do func(LLMBridgeClient) error) error {
	client, grpcClient, err := b.acquire()
	if err != nil {
		return err
	}
	err = do(grpcClient)
	if err == nil || status.Code(err) != codes.Unavailable {
		return err
	}
	_, grpcClient, rerr := b.reacquire(client)
	if rerr != nil {
		return err
	}
	return do(grpcClient)
}

func (b *pluginBridge) Enabled() bool {
	return true
}

func (b *pluginBridge) Close(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.client != nil {
		b.client.Kill()
		b.client = nil
		b.grpc = nil
	}
	return nil
}

// SignalPlugin forwards sig to the live plugin subprocess. A dead or missing
// plugin is skipped rather than restarted: a freshly spawned process's heap is
// useless for diagnosing a leak.
func (b *pluginBridge) SignalPlugin(sig syscall.Signal) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil || b.client.Exited() {
		logx.New().WithField("source", "llmbridge").Debug("skip signalling plugin: no live subprocess")
		return nil
	}
	rc := b.client.ReattachConfig()
	if rc == nil || rc.Pid == 0 {
		logx.New().WithField("source", "llmbridge").Debug("skip signalling plugin: no pid")
		return nil
	}
	return syscall.Kill(rc.Pid, sig)
}

func (b *pluginBridge) BridgeRequest(ctx context.Context, src, dst Format, body []byte, headers http.Header, pendingURL string, profile OutboundProfile) ([]byte, string, error) {
	if src == FormatUnknown || dst == FormatUnknown {
		return nil, "", fmt.Errorf("llmbridge: bridge with unknown format (src=%s dst=%s)", src, dst)
	}
	if src == dst {
		return body, contentTypeOrDefault(headers), nil
	}
	profileMsg, err := profileToProto(profile)
	if err != nil {
		return nil, "", err
	}
	var resp *BridgeBodyResponse
	err = b.callUnary(func(c LLMBridgeClient) error {
		var callErr error
		resp, callErr = c.BridgeRequest(ctx, &BridgeRequestRequest{
			Src:        formatToProto(src),
			Dst:        formatToProto(dst),
			Body:       body,
			Headers:    headerToProto(headers),
			PendingUrl: pendingURL,
			Profile:    profileMsg,
		})
		return callErr
	})
	if err != nil {
		return nil, "", fmt.Errorf("llmbridge: bridge request plugin call: %w", err)
	}
	return resp.GetBody(), resp.GetContentType(), nil
}

func (b *pluginBridge) BridgeNonStream(ctx context.Context, src, upstream Format, upstreamBody []byte, upstreamHeaders http.Header, profile OutboundProfile) ([]byte, string, error) {
	if src == FormatUnknown || upstream == FormatUnknown {
		return nil, "", fmt.Errorf("llmbridge: bridge non-stream with unknown format (src=%s upstream=%s)", src, upstream)
	}
	if src == upstream {
		return upstreamBody, contentTypeOrDefault(upstreamHeaders), nil
	}
	profileMsg, err := profileToProto(profile)
	if err != nil {
		return nil, "", err
	}
	var resp *BridgeBodyResponse
	err = b.callUnary(func(c LLMBridgeClient) error {
		var callErr error
		resp, callErr = c.BridgeNonStream(ctx, &BridgeNonStreamRequest{
			Src:      formatToProto(src),
			Upstream: formatToProto(upstream),
			Body:     upstreamBody,
			Headers:  headerToProto(upstreamHeaders),
			Profile:  profileMsg,
		})
		return callErr
	})
	if err != nil {
		return nil, "", fmt.Errorf("llmbridge: bridge non-stream plugin call: %w", err)
	}
	return resp.GetBody(), resp.GetContentType(), nil
}

func (b *pluginBridge) AggregateStream(ctx context.Context, format Format, contentType string, body []byte, profile OutboundProfile) ([]byte, error) {
	if format == FormatUnknown {
		return nil, fmt.Errorf("llmbridge: aggregate stream with unknown format")
	}
	profileMsg, err := profileToProto(profile)
	if err != nil {
		return nil, err
	}
	var resp *AggregateStreamResponse
	err = b.callUnary(func(c LLMBridgeClient) error {
		var callErr error
		resp, callErr = c.AggregateStream(ctx, &AggregateStreamRequest{
			Format:      formatToProto(format),
			ContentType: contentType,
			Body:        body,
			Profile:     profileMsg,
		})
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("llmbridge: aggregate stream plugin call: %w", err)
	}
	return resp.GetBody(), nil
}

func (b *pluginBridge) BridgeStream(ctx context.Context, src, upstream Format, upstreamBody io.ReadCloser, upstreamCT string, profile OutboundProfile) (io.ReadCloser, error) {
	if src == FormatUnknown || upstream == FormatUnknown {
		_ = upstreamBody.Close()
		return nil, fmt.Errorf("llmbridge: bridge stream with unknown format (src=%s upstream=%s)", src, upstream)
	}
	if src == upstream {
		return upstreamBody, nil
	}
	profileMsg, err := profileToProto(profile)
	if err != nil {
		_ = upstreamBody.Close()
		return nil, err
	}
	// openStream opens the bidi stream and sends the start frame. Restarting
	// after the pump goroutines begin is not possible (bytes may already have
	// reached the client), so a dead plugin is only recovered before the first
	// frame here; a mid-stream death surfaces as a stream error and the next
	// request recovers via acquire().
	startFrame := &BridgeStreamChunk{Payload: &BridgeStreamChunk_Start{Start: &BridgeStreamStart{
		Src:         formatToProto(src),
		Upstream:    formatToProto(upstream),
		ContentType: upstreamCT,
		Profile:     profileMsg,
	}}}
	openStream := func(c LLMBridgeClient) (LLMBridge_BridgeStreamClient, error) {
		s, err := c.BridgeStream(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.Send(startFrame); err != nil {
			_ = s.CloseSend()
			return nil, err
		}
		return s, nil
	}

	client, grpcClient, err := b.acquire()
	if err != nil {
		_ = upstreamBody.Close()
		return nil, fmt.Errorf("llmbridge: open bridge stream plugin call: %w", err)
	}
	stream, err := openStream(grpcClient)
	if err != nil && status.Code(err) == codes.Unavailable {
		if _, grpcClient, rerr := b.reacquire(client); rerr == nil {
			stream, err = openStream(grpcClient)
		}
	}
	if err != nil {
		_ = upstreamBody.Close()
		return nil, fmt.Errorf("llmbridge: open bridge stream plugin call: %w", err)
	}

	pr, pw := io.Pipe()
	done := make(chan struct{})
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			_ = upstreamBody.Close()
			_ = stream.CloseSend()
			close(done)
		})
	}

	go func() {
		defer closeAll()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := upstreamBody.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				if err := stream.Send(&BridgeStreamChunk{Payload: &BridgeStreamChunk_Data{Data: data}}); err != nil {
					_ = pw.CloseWithError(fmt.Errorf("llmbridge: send bridge stream data: %w", err))
					return
				}
			}
			if readErr == nil {
				continue
			}
			if readErr == io.EOF {
				if err := stream.Send(&BridgeStreamChunk{Payload: &BridgeStreamChunk_End{End: &BridgeStreamEnd{}}}); err != nil {
					_ = pw.CloseWithError(fmt.Errorf("llmbridge: send bridge stream end: %w", err))
				}
				return
			}
			_ = stream.Send(&BridgeStreamChunk{Payload: &BridgeStreamChunk_Error{Error: &BridgeStreamError{Message: readErr.Error()}}})
			_ = pw.CloseWithError(readErr)
			return
		}
	}()

	go func() {
		defer closeAll()
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					_ = pw.Close()
				} else {
					_ = pw.CloseWithError(fmt.Errorf("llmbridge: receive bridge stream data: %w", err))
				}
				return
			}
			switch payload := chunk.GetPayload().(type) {
			case *BridgeStreamChunk_Data:
				if _, err := pw.Write(payload.Data); err != nil {
					return
				}
			case *BridgeStreamChunk_Error:
				msg := payload.Error.GetMessage()
				if msg == "" {
					msg = "llmbridge: bridge stream plugin returned empty error"
				}
				_ = pw.CloseWithError(fmt.Errorf("%s", msg))
				return
			default:
				_ = pw.CloseWithError(fmt.Errorf("llmbridge: bridge stream plugin returned unexpected frame"))
				return
			}
		}
	}()

	return &pluginStreamReadCloser{ReadCloser: pr, close: closeAll, done: done}, nil
}

// pluginLogWriter forwards the plugin subprocess's stderr to the gateway log,
// one line per record, so panic stack traces and exit causes are visible
// instead of being discarded (go-plugin defaults ClientConfig.Stderr to
// io.Discard).
type pluginLogWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newPluginLogWriter() *pluginLogWriter {
	return &pluginLogWriter{}
}

func (w *pluginLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No complete line yet; put the partial back and wait for more.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			logx.New().WithField("source", "llmbridge-plugin").Warn(trimmed)
		}
	}
	return len(p), nil
}

type pluginStreamReadCloser struct {
	io.ReadCloser
	close func()
	done  <-chan struct{}
}

func (r *pluginStreamReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.close()
	<-r.done
	return err
}
