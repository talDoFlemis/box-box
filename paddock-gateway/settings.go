package main

import (
	"bytes"
	_ "embed"
	"log"
	"strings"

	"github.com/spf13/viper"
	"github.com/go-playground/validator/v10"
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
	CORS   CORSSettings `mapstructure:"cors" validate:"required,dive"`
}

type ObservabilitySettings struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint" validate:"required_if=Enabled true,url"`
}

type Config struct {
	HTTP          HTTPSettings          `mapstructure:"http"`
	Observability ObservabilitySettings `mapstructure:"observability"`
}

func LoadConfig() (*Config, error) {
	var cfg *Config

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
		"Accept":{}, "Authorization":{}, "Content-Type":{}, "X-CSRF-Token":{},
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
