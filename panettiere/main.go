package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	"github.com/taldoflemis/box-box/pacchetto"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	panettierev1pb "github.com/taldoflemis/box-box/panettiere/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

var tracer = otel.Tracer("panettiere")

//go:embed base.yaml
var baseconfig []byte

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

	slog.InfoContext(ctx, "Launching panettiere")

	slog.InfoContext(ctx, "Loading config")
	settings, err := pacchetto.LoadConfig[Settings]("PANETTIERE", baseconfig)
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

	slog.InfoContext(ctx, "Creating gRPC server")
	server := pacchetto.CreateGRPCServer()
	healthcheck := health.NewServer()
	healthgrpc.RegisterHealthServer(server, healthcheck)
	panettiereService := newPanettiereService(settings.Panettiere)
	panettierev1pb.RegisterPanettiereServiceServer(server, panettiereService)

	// Ensure service is properly shut down
	defer panettiereService.Stop()

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

			if panettiereService.isSleeping {
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

type panettiereService struct {
	panettierev1pb.UnimplementedPanettiereServiceServer
	settings         PanettiereSettings
	status           string
	mu               sync.RWMutex
	isSleeping       bool
	isWorkingOnDough bool
	sleepTicker      *time.Ticker
	shouldSleep      bool
	ctx              context.Context
	cancel           context.CancelFunc
}

func newPanettiereService(panettiereSettings PanettiereSettings) *panettiereService {
	ctx, cancel := context.WithCancel(context.Background())

	// Seed the random number generator for variance calculations
	rand.Seed(time.Now().UnixNano())

	service := &panettiereService{
		settings: panettiereSettings,
		status:   "idle",
		ctx:      ctx,
		cancel:   cancel,
	}

	// Start the sleep ticker
	service.startSleepTicker()

	return service
}

func (p *panettiereService) startSleepTicker() {
	duration := time.Duration(p.settings.PeriodBetweenSleepInSeconds) * time.Second
	p.sleepTicker = time.NewTicker(duration)

	go func() {
		for {
			select {
			case <-p.sleepTicker.C:
				p.mu.Lock()
				if !p.isWorkingOnDough && !p.isSleeping {
					// Go to sleep immediately if not working
					p.isSleeping = true

					// Calculate sleep duration with potential oversleeping
					baseSleepDuration := time.Duration(p.settings.SleepDurationInSeconds) * time.Second
					sleepDuration := baseSleepDuration

					// Check if panettiere will oversleep
					if rand.Float64() < p.settings.ProbabilityOfOversleeping {
						sleepDuration = time.Duration(float64(baseSleepDuration) * p.settings.OversleepingFactor)
						slog.InfoContext(p.ctx, "Panettiere is oversleeping!",
							slog.Duration("planned_sleep", baseSleepDuration),
							slog.Duration("actual_sleep", sleepDuration))
					} else {
						slog.InfoContext(p.ctx, "Panettiere is sleeping",
							slog.Duration("sleep_duration", sleepDuration))
					}

					p.status = "sleeping"

					go func() {
						time.Sleep(sleepDuration)
						p.mu.Lock()
						p.isSleeping = false
						p.status = "idle"
						p.mu.Unlock()
						slog.InfoContext(p.ctx, "Panettiere woke up and is ready to work")
					}()
				} else if p.isWorkingOnDough {
					// Mark that sleep should happen after work is done
					p.shouldSleep = true
					slog.InfoContext(p.ctx, "Sleep timer triggered, panettiere should sleep after current work")
				}
				p.mu.Unlock()
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

func (p *panettiereService) checkAndSleep() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.shouldSleep && !p.isWorkingOnDough && !p.isSleeping {
		p.isSleeping = true
		p.shouldSleep = false

		// Calculate sleep duration with potential oversleeping
		baseSleepDuration := time.Duration(p.settings.SleepDurationInSeconds) * time.Second
		sleepDuration := baseSleepDuration

		// Check if panettiere will oversleep
		if rand.Float64() < p.settings.ProbabilityOfOversleeping {
			sleepDuration = time.Duration(float64(baseSleepDuration) * p.settings.OversleepingFactor)
			slog.InfoContext(p.ctx, "Panettiere is oversleeping after work!",
				slog.Duration("planned_sleep", baseSleepDuration),
				slog.Duration("actual_sleep", sleepDuration))
		} else {
			slog.InfoContext(p.ctx, "Panettiere is sleeping after work",
				slog.Duration("sleep_duration", sleepDuration))
		}

		p.status = "sleeping"

		go func() {
			time.Sleep(sleepDuration)
			p.mu.Lock()
			p.isSleeping = false
			p.status = "idle"
			p.mu.Unlock()
			slog.InfoContext(p.ctx, "Panettiere woke up and is ready to work")
		}()
	}
}

func (p *panettiereService) Stop() {
	if p.sleepTicker != nil {
		p.sleepTicker.Stop()
	}
	p.cancel()
}

// MakeDough implements v1.PanettiereServiceServer.
func (p *panettiereService) MakeDough(ctx context.Context, req *panettierev1pb.DoughRequest) (*panettierev1pb.DoughResponse, error) {
	ctx, span := tracer.Start(ctx, "panettiereService.MakeDough", trace.WithAttributes(
		attribute.String("box-box.orderid", req.OrderId),
		attribute.String("panettiere.border", panettierev1pb.BorderKind_name[int32(req.Border)]),
		attribute.String("panettiere.size", panettierev1pb.PizzaSize_name[int32(req.Size)]),
	))
	defer span.End()

	// Check if panettiere is sleeping
	p.mu.RLock()
	if p.isSleeping {
		p.mu.RUnlock()
		slog.WarnContext(ctx, "Cannot make dough: panettiere is sleeping", slog.String("order-id", req.OrderId))
		return nil, status.Errorf(codes.ResourceExhausted, "panettiere is sleeping and cannot make dough right now")
	}
	p.mu.RUnlock()

	// Mark as working on dough
	p.mu.Lock()
	p.isWorkingOnDough = true
	p.status = fmt.Sprintf("making dough of order %s", req.OrderId)
	p.mu.Unlock()

	defer func() {
		// Mark as finished working on dough and check if should sleep
		p.mu.Lock()
		p.isWorkingOnDough = false
		p.status = "idle"
		p.mu.Unlock()

		// Check if we should sleep after finishing the work
		p.checkAndSleep()
	}()

	slog.DebugContext(ctx, "Starting to make dough", slog.String("order-id", req.OrderId))

	// Calculate dough making time with variance
	baseDoughTime := time.Duration(p.settings.TimeToMakeADoughInSeconds) * time.Second
	varianceFactor := p.settings.VarianceInDoughMakeInSecondsFactor

	// Apply random variance: variance between 1/varianceFactor and varianceFactor
	// For example, if varianceFactor is 2, variance will be between 0.5x and 2x
	minFactor := 1.0 / varianceFactor
	maxFactor := varianceFactor
	randomFactor := minFactor + rand.Float64()*(maxFactor-minFactor)

	actualDoughTime := time.Duration(float64(baseDoughTime) * randomFactor)

	slog.InfoContext(ctx, "Making dough",
		slog.String("order-id", req.OrderId),
		slog.Duration("base_time", baseDoughTime),
		slog.Duration("actual_time", actualDoughTime),
		slog.Float64("variance_factor", randomFactor))

	// Simulate the actual work time for making dough
	time.Sleep(actualDoughTime)

	var content strings.Builder
	content.WriteString("Dough with ")
	content.WriteString(panettierev1pb.BorderKind_name[int32(req.Border)])
	content.WriteString(" border, size ")
	content.WriteString(panettierev1pb.PizzaSize_name[int32(req.Size)])

	slog.InfoContext(ctx, "Dough is ready", slog.String("order-id", req.OrderId), slog.String("dough", content.String()))

	return &panettierev1pb.DoughResponse{
		Content: content.String(),
	}, nil
}

// Status implements v1.PanettiereServiceServer.
func (p *panettiereService) Status(context.Context, *emptypb.Empty) (*panettierev1pb.StatusResponse, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := p.status
	if p.isSleeping {
		status = "sleeping"
	} else if p.isWorkingOnDough {
		status = p.status // Keep the detailed working status
	} else if p.shouldSleep {
		status = "should sleep after current work"
	}

	return &panettierev1pb.StatusResponse{
		Status: status,
	}, nil
}

var _ panettierev1pb.PanettiereServiceServer = (*panettiereService)(nil)
