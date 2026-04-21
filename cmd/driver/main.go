// Command driver starts the OpenShell compute driver for OpenShift. It listens
// on a Unix domain socket and serves the ComputeDriver gRPC service that the
// OpenShell gateway connects to.
package main

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"github.com/zanetworker/openshell-driver-openshift/internal/driver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	socketPath := flag.String("socket", "/var/run/openshell-driver.sock",
		"Unix domain socket path for the gRPC server")
	namespace := flag.String("namespace", "openshell-system",
		"Kubernetes namespace where sandboxes are provisioned")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Clean up stale socket from a previous run.
	os.Remove(*socketPath)

	lis, err := net.Listen("unix", *socketPath)
	if err != nil {
		logger.Error("failed to listen", "socket", *socketPath, "error", err)
		os.Exit(1)
	}

	d, err := driver.New(*namespace, logger)
	if err != nil {
		logger.Error("failed to initialize driver", "error", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	pb.RegisterComputeDriverServer(srv, d)
	reflection.Register(srv)

	// Graceful shutdown on SIGTERM/SIGINT.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		s := <-sig
		logger.Info("received signal, shutting down", "signal", s)
		srv.GracefulStop()
	}()

	logger.Info("driver listening", "socket", *socketPath, "namespace", *namespace)
	if err := srv.Serve(lis); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}
