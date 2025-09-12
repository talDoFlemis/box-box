package main

import (
	"bytes"
	"log"
	"strings"

	_ "embed"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
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
}

type Settings struct {
	App           pacchetto.AppSettings           `mapstructure:"app" validate:"required"`
	Maestro       MaestroSettings                 `mapstructure:"maestro" validate:"required"`
	Nats          pacchetto.NatsSettings          `mapstructure:"nats" validate:"required"`
	OpenTelemetry pacchetto.OpenTelemetrySettings `mapstructure:"opentelemetry" validate:"required"`
	GRPCServer    pacchetto.GRPCServerSettings    `mapstructure:"grpc-server" validate:"required"`
}

func LoadConfig() (*Settings, error) {
	var cfg *Settings

	viper.SetConfigType("yaml")
	err := viper.ReadConfig(bytes.NewReader(baseConfig))
	if err != nil {
		log.Println("Failed to read config from yaml")
		return nil, err
	}

	viper.SetEnvPrefix("MAESTRO")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", ""))
	viper.AutomaticEnv()

	err = viper.Unmarshal(&cfg)
	if err != nil {
		return nil, err
	}

	validate := validator.New()
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
