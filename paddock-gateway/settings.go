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

type Settings struct {
	App           pacchetto.AppSettings           `mapstructure:"app" validate:"required"`
	HTTP          pacchetto.HTTPSettings          `mapstructure:"http" validate:"required"`
	OpenTelemetry pacchetto.OpenTelemetrySettings `mapstructure:"opentelemetry" validate:"required"`
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
