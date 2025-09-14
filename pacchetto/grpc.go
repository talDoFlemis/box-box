package pacchetto

import (
	"context"
	"log/slog"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func CreateGRPCClient(ctx context.Context, cfg GRPCClientSettings) (*grpc.ClientConn, error) {
	options := make([]grpc.DialOption, 0)
	options = append(options, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))

	retry_opts := []retry.CallOption{
		retry.WithMax(cfg.Retries),
		retry.WithCodes(codes.Unavailable, codes.ResourceExhausted),
		retry.WithBackoff(retry.BackoffExponential(time.Duration(cfg.ExponentialBackoffBaseInMilliseconds) * time.Millisecond)),
	}

	options = append(options, grpc.WithUnaryInterceptor(retry.UnaryClientInterceptor(retry_opts...)))
	options = append(options, grpc.WithStreamInterceptor(retry.StreamClientInterceptor(retry_opts...)))

	var cred grpc.DialOption

	cred = grpc.WithTransportCredentials(insecure.NewCredentials())

	options = append(options, cred)

	conn, err := grpc.NewClient(cfg.Address,
		options...,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create grpc client", slog.Any("err", err))
		return nil, err
	}

	return conn, nil
}

func CreateGRPCServer() *grpc.Server {
	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	return srv
}
