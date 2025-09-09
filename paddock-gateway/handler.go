package main

import (
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/attribute"
)

var upgrader = websocket.Upgrader{}

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

	e.GET("/healthz", handler.HealthCheck)
	v1 := e.Group("/v1")

	v1.GET("/order/ws", handler.GetLiveOrdersRequests)

	return handler
}

// GetLiveOrdersRequests godoc
//
// @Summary Get live orders via WebSocket
// @Tags order
// @Produce json
// @Success 200 {object} dto.BidsListDTO
// @Router /v1/order/ws [get]
func (h *MainHandler) GetLiveOrdersRequests(c echo.Context) error {
	ctx := c.Request().Context()
	// Upgrade websocket connection
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to upgrade websocket connection", slog.String("error", err.Error()))
		return err
	}
	defer ws.Close()

	slog.DebugContext(ctx, "websocket connection established")

	requests := []OrderResponse{
		{
			OrderID:     "order_1",
			Destination: "Here",
			Size:        "Large",
			Username:    "Aasdfasdf",
			Toppings: []string{
				"Calamari",
			},
			OrderedAt: time.Now(),
			Status:    "pending",
		},
	}

	for {
		// Write
		randomIndex := rand.Intn(len(requests))

		request := requests[randomIndex]
		slog.DebugContext(ctx, "writing:", slog.Any("request", request))

		err := ws.WriteJSON(request)
		if err != nil {
			slog.ErrorContext(ctx, "write:", slog.String("error", err.Error()))
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// HealthCheck godoc
//
// @Summary Check the health of the service
// @Tags health
// @Produce json
// @Success 200 {object} dto.BidsListDTO
// @Failure 503 {string} string "error"
// @Router /healthz [get]
func (h *MainHandler) HealthCheck(c echo.Context) error {
	return c.String(200, "OK")
}
