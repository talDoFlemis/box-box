package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	maestrov1pb "github.com/taldoflemis/box-box/maestro/v1"
	"github.com/taldoflemis/box-box/pacchetto"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	panettierev1pb "github.com/taldoflemis/box-box/panettiere/v1"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	retcode := 0
	defer func() {
		os.Exit(retcode)
	}()

	slog.InfoContext(ctx, "Launching el-maestro")

	slog.InfoContext(ctx, "Loading config")
	settings, err := pacchetto.LoadConfig[Settings]("MAESTRO", baseConfig)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load config", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Setting up opentelemetry")
	otelShutdown, err := telemetry.SetupOTelSDK(ctx, settings.App, settings.OpenTelemetry)
	if err != nil {
		slog.Error("failed to setup telemetry", slog.Any("err", err))
		retcode = 1
		return
	}

	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
		if err != nil {
			slog.ErrorContext(
				ctx,
				"failed to shutdown opentelemetry providers",
				slog.Any("err", err),
			)
			retcode = 1
		}
	}()

	slog.InfoContext(ctx, "Maestro settings", slog.Any("settings", settings.Maestro))

	slog.InfoContext(ctx, "Connecting to NATS server")
	nc, err := settings.Nats.GetNatsClient()
	if err != nil {
		slog.ErrorContext(ctx, "failed to connect to NATS server", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Creating gRPC client to panettiere service")
	panettiereConn, err := pacchetto.CreateGRPCClient(ctx, settings.Maestro.PanettiereClient)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create panettiere gRPC client", slog.Any("err", err))
		retcode = 1
		return
	}
	defer panettiereConn.Close()

	panettiereClient := panettierev1pb.NewPanettiereServiceClient(panettiereConn)

	streamName := "ORDERS"
	subject := "orders"
	healthcheck := health.NewServer()
	maestroHandler, err := newMaestroHandlerV1(settings.Maestro, panettiereClient, nc, streamName, subject, healthcheck)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create maestro handler", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Creating gRPC server")
	server := pacchetto.CreateGRPCServer()
	healthgrpc.RegisterHealthServer(server, healthcheck)
	maestrov1pb.RegisterMaestroServiceServer(server, maestroHandler)

	if settings.GRPCServer.EnableReflection {
		reflection.Register(server)
	}

	go func() {
		// asynchronously inspect dependencies and toggle serving status as needed
		status := healthpb.HealthCheckResponse_SERVING
		sleepDuration := time.Duration(settings.GRPCServer.AsyncHealthIntervalInSeconds) * time.Second

		system := ""

		for {
			healthcheck.SetServingStatus(system, status)

			if !nc.IsConnected() {
				status = healthpb.HealthCheckResponse_NOT_SERVING
			} else {
				status = healthpb.HealthCheckResponse_SERVING
			}

			time.Sleep(sleepDuration)
		}
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%s", settings.GRPCServer.Host, strconv.Itoa(settings.GRPCServer.Port)))
	if err != nil {
		slog.ErrorContext(ctx, "failed to listen", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Starting gRPC server", slog.Any("addr", lis.Addr()))

	errChan := make(chan error)
	go func() {
		err := server.Serve(lis)
		if err != nil {
			slog.ErrorContext(ctx, "failed to serve", slog.Any("err", err))
			errChan <- err
		}
	}()

	go func() {
		maestroHandler.startTurn(ctx)
	}()

	select {
	case err := <-errChan:
		slog.ErrorContext(ctx, "gRPC server stopped", slog.Any("err", err))
		break
	case <-ctx.Done():
		// Wait for first Signal arrives
	}

	slog.InfoContext(ctx, "Shutting down gRPC server")
	server.GracefulStop()
	slog.InfoContext(ctx, "gRPC server stopped")
}
