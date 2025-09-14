package main

import (
	_ "embed"

	"github.com/taldoflemis/box-box/pacchetto"
)

//go:embed base.yaml
var baseConfig []byte

type MaestroSettings struct {
	PanettiereClient            pacchetto.GRPCClientSettings `mapstructure:"panettiere-client" validate:"required"`
	SmokingDurationInSeconds    int                          `mapstructure:"smoking-duration-in-seconds" validate:"required,min=1"`
	ProbabilityOfOversmoking    float64                      `mapstructure:"probability-of-oversmoking" validate:"required,gte=0,lte=1"`
	OversmokingFactor           float64                      `mapstructure:"oversmoking-factor" validate:"required,gt=1"`
	PeriodBetweenLunchInSeconds int                          `mapstructure:"period-between-lunch-in-seconds" validate:"required,min=30"`
	LunchDurationInSeconds      int                          `mapstructure:"lunch-duration-in-seconds" validate:"required,min=1"`
	OrderBatchSize              int                          `mapstructure:"order-batch-size" validate:"required,min=1"`
	FetchMaxWaitInSeconds       int                          `mapstructure:"fetch-max-wait-in-seconds" validate:"required,min=5"`
}

type Settings struct {
	App           pacchetto.AppSettings           `mapstructure:"app" validate:"required"`
	Maestro       MaestroSettings                 `mapstructure:"maestro" validate:"required"`
	Nats          pacchetto.NatsSettings          `mapstructure:"nats" validate:"required"`
	OpenTelemetry pacchetto.OpenTelemetrySettings `mapstructure:"opentelemetry" validate:"required"`
	GRPCServer    pacchetto.GRPCServerSettings    `mapstructure:"grpc-server" validate:"required"`
}
