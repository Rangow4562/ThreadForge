package service

import (
	"context"
	"errors"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/threadforge-ai/threadforge/internal/traces"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlplogs "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxOTLPGRPCConcurrentStreams = 32

type otlpGRPCLogsService struct {
	collectorlogs.UnimplementedLogsServiceServer
	traces *traces.Store
}

type otlpGRPCTraceService struct {
	collectortrace.UnimplementedTraceServiceServer
	traces *traces.Store
}

type otlpGRPCMetricsService struct {
	collectormetrics.UnimplementedMetricsServiceServer
	traces *traces.Store
}

// NewOTLPGRPCServer creates the standard OTLP logs, traces, and metrics
// services. Callers must bind it only to a loopback listener.
func NewOTLPGRPCServer(traceStore *traces.Store) (*grpc.Server, error) {
	if traceStore == nil {
		return nil, errors.New("local trace store is required for OTLP gRPC")
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxRequestBytes),
		grpc.MaxSendMsgSize(maxRequestBytes),
		grpc.MaxConcurrentStreams(maxOTLPGRPCConcurrentStreams),
	)
	collectorlogs.RegisterLogsServiceServer(server, &otlpGRPCLogsService{traces: traceStore})
	collectortrace.RegisterTraceServiceServer(server, &otlpGRPCTraceService{traces: traceStore})
	collectormetrics.RegisterMetricsServiceServer(server, &otlpGRPCMetricsService{traces: traceStore})
	return server, nil
}

func (s *otlpGRPCLogsService) Export(ctx context.Context, request *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	if request == nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "invalid OTLP logs request")
	}
	if proto.Size(request) > maxRequestBytes {
		return nil, grpcstatus.Error(codes.ResourceExhausted, "OTLP request exceeds the 8 MiB limit")
	}
	body, err := marshalOTLPGRPCData(&otlplogs.LogsData{ResourceLogs: request.ResourceLogs})
	if err != nil {
		return nil, invalidOTLPGRPCRequest(err, "invalid OTLP logs request")
	}
	if err := ingestOTLPGRPC(ctx, s.traces, body, decodeOTLPLogs); err != nil {
		return nil, err
	}
	return &collectorlogs.ExportLogsServiceResponse{}, nil
}

func (s *otlpGRPCTraceService) Export(ctx context.Context, request *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	if request == nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "invalid OTLP traces request")
	}
	if proto.Size(request) > maxRequestBytes {
		return nil, grpcstatus.Error(codes.ResourceExhausted, "OTLP request exceeds the 8 MiB limit")
	}
	body, err := marshalOTLPGRPCData(&otlptrace.TracesData{ResourceSpans: request.ResourceSpans})
	if err != nil {
		return nil, invalidOTLPGRPCRequest(err, "invalid OTLP traces request")
	}
	if err := ingestOTLPGRPC(ctx, s.traces, body, decodeOTLPTraces); err != nil {
		return nil, err
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func (s *otlpGRPCMetricsService) Export(ctx context.Context, request *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	if request == nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "invalid OTLP metrics request")
	}
	if proto.Size(request) > maxRequestBytes {
		return nil, grpcstatus.Error(codes.ResourceExhausted, "OTLP request exceeds the 8 MiB limit")
	}
	body, err := marshalOTLPGRPCData(&otlpmetrics.MetricsData{ResourceMetrics: request.ResourceMetrics})
	if err != nil {
		return nil, invalidOTLPGRPCRequest(err, "invalid OTLP metrics request")
	}
	if err := ingestOTLPGRPC(ctx, s.traces, body, decodeOTLPMetrics); err != nil {
		return nil, err
	}
	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

func invalidOTLPGRPCRequest(err error, message string) error {
	if grpcstatus.Code(err) != codes.Unknown {
		return err
	}
	return grpcstatus.Error(codes.InvalidArgument, message)
}

func marshalOTLPGRPCData(message proto.Message) ([]byte, error) {
	body, err := (protojson.MarshalOptions{UseEnumNumbers: true}).Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(body) > maxRequestBytes {
		return nil, grpcstatus.Error(codes.ResourceExhausted, "normalized OTLP request exceeds the 8 MiB limit")
	}
	return body, nil
}

func ingestOTLPGRPC(ctx context.Context, traceStore *traces.Store, body []byte, decode func([]byte) ([]beacon.Event, error)) error {
	events, err := decode(body)
	if err != nil || len(events) == 0 || len(events) > maxEventBatch {
		return grpcstatus.Error(codes.InvalidArgument, "invalid OTLP signal payload")
	}
	if err := traceStore.Append(ctx, events); err != nil {
		if errors.Is(err, context.Canceled) {
			return grpcstatus.Error(codes.Canceled, "OTLP request canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return grpcstatus.Error(codes.DeadlineExceeded, "OTLP request deadline exceeded")
		}
		return grpcstatus.Error(codes.Internal, "could not persist local OTLP events")
	}
	return nil
}
