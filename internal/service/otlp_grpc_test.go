package service

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tracestore "github.com/threadforge-ai/threadforge/internal/traces"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestOTLPGRPCIngestsAllSignalsAndRedactsSecrets(t *testing.T) {
	traceStore, err := tracestore.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewOTLPGRPCServer(traceStore)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	var logs collectorlogs.ExportLogsServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"grpc-fixture"}}]},"scopeLogs":[{"scope":{"name":"threadforge-test","version":"1"},"logRecords":[{"timeUnixNano":"1750000000000000000","severityText":"INFO","body":{"stringValue":"local event"},"attributes":[{"key":"threadforge.action","value":{"stringValue":"tool.completed"}},{"key":"api_key","value":{"stringValue":"grpc-secret-123456789"}}]}]}]}]}`), &logs); err != nil {
		t.Fatal(err)
	}
	if _, err := collectorlogs.NewLogsServiceClient(connection).Export(ctx, &logs); err != nil {
		t.Fatalf("export OTLP logs: %v", err)
	}

	var spans collectortrace.ExportTraceServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceSpans":[{"scopeSpans":[{"scope":{"name":"threadforge-test"},"spans":[{"traceId":"00112233445566778899aabbccddeeff","spanId":"0011223344556677","name":"grpc span","startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000000000000001","status":{"code":1},"attributes":[{"key":"password","value":{"stringValue":"span-secret-123456789"}}]}]}]}]}`), &spans); err != nil {
		t.Fatal(err)
	}
	if _, err := collectortrace.NewTraceServiceClient(connection).Export(ctx, &spans); err != nil {
		t.Fatalf("export OTLP traces: %v", err)
	}

	var metrics collectormetrics.ExportMetricsServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"scope":{"name":"threadforge-test"},"metrics":[{"name":"latency","unit":"ms","gauge":{"dataPoints":[{"timeUnixNano":"1750000000000000000","asDouble":12.5,"attributes":[{"key":"region","value":{"stringValue":"local"}}]}]}}]}]}]}`), &metrics); err != nil {
		t.Fatal(err)
	}
	if _, err := collectormetrics.NewMetricsServiceClient(connection).Export(ctx, &metrics); err != nil {
		t.Fatalf("export OTLP metrics: %v", err)
	}

	stored, err := os.ReadFile(traceStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "grpc-secret-123456789") || strings.Contains(string(stored), "span-secret-123456789") {
		t.Fatal("OTLP gRPC persisted a credential-like attribute value")
	}
	for _, signal := range []string{`"action":"tool.completed"`, `"kind":"otel_span"`, `"kind":"otel_metric"`} {
		if !strings.Contains(string(stored), signal) {
			t.Fatalf("normalized trace store is missing %s", signal)
		}
	}
	var rows []map[string]json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(string(stored)), "\n") {
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("stored event is not JSON: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 3 {
		t.Fatalf("stored %d events, want one per OTLP signal", len(rows))
	}
}

func TestOTLPGRPCRejectsInvalidAndOversizedBatchesAtomically(t *testing.T) {
	traceStore, err := tracestore.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewOTLPGRPCServer(traceStore)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	var invalid collectortrace.ExportTraceServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"00000000000000000000000000000000","spanId":"0000000000000000","name":"invalid","startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000000000000000"}]}]}]}`), &invalid); err != nil {
		t.Fatal(err)
	}
	_, err = collectortrace.NewTraceServiceClient(connection).Export(ctx, &invalid)
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid trace status = %s, want InvalidArgument (err=%v)", grpcstatus.Code(err), err)
	}

	var oversized collectorlogs.ExportLogsServiceRequest
	var records strings.Builder
	for index := 0; index <= maxEventBatch; index++ {
		if index > 0 {
			records.WriteByte(',')
		}
		records.WriteString(`{"timeUnixNano":"1750000000000000000","severityText":"INFO","body":{"stringValue":"bounded"}}`)
	}
	requestJSON := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[` + records.String() + `]}]}]}`
	if err := protojson.Unmarshal([]byte(requestJSON), &oversized); err != nil {
		t.Fatal(err)
	}
	if proto.Size(&oversized) > maxRequestBytes {
		t.Fatal("oversized-batch fixture unexpectedly exceeds the byte bound")
	}
	_, err = collectorlogs.NewLogsServiceClient(connection).Export(ctx, &oversized)
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized batch status = %s, want InvalidArgument (err=%v)", grpcstatus.Code(err), err)
	}

	stored, err := os.ReadFile(traceStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("invalid batch partially persisted %d bytes", len(stored))
	}
}

func TestOTLPGRPCRejectsNilTraceStore(t *testing.T) {
	if server, err := NewOTLPGRPCServer(nil); err == nil || server != nil {
		t.Fatalf("nil trace store result = (%v, %v), want an error and no server", server, err)
	}
}
