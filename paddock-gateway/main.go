package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "net/http/pprof"

	healthgo "github.com/hellofresh/health-go/v5"
	"github.com/labstack/echo-contrib/pprof"
	"github.com/labstack/echo/v4"
	echoSwagger "github.com/swaggo/echo-swagger"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	_ "github.com/taldoflemis/box-box/paddock-gateway/docs"
)

// @title						Paddock Gateway
// @version						1.0
// @host						localhost:8080
// @BasePath  					/
// @securityDefinitions.apikey	Bearer
// @in							header
// @name						Authorization
// @description					Type "Bearer" followed by a space and JWT token.
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

	slog.InfoContext(ctx, "Launching paddock-gateway")

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

	errChan := make(chan error)
	server := echo.New()
	server.HideBanner = true

	slog.InfoContext(ctx, "Connecting to NATS server")
	nc, err := settings.Nats.GetNatsClient()
	if err != nil {
		slog.ErrorContext(ctx, "failed to connect to NATS server", slog.Any("err", err))
		retcode = 1
		return
	}

	orderPubSubber, err := NewNATSOrderPubSubber(nc, "orders", "ORDERS")
	if err != nil {
		slog.ErrorContext(ctx, "failed to create order pub/subber", slog.Any("err", err))
		retcode = 1
		return
	}

	slog.InfoContext(ctx, "Setting up health checker")
	health, err := healthgo.New(
		healthgo.WithComponent(healthgo.Component{
			Name:    "paddock-gateway",
			Version: "1.0.0",
		}),
		healthgo.WithChecks(healthgo.Config{
			Name: "nats",
			Check: func(ctx context.Context) error {
				if !nc.IsConnected() {
					return errors.New("NATS connection is not active")
				}
				return nil
			},
		}),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create health checker", slog.Any("err", err))
		retcode = 1
		return
	}

	NewMainHandler(server, settings, orderPubSubber, health)
	server.GET("/swagger/*", echoSwagger.WrapHandler)
	pprof.Register(server)

	go func() {
		slog.InfoContext(ctx, "listening for requests", slog.String("ip", settings.HTTP.IP), slog.String("port", settings.HTTP.Port))
		errChan <- server.Start(fmt.Sprintf("%s:%s", settings.HTTP.IP, settings.HTTP.Port))
	}()

	select {
	case err = <-errChan:
		slog.ErrorContext(ctx, "error when running server", slog.Any("err", err))
		retcode = 1
		return
	case <-ctx.Done():
		// Wait for first Signal arrives
	}

	err = server.Shutdown(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to shutdown gracefully the server", slog.Any("err", err))
	}
}
