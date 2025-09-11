package main

import (
	_ "embed"

	"github.com/taldoflemis/box-box/pacchetto"
)

type PanettiereSettings struct {
	PeriodBetweenSleepInSeconds        int     `mapstructure:"period-between-sleep-in-seconds" validate:"required,min=30"`
	SleepDurationInSeconds             int     `mapstructure:"sleep-duration-in-seconds" validate:"required,min=10"`
	ProbabilityOfOversleeping          float64 `mapstructure:"probability-of-oversleeping" validate:"required,min=0,max=1"`
	OversleepingFactor                 float64 `mapstructure:"oversleeping-factor" validate:"required,min=1,max=3"`
	TimeToMakeADoughInSeconds          int     `mapstructure:"time-to-make-a-dough-in-seconds" validate:"required,min=1"`
	VarianceInDoughMakeInSecondsFactor float64 `mapstructure:"variance-in-dough-make-in-seconds" validate:"required,min=0.5,max=2"`
}

type Settings struct {
	App           pacchetto.AppSettings           `mapstructure:"app" validate:"required"`
	Panettiere    PanettiereSettings              `mapstructure:"panettiere" validate:"required"`
	OpenTelemetry pacchetto.OpenTelemetrySettings `mapstructure:"opentelemetry" validate:"required"`
	GRPCServer    pacchetto.GRPCServerSettings    `mapstructure:"grpc-server" validate:"required"`
}
