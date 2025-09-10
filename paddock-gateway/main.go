package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/nats-io/nats.go"
	echoSwagger "github.com/swaggo/echo-swagger"
	_ "github.com/taldoflemis/box-box/paddock-gateway/docs"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
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

	settings, err := LoadConfig()
	if err != nil {
		slog.ErrorContext(ctx, "failed to load config", slog.Any("err", err))
		retcode = 1
		return
	}

	otelShutdown, err := SetupOTelSDK(ctx, *settings)
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

	if settings.OpenTelemetry.Enabled {
		interval := time.Duration(settings.OpenTelemetry.Interval) * time.Second
		err = runtime.Start(runtime.WithMinimumReadMemStatsInterval(interval))
		if err != nil {
			slog.Error("failed to start runtime collector", slog.Any("error", err))
			retcode = 1
			return
		}
	}

	errChan := make(chan error)
	server := echo.New()
	server.HideBanner = true

	nc, _ := nats.Connect(nats.DefaultURL)

	orderPubSubber, err := NewNATSOrderPubSubber(nc, "orders", "ORDERS")
	if err != nil {
		slog.ErrorContext(ctx, "failed to create order pub/subber", slog.Any("err", err))
		retcode = 1
		return
	}

	NewMainHandler(server, settings, orderPubSubber)
	server.GET("/swagger/*", echoSwagger.WrapHandler)

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
