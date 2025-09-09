package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	slogmulti "github.com/samber/slog-multi"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

// SetupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupOTelSDK(
	ctx context.Context,
	cfg Settings,
) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.App.Name),
			semconv.ServiceVersionKey.String(cfg.App.Version),
			semconv.ServiceNamespaceKey.String("diafi"),
		),
	)

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up trace provider.
	tracerProvider, err := newTraceProvider(ctx, cfg, res)
	if err != nil {
		handleErr(err)
		return nil, err
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(newPropagator())

	loggerProvider, err := newLoggerProvider(ctx, cfg, res)
	if err != nil {
		handleErr(err)
		return nil, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	meterProvider, err := newMeterProvider(ctx, cfg, res)
	if err != nil {
		handleErr(err)
		return nil, err
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	return shutdown, err
}

//nolint:ireturn
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTraceProvider(
	ctx context.Context,
	cfg Settings,
	res *resource.Resource,
) (*trace.TracerProvider, error) {
	traceProvider := trace.NewTracerProvider()

	if cfg.OpenTelemetry.Enabled {
		otelSpanExporter, err := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithEndpoint(cfg.OpenTelemetry.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}

		timeout := time.Duration(cfg.OpenTelemetry.Traces.TimeoutInSec) * time.Second
		sampler := trace.ParentBased(
			trace.TraceIDRatioBased(float64(cfg.OpenTelemetry.Traces.SampleRate)),
		)

		traceProvider = trace.NewTracerProvider(
			trace.WithBatcher(otelSpanExporter,
				trace.WithBatchTimeout(timeout),
				trace.WithMaxQueueSize(cfg.OpenTelemetry.Traces.MaxQueueSize),
				trace.WithMaxExportBatchSize(cfg.OpenTelemetry.Traces.BatchSize),
			),
			trace.WithSampler(sampler),
			trace.WithResource(res),
		)
	}

	return traceProvider, nil
}

func newLoggerProvider(
	ctx context.Context,
	cfg Settings,
	res *resource.Resource,
) (*log.LoggerProvider, error) {
	provider := log.NewLoggerProvider()

	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
	})

	errorFormattingMiddleware := slogmulti.NewHandleInlineMiddleware(errorFormattingMiddleware)

	// Set handler pipeline for logging custom attributes like user.id and errors
	handlerPipeline := slogmulti.Pipe(errorFormattingMiddleware)

	if !cfg.OpenTelemetry.Enabled {
		slog.SetDefault(slog.New(handlerPipeline.Handler(jsonHandler)))
		return provider, nil
	}

	otlpExporter, err := otlploggrpc.New(
		ctx,
		otlploggrpc.WithEndpoint(cfg.OpenTelemetry.Endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	interval := time.Duration(cfg.OpenTelemetry.Logs.IntervalInSec) * time.Second
	timeout := time.Duration(cfg.OpenTelemetry.Logs.TimeoutInSec) * time.Second

	processor := log.NewBatchProcessor(otlpExporter,
		log.WithMaxQueueSize(cfg.OpenTelemetry.Logs.MaxQueueSize),
		log.WithExportMaxBatchSize(cfg.OpenTelemetry.Logs.BatchSize),
		log.WithExportTimeout(timeout),
		log.WithExportInterval(interval),
	)
	loggerProvider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(processor),
	)

	// Here we bridge the OpenTelemetry logger to the slog logger.
	// If we want to change the actual logger we must use another bridge
	otelLogHandler := otelslog.NewHandler(
		cfg.App.Name,
		otelslog.WithLoggerProvider(loggerProvider),
		otelslog.WithVersion(cfg.App.Version),
		otelslog.WithSource(true),
	)

	// Set default logger
	logger := slog.New(handlerPipeline.Handler(slogmulti.Fanout(jsonHandler, otelLogHandler)))
	slog.SetDefault(logger)

	logger.InfoContext(ctx, "Logger initialized")

	return provider, nil
}

func newMeterProvider(
	ctx context.Context,
	cfg Settings,
	res *resource.Resource,
) (*metric.MeterProvider, error) {
	// Initialize with noop meter provider
	meterProvider := metric.NewMeterProvider()

	if cfg.OpenTelemetry.Enabled {
		otlpExporter, err := otlpmetricgrpc.New(
			ctx,
			otlpmetricgrpc.WithEndpoint(cfg.OpenTelemetry.Endpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}

		interval := time.Duration(cfg.OpenTelemetry.Metrics.IntervalInSec) * time.Second
		timeout := time.Duration(cfg.OpenTelemetry.Metrics.TimeoutInSec) * time.Second

		meterProvider = metric.NewMeterProvider(
			metric.WithReader(metric.NewPeriodicReader(
				otlpExporter,
				metric.WithInterval(interval),
				metric.WithTimeout(timeout),
			)),
			metric.WithResource(res),
		)
	}

	return meterProvider, nil
}
