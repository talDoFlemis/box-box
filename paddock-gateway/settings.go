package main

import (
	"bytes"
	_ "embed"
	"log"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

type Environment string

//go:embed base.yaml
var baseConfig []byte

type CORSSettings struct {
	Origins []string `mapstructure:"origins" validate:"min=1,dive,url"`
	Methods []string `mapstructure:"methods" validate:"min=1,dive,oneof=GET POST PUT DELETE OPTIONS PATCH HEAD"`
	Headers []string `mapstructure:"headers" validate:"min=1,dive,baseheader"`
}

type HTTPSettings struct {
	Port   string       `mapstructure:"port" validate:"required,numeric"`
	Prefix string       `mapstructure:"prefix" validate:"required"`
	IP     string       `mapstructure:"ip" validate:"required,ip"`
	CORS   CORSSettings `mapstructure:"cors" validate:"required"`
}

type ObservabilitySettings struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint" validate:"required_if=Enabled true,url"`
}

type AppSettings struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
	Env     string `mapstructure:"env"`
}

type OpenTelemetryLogSettings struct {
	TimeoutInSec  int64 `mapstructure:"timeout"`
	IntervalInSec int64 `mapstructure:"interval"`
	MaxQueueSize  int   `mapstructure:"maxqueuesize"`
	BatchSize     int   `mapstructure:"batchsize"`
}

type OpenTelemetryTraceSettings struct {
	TimeoutInSec int64 `mapstructure:"timeout"`
	MaxQueueSize int   `mapstructure:"maxqueuesize"`
	BatchSize    int   `mapstructure:"batchsize"`
	SampleRate   int   `mapstructure:"samplerate"`
}

type OpenTelemetryMetricSettings struct {
	IntervalInSec int64 `mapstructure:"interval"`
	TimeoutInSec  int64 `mapstructure:"timeout"`
}

type OpenTelemetrySettings struct {
	Enabled  bool                        `mapstructure:"enabled"`
	Endpoint string                      `mapstructure:"endpoint"`
	Metrics  OpenTelemetryMetricSettings `mapstructure:"metrics"`
	Traces   OpenTelemetryTraceSettings  `mapstructure:"traces"`
	Logs     OpenTelemetryLogSettings    `mapstructure:"logs"`
	Interval int                         `mapstructure:"interval"`
}

type Settings struct {
	App           AppSettings           `mapstructure:"app" validate:"required"`
	HTTP          HTTPSettings          `mapstructure:"http" validate:"required"`
	OpenTelemetry OpenTelemetrySettings `mapstructure:"opentelemetry" validate:"required"`
}

func LoadConfig() (*Settings, error) {
	var cfg *Settings

	viper.SetConfigType("yaml")
	err := viper.ReadConfig(bytes.NewReader(baseConfig))
	if err != nil {
		log.Println("Failed to read config from yaml")
		return nil, err
	}

	viper.SetEnvPrefix("PADDOCKGATEWAY")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", ""))
	viper.AutomaticEnv()

	err = viper.Unmarshal(&cfg)
	if err != nil {
		return nil, err
	}

	validate := validator.New()
	allowedHeaders := map[string]struct{}{
		"Accept": {}, "Authorization": {}, "Content-Type": {}, "X-CSRF-Token": {},
	}
	validate.RegisterValidation("baseheader", func(fl validator.FieldLevel) bool {
		header := fl.Field().String()
		_, ok := allowedHeaders[header]
		return ok
	})
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
