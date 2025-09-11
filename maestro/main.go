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
	settings, err := LoadConfig()
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
		err = errors.Join(err, otelShutdown(ctx))
		if err != nil {
			slog.ErrorContext(
				ctx,
				"failed to shutdown opentelemetry providers",
				slog.Any("err", err),
			)
			retcode = 1
		}
	}()

	slog.InfoContext(ctx, "Connecting to NATS server")
	nc, err := settings.Nats.GetNatsClient()
	if err != nil {
		slog.ErrorContext(ctx, "failed to connect to NATS server", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Creating gRPC server")
	server := pacchetto.CreateGRPCServer()
	healthcheck := health.NewServer()
	healthgrpc.RegisterHealthServer(server, healthcheck)
	maestroHandler := newMaestroHandlerV1()
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

	slog.InfoContext(ctx, "Starting to listen to new orders")
	go func() {
		newOrderListener := newOrderListener(nc, "orders.new", "ORDERS")
		err := newOrderListener.ListenToOrders(ctx, maestroHandler.processNewOrder)
		if err != nil {
			slog.ErrorContext(ctx, "failed to listen to new orders", slog.Any("err", err))
			errChan <- err
		}
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
