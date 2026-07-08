package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"picotera/pkg/heapdump"
	"picotera/pkg/llmbridge"
	"picotera/pkg/llmbridgeimpl"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	dumpDir := os.Getenv("PICOTERA_HEAP_DUMP_DIR")
	if dumpDir == "" {
		dumpDir = os.TempDir()
	}
	heapdump.Install(dumpDir, "plugin", nil)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: llmbridge.PluginHandshake(),
		Plugins:         llmbridge.PluginMap(&server{}),
		GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
			opts = append(opts,
				grpc.MaxRecvMsgSize(llmbridge.MaxGRPCMessageSize),
				grpc.MaxSendMsgSize(llmbridge.MaxGRPCMessageSize),
				grpc.ChainUnaryInterceptor(recoverUnary),
				grpc.ChainStreamInterceptor(recoverStream),
			)
			return grpc.NewServer(opts...)
		},
	})
}

// recoverUnary turns a panic in a conversion handler into an Internal error for
// that single request instead of letting it crash the whole plugin process.
func recoverUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoveredError(info.FullMethod, r)
		}
	}()
	return handler(ctx, req)
}

// recoverStream is the streaming counterpart to recoverUnary.
func recoverStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoveredError(info.FullMethod, r)
		}
	}()
	return handler(srv, ss)
}

func recoveredError(method string, r any) error {
	fmt.Fprintf(os.Stderr, "llmbridge: panic in %s: %v\n%s\n", method, r, debug.Stack())
	return status.Errorf(codes.Internal, "llmbridge: panic: %v", r)
}

type server struct {
	llmbridge.UnimplementedLLMBridgeServer
}

func (s *server) GetInfo(context.Context, *llmbridge.GetInfoRequest) (*llmbridge.GetInfoResponse, error) {
	return &llmbridge.GetInfoResponse{AbiVersion: llmbridge.PluginABI()}, nil
}

func (s *server) BridgeRequest(ctx context.Context, req *llmbridge.BridgeRequestRequest) (*llmbridge.BridgeBodyResponse, error) {
	src, err := llmbridge.FormatFromProto(req.GetSrc())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	dst, err := llmbridge.FormatFromProto(req.GetDst())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := llmbridge.ProfileFromProto(req.GetProfile())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	body, ct, err := llmbridgeimpl.BridgeRequest(ctx, src, dst, req.GetBody(), llmbridge.HeaderFromProto(req.GetHeaders()), req.GetPendingUrl(), profile)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &llmbridge.BridgeBodyResponse{Body: body, ContentType: ct}, nil
}

func (s *server) BridgeNonStream(ctx context.Context, req *llmbridge.BridgeNonStreamRequest) (*llmbridge.BridgeBodyResponse, error) {
	src, err := llmbridge.FormatFromProto(req.GetSrc())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	upstream, err := llmbridge.FormatFromProto(req.GetUpstream())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := llmbridge.ProfileFromProto(req.GetProfile())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	body, ct, err := llmbridgeimpl.BridgeNonStream(ctx, src, upstream, req.GetBody(), llmbridge.HeaderFromProto(req.GetHeaders()), profile)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &llmbridge.BridgeBodyResponse{Body: body, ContentType: ct}, nil
}

func (s *server) AggregateStream(ctx context.Context, req *llmbridge.AggregateStreamRequest) (*llmbridge.AggregateStreamResponse, error) {
	format, err := llmbridge.FormatFromProto(req.GetFormat())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := llmbridge.ProfileFromProto(req.GetProfile())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	body, err := llmbridgeimpl.AggregateStream(ctx, format, req.GetContentType(), req.GetBody(), profile)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &llmbridge.AggregateStreamResponse{Body: body}, nil
}

func (s *server) BridgeStream(stream llmbridge.LLMBridge_BridgeStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "llmbridge: bridge stream protocol error: first frame must be start")
		}
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "llmbridge: bridge stream protocol error: first frame must be start")
	}
	src, err := llmbridge.FormatFromProto(start.GetSrc())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	upstream, err := llmbridge.FormatFromProto(start.GetUpstream())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := llmbridge.ProfileFromProto(start.GetProfile())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	reader := newStreamReader(stream)
	bridge, err := llmbridgeimpl.OpenStream(stream.Context(), src, upstream, reader, start.GetContentType(), profile)
	if err != nil {
		_ = reader.Close()
		return sendStreamError(stream, err)
	}
	defer bridge.Close()

	err = bridge.Pump(stream.Context(), streamWriter{stream: stream})
	_ = reader.Close()
	if err != nil {
		return sendStreamError(stream, err)
	}
	return nil
}

type streamReader struct {
	stream llmbridge.LLMBridge_BridgeStreamServer
	buf    []byte
	ended  bool
}

func newStreamReader(stream llmbridge.LLMBridge_BridgeStreamServer) *streamReader {
	return &streamReader{stream: stream}
}

func (r *streamReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.ended {
			return 0, io.EOF
		}
		chunk, err := r.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		switch payload := chunk.GetPayload().(type) {
		case *llmbridge.BridgeStreamChunk_Data:
			r.buf = payload.Data
		case *llmbridge.BridgeStreamChunk_End:
			r.ended = true
		case *llmbridge.BridgeStreamChunk_Start:
			return 0, fmt.Errorf("llmbridge: bridge stream protocol error: duplicate start frame")
		case *llmbridge.BridgeStreamChunk_Error:
			msg := payload.Error.GetMessage()
			if msg == "" {
				return 0, fmt.Errorf("llmbridge: bridge stream protocol error: empty error frame")
			}
			return 0, fmt.Errorf("%s", msg)
		default:
			return 0, fmt.Errorf("llmbridge: bridge stream protocol error: unexpected frame")
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *streamReader) Close() error {
	r.ended = true
	return nil
}

type streamWriter struct {
	stream llmbridge.LLMBridge_BridgeStreamServer
}

func (w streamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// The stream pump passes a fresh encoded event buffer on each Write, and
	// gRPC serializes the message before Send returns.
	if err := w.stream.Send(&llmbridge.BridgeStreamChunk{Payload: &llmbridge.BridgeStreamChunk_Data{Data: p}}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func sendStreamError(stream llmbridge.LLMBridge_BridgeStreamServer, err error) error {
	if err == nil {
		return nil
	}
	_ = stream.Send(&llmbridge.BridgeStreamChunk{Payload: &llmbridge.BridgeStreamChunk_Error{Error: &llmbridge.BridgeStreamError{Message: err.Error()}}})
	return status.Error(codes.Internal, err.Error())
}
