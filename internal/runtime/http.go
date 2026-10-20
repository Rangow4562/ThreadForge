package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/mcp"
	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/projectfiles"
	"github.com/threadforge-ai/threadforge/internal/service"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/internal/traces"
	"google.golang.org/grpc"
)

func RunHTTP(listen, databasePath, tracePath string) error {
	return RunHTTPWithProjectRoot(listen, databasePath, tracePath, "")
}

func RunHTTPWithProjectRoot(listen, databasePath, tracePath, projectRoot string) error {
	return RunHTTPWithProjectRootAndGRPC(listen, "", databasePath, tracePath, projectRoot)
}

func RunHTTPWithProjectRootAndGRPC(httpListen, grpcListen, databasePath, tracePath, projectRoot string) error {
	if !IsLoopbackAddress(httpListen) {
		return errors.New("Threadforge HTTP service must bind to a loopback IP address")
	}
	if grpcListen != "" && !IsLoopbackAddress(grpcListen) {
		return errors.New("Threadforge OTLP gRPC service must bind to a loopback IP address")
	}
	paths, err := appdata.Resolve(databasePath, tracePath)
	if err != nil {
		return err
	}
	procedureStore, err := store.Open(paths.SQLite)
	if err != nil {
		return err
	}
	defer procedureStore.Close()
	traceStore, err := traces.Open(paths.Traces)
	if err != nil {
		return err
	}
	var memoryStore *projectfiles.Store
	var publishedProcedures *procedurefiles.Store
	if strings.TrimSpace(projectRoot) != "" {
		memoryStore, err = projectfiles.New(projectRoot)
		if err != nil {
			return fmt.Errorf("open version-controlled project memory: %w", err)
		}
		publishedProcedures, err = procedurefiles.New(projectRoot)
		if err != nil {
			return fmt.Errorf("open version-controlled project procedures: %w", err)
		}
	}
	httpListener, grpcListener, err := openLocalListeners(httpListen, grpcListen, net.Listen)
	if err != nil {
		return err
	}
	var grpcServer *grpc.Server
	if grpcListener != nil {
		grpcServer, err = service.NewOTLPGRPCServer(traceStore)
		if err != nil {
			_ = grpcListener.Close()
			_ = httpListener.Close()
			return err
		}
	}
	httpHandler := service.NewHandlerWithProjectStoresRulesAndInventoryRoots(procedureStore, traceStore, memoryStore, publishedProcedures, filepath.Join(paths.Root, "rules"), "", projectRoot)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveLocalServers(ctx, httpListener, grpcListener, httpHandler, grpcServer)
}

type listenFunc func(network, address string) (net.Listener, error)

func openLocalListeners(httpAddress, grpcAddress string, listen listenFunc) (net.Listener, net.Listener, error) {
	if listen == nil {
		return nil, nil, errors.New("local listener factory is required")
	}
	httpListener, err := listen("tcp", httpAddress)
	if err != nil {
		if httpListener != nil {
			_ = httpListener.Close()
		}
		return nil, nil, fmt.Errorf("listen on %s: %w", httpAddress, err)
	}
	if httpListener == nil {
		return nil, nil, errors.New("HTTP listener factory returned no listener")
	}
	if grpcAddress == "" {
		return httpListener, nil, nil
	}
	grpcListener, err := listen("tcp", grpcAddress)
	if err != nil {
		if grpcListener != nil {
			_ = grpcListener.Close()
		}
		_ = httpListener.Close()
		return nil, nil, fmt.Errorf("listen for OTLP gRPC on %s: %w", grpcAddress, err)
	}
	if grpcListener == nil {
		_ = httpListener.Close()
		return nil, nil, errors.New("OTLP gRPC listener factory returned no listener")
	}
	return httpListener, grpcListener, nil
}

func serveLocalServers(ctx context.Context, httpListener, grpcListener net.Listener, httpHandler http.Handler, grpcServer *grpc.Server) error {
	if ctx == nil || httpListener == nil || httpHandler == nil || (grpcListener == nil) != (grpcServer == nil) {
		return errors.New("invalid local telemetry server configuration")
	}
	defer httpListener.Close()
	if grpcListener != nil {
		defer grpcListener.Close()
	}
	server := &http.Server{
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 2)
	go func() { serveErr <- server.Serve(httpListener) }()
	if grpcServer != nil {
		go func() { serveErr <- grpcServer.Serve(grpcListener) }()
	}
	select {
	case err := <-serveErr:
		_ = server.Close()
		if grpcServer != nil {
			grpcServer.Stop()
		}
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return fmt.Errorf("local telemetry service stopped: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpDone := make(chan error, 1)
		go func() { httpDone <- server.Shutdown(shutdownCtx) }()
		var grpcDone chan struct{}
		if grpcServer != nil {
			grpcDone = make(chan struct{})
			go func() {
				grpcServer.GracefulStop()
				close(grpcDone)
			}()
		}
		var httpErr error
		httpStopped := false
		grpcStopped := grpcServer == nil
		for !httpStopped || !grpcStopped {
			select {
			case httpErr = <-httpDone:
				httpStopped = true
			case <-grpcDone:
				grpcStopped = true
			case <-shutdownCtx.Done():
				_ = server.Close()
				if grpcServer != nil {
					grpcServer.Stop()
				}
				return errors.Join(httpErr, errors.New("local telemetry service shutdown timed out"))
			}
		}
		return httpErr
	}
}

func RunMCP(databasePath string, in *os.File, out *os.File) error {
	return RunMCPWithProjectRoot(databasePath, "", in, out)
}

func RunMCPWithProjectRoot(databasePath, projectRoot string, in *os.File, out *os.File) error {
	paths, err := appdata.Resolve(databasePath, "")
	if err != nil {
		return err
	}
	procedureStore, err := store.Open(paths.SQLite)
	if err != nil {
		return err
	}
	defer procedureStore.Close()
	var memoryStore *projectfiles.Store
	var publishedProcedures *procedurefiles.Store
	resolvedProjectRoot := ""
	if strings.TrimSpace(projectRoot) != "" {
		memoryStore, err = projectfiles.New(projectRoot)
		if err != nil {
			return fmt.Errorf("open version-controlled project memory: %w", err)
		}
		publishedProcedures, err = procedurefiles.New(projectRoot)
		if err != nil {
			return fmt.Errorf("open version-controlled project procedures: %w", err)
		}
		resolvedProjectRoot, err = filepath.Abs(projectRoot)
		if err != nil {
			return fmt.Errorf("resolve project root for local procedure freshness: %w", err)
		}
		resolvedProjectRoot, err = filepath.EvalSymlinks(resolvedProjectRoot)
		if err != nil {
			return fmt.Errorf("resolve project root for local procedure freshness: %w", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mcp.NewWithProjectStores(procedureStore, memoryStore, publishedProcedures, resolvedProjectRoot).ServeStdio(ctx, in, out)
}

func IsLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
