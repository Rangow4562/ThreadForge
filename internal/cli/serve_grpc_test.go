package cli

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeRejectsNonLoopbackOTLPGRPCAddress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"serve", "--listen", "127.0.0.1:4318", "--grpc-listen", "0.0.0.0:4317"}, &stdout, &stderr)
	if code == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "OTLP gRPC service must bind to a loopback IP address") {
		t.Fatalf("serve wildcard gRPC listener code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestServeEmptyGRPCListenDisablesDefaultListener(t *testing.T) {
	if os.Getenv("THREADFORGE_TEST_SERVE_EMPTY_GRPC") == "1" {
		code := Run([]string{"serve", "--listen", os.Getenv("THREADFORGE_TEST_HTTP_LISTEN"), "--grpc-listen", ""}, os.Stdout, os.Stderr)
		os.Exit(code)
	}

	occupiedGRPC, err := net.Listen("tcp", "127.0.0.1:4317")
	if err != nil {
		t.Skipf("default OTLP gRPC port is unavailable for the disable-path test: %v", err)
	}
	defer occupiedGRPC.Close()
	httpReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpAddress := httpReservation.Addr().String()
	_ = httpReservation.Close()

	home := filepath.Join(t.TempDir(), "threadforge-home")
	var output bytes.Buffer
	command := exec.Command(os.Args[0], "-test.run=^TestServeEmptyGRPCListenDisablesDefaultListener$")
	command.Env = testEnvironmentWithOverrides(map[string]string{
		"THREADFORGE_TEST_SERVE_EMPTY_GRPC": "1",
		"THREADFORGE_TEST_HTTP_LISTEN":      httpAddress,
		"THREADFORGE_HOME":                  home,
	})
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = command.Process.Kill()
			<-wait
		}
	})

	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-wait:
			finished = true
			t.Fatalf("serve exited before HTTP became available: %v; output=%s", err, output.String())
		default:
		}
		response, err := client.Get("http://" + httpAddress + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				_ = command.Process.Kill()
				<-wait
				finished = true
				t.Fatalf("health status = %d, want 200; output=%s", response.StatusCode, output.String())
			}
			_ = command.Process.Kill()
			<-wait
			finished = true
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = command.Process.Kill()
	<-wait
	finished = true
	t.Fatalf("serve did not start HTTP with --grpc-listen empty; output=%s", output.String())
}

func testEnvironmentWithOverrides(overrides map[string]string) []string {
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		overridden := false
		for overrideKey := range overrides {
			if strings.EqualFold(key, overrideKey) {
				overridden = true
				break
			}
		}
		if !overridden {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}
