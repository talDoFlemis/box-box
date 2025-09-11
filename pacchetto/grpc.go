package pacchetto

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func CreateGRPCClient(ctx context.Context, cfg GRPCClientSettings) (*grpc.ClientConn, error) {
	options := make([]grpc.DialOption, 0)
	options = append(options, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))

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
