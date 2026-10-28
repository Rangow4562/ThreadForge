package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/threadforge-ai/threadforge/internal/service"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestHTTPAndGRPCRuntimeRejectsNonLoopbackListenersBeforeOpeningStores(t *testing.T) {
	tests := []struct {
		name      string
		httpAddr  string
		grpcAddr  string
		wantError string
	}{
		{name: "http wildcard", httpAddr: "0.0.0.0:4318", grpcAddr: "127.0.0.1:4317", wantError: "HTTP"},
		{name: "grpc wildcard", httpAddr: "127.0.0.1:4318", grpcAddr: "0.0.0.0:4317", wantError: "gRPC"},
		{name: "grpc public address", httpAddr: "127.0.0.1:4318", grpcAddr: "192.0.2.7:4317", wantError: "gRPC"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "threadforge-home")
			t.Setenv("THREADFORGE_HOME", home)
			err := RunHTTPWithProjectRootAndGRPC(test.httpAddr, test.grpcAddr, "", "", "")
			if err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("listener validation error = %v, want %s loopback rejection", err, test.wantError)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("invalid listener created the local store directory: %v", err)
			}
		})
	}
}

func TestOpenLocalListenersClosesHTTPWhenGRPCPortIsOccupied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	var openedHTTP net.Listener
	listen := func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil && address == "127.0.0.1:0" {
			openedHTTP = listener
		}
		return listener, err
	}
	httpListener, grpcListener, err := openLocalListeners("127.0.0.1:0", occupied.Addr().String(), listen)
	if err == nil || httpListener != nil || grpcListener != nil || openedHTTP == nil {
		t.Fatalf("occupied gRPC result = (%v, %v, %v), expected closed HTTP listener and error", httpListener, grpcListener, err)
	}
	reopened, err := net.Listen("tcp", openedHTTP.Addr().String())
	if err != nil {
		t.Fatalf("HTTP listener leaked after gRPC bind failure: %v", err)
	}
	_ = reopened.Close()
}

func TestOpenLocalListenersLeavesGRPCDisabledWhenAddressIsEmpty(t *testing.T) {
	httpListener, grpcListener, err := openLocalListeners("127.0.0.1:0", "", net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	if httpListener == nil || grpcListener != nil {
		t.Fatalf("listeners = (%v, %v), want HTTP only", httpListener, grpcListener)
	}
	_ = httpListener.Close()
}

func probeLoopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable: %v", err)
	}
	defer ln.Close()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
	if err != nil {
		t.Skipf("loopback connections unavailable in this environment (AppContainer/sandbox socket isolation): %v", err)
	}
	_ = conn.Close()
}

func TestLocalServerServesHTTPAndAllOTLPGRPCSignalsThenShutsDown(t *testing.T) {
	probeLoopback(t)
	home := t.TempDir()
	database, err := store.Open(filepath.Join(home, "threadforge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	traceStore, err := traces.Open(filepath.Join(home, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	httpListener, grpcListener, err := openLocalListeners("127.0.0.1:0", "127.0.0.1:0", net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	grpcServer, err := service.NewOTLPGRPCServer(traceStore)
	if err != nil {
		_ = httpListener.Close()
		_ = grpcListener.Close()
		t.Fatal(err)
	}
	serverContext, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveLocalServers(serverContext, httpListener, grpcListener, service.NewHandler(database, traceStore), grpcServer)
	}()
	var waitForServerDone <-chan error = serverDone
	t.Cleanup(func() {
		cancelServer()
		if waitForServerDone != nil {
			select {
			case <-waitForServerDone:
			case <-time.After(5 * time.Second):
				t.Error("local telemetry servers did not stop during cleanup")
			}
		}
	})

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + httpListener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}

	dialContext, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	connection, err := grpc.DialContext(dialContext, grpcListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	cancelDial()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	rpcContext, cancelRPC := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRPC()

	var logs collectorlogs.ExportLogsServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceLogs":[{"scopeLogs":[{"scope":{"name":"runtime-test"},"logRecords":[{"timeUnixNano":"1750000000000000000","severityText":"INFO","body":{"stringValue":"local log"}}]}]}]}`), &logs); err != nil {
		t.Fatal(err)
	}
	if _, err := collectorlogs.NewLogsServiceClient(connection).Export(rpcContext, &logs); err != nil {
		t.Fatalf("runtime OTLP logs RPC: %v", err)
	}
	var spans collectortrace.ExportTraceServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"00112233445566778899aabbccddeeff","spanId":"0011223344556677","name":"runtime span","startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000000000000001"}]}]}]}`), &spans); err != nil {
		t.Fatal(err)
	}
	if _, err := collectortrace.NewTraceServiceClient(connection).Export(rpcContext, &spans); err != nil {
		t.Fatalf("runtime OTLP traces RPC: %v", err)
	}
	var metrics collectormetrics.ExportMetricsServiceRequest
	if err := protojson.Unmarshal([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"runtime.metric","gauge":{"dataPoints":[{"timeUnixNano":"1750000000000000000","asDouble":1.5}]}}]}]}]}`), &metrics); err != nil {
		t.Fatal(err)
	}
	if _, err := collectormetrics.NewMetricsServiceClient(connection).Export(rpcContext, &metrics); err != nil {
		t.Fatalf("runtime OTLP metrics RPC: %v", err)
	}

	stored, err := os.ReadFile(traceStore.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(stored)), "\n") + 1; got != 3 {
		t.Fatalf("runtime persisted %d OTLP events, want 3", got)
	}
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(stored)), "\n")[0]), new(map[string]any)); err != nil {
		t.Fatalf("runtime trace store contains invalid JSON: %v", err)
	}

	cancelServer()
	select {
	case err := <-serverDone:
		waitForServerDone = nil
		if err != nil {
			t.Fatalf("graceful listener shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local telemetry servers did not stop after context cancellation")
	}
	if response, err := client.Get("http://" + httpListener.Addr().String() + "/healthz"); err == nil {
		_ = response.Body.Close()
		t.Fatal("HTTP listener remained available after shutdown")
	}
	connectionProbe, err := net.DialTimeout("tcp", grpcListener.Addr().String(), 250*time.Millisecond)
	if err == nil {
		_ = connectionProbe.Close()
		t.Fatal("OTLP gRPC listener remained available after shutdown")
	}
}
