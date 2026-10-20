package ci

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestShutdownHTTPServerRetriesOnlyAfterGraceDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	defer release()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-releaseHandler
		w.WriteHeader(http.StatusNoContent)
	})}
	defer server.Close()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	responseErrors := make(chan error, 1)
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
		responseErrors <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler did not start")
	}

	// Let the first shutdown attempt hit its deadline, then release the active
	// request so the bounded retry can complete gracefully.
	releaseTimer := time.AfterFunc(250*time.Millisecond, release)
	err = shutdownHTTPServer(server, 200*time.Millisecond)
	releaseTimer.Stop()
	if err != nil {
		t.Fatalf("bounded shutdown retry failed: %v", err)
	}
	select {
	case err := <-responseErrors:
		if err != nil {
			t.Fatalf("HTTP request did not finish after handler release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request did not finish")
	}
	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP server did not finish serving after shutdown")
	}
	client.CloseIdleConnections()
}
