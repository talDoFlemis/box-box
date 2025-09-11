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
	PanettiereEndpoint string `mapstructure:"panettiere-endpoint" validate:"required"`
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
