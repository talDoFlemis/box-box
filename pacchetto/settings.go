package pacchetto

import (
	"strconv"

	"github.com/nats-io/nats.go"
)

type Environment string

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

type GRPCClientSettings struct {
	Address string `mapstructure:"address" validate:"required"`
}

type GRPCServerSettings struct {
	EnableReflection             bool   `mapstructure:"enable-reflection" validate:"required"`
	AsyncHealthIntervalInSeconds int    `mapstructure:"async-health-interval-in-seconds" validate:"required,min=5"`
	Port                         int    `mapstructure:"port" validate:"required,min=1"`
	Host                         string `mapstructure:"host" validate:"required,ip"`
}

type NatsSettings struct {
	UseCredentials bool `mapstructure:"usecredentials"`
	// Only used if UseCredentials is true
	Username string `mapstructure:"username" validate:"required_if=UseCredentials true"`
	Password string `mapstructure:"password" validate:"required_if=UseCredentials true"`
	Host     string `mapstructure:"host" validate:"required"`
	Port     int    `mapstructure:"port" validate:"required,min=1"`
}

func (n *NatsSettings) GetNatsClient() (*nats.Conn, error) {
	portStr := strconv.Itoa(n.Port)
	return nats.Connect(
		n.Host+":"+portStr,
		nats.UserInfo(n.Username, n.Password),
	)
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
