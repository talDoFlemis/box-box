package main

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/attribute"
)

type MainHandler struct{}

func NewMainHandler(e *echo.Echo, settings *Settings) *MainHandler {
	logger := slog.Default()
	e.HideBanner = true
	e.Use(slogecho.New(logger))
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: settings.HTTP.CORS.Origins,
		AllowMethods: settings.HTTP.CORS.Methods,
		AllowHeaders: settings.HTTP.CORS.Headers,
	}))
	e.Use(otelecho.Middleware("paddock-gateway",
		otelecho.WithMetricAttributeFn(func(r *http.Request) []attribute.KeyValue {
			return []attribute.KeyValue{
				attribute.String("client.ip", r.RemoteAddr),
				attribute.String("user.agent", r.UserAgent()),
			}
		}),
		otelecho.WithEchoMetricAttributeFn(func(c echo.Context) []attribute.KeyValue {
			return []attribute.KeyValue{
				attribute.String("handler.path", c.Path()),
				attribute.String("handler.method", c.Request().Method),
			}
		}),
	))

	handler := &MainHandler{}

	e.GET("/health", handler.HealthCheck)

	return handler
}

func (h *MainHandler) HealthCheck(c echo.Context) error {
	return c.String(200, "OK")
}
