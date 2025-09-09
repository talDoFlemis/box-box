package main

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/attribute"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type MainHandler struct {
	orders               map[string]OrderResponse
	liveEventSubscribers map[*websocket.Conn]chan OrderResponse
	mu                   sync.Mutex
}

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

	handler := &MainHandler{
		orders:               make(map[string]OrderResponse),
		liveEventSubscribers: make(map[*websocket.Conn]chan OrderResponse),
	}

	e.GET("/healthz", handler.HealthCheck)
	v1 := e.Group("/v1")

	v1.POST("/order", handler.OrderNewPizza)
	v1.GET("/order/ws", handler.GetLiveOrdersRequests)

	return handler
}

// OrderNewPizza godoc
//
// @Summary Create a new pizza order
// @Tags order
// @Accept json
// @Produce json
// @Param order body NewPizzaOrderRequest true "New Pizza Order Request"
// @Success 200 {object} NewPizzaOrderResponse
// @Failure 422 {string} string "error"
// @Router /v1/order [post]
func (h *MainHandler) OrderNewPizza(c echo.Context) error {
	ctx := c.Request().Context()

	var req NewPizzaOrderRequest
	err := c.Bind(&req)
	if err != nil {
		slog.ErrorContext(ctx, "failed to bind request", slog.String("error", err.Error()))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	newOrder := OrderResponse{
		Size:        req.Size,
		Toppings:    req.Toppings,
		Destination: req.Destination,
		Username:    req.Username,
		OrderedAt:   time.Now(),
		OrderID:     uuid.New().String(),
		Status:      "pending",
	}

	h.orders[newOrder.OrderID] = newOrder

	resp := NewPizzaOrderResponse{
		OrderID:   newOrder.OrderID,
		OrderedAt: newOrder.OrderedAt,
	}

	for _, subChan := range h.liveEventSubscribers {
		subChan <- newOrder
	}

	return c.JSON(http.StatusOK, resp)
}

// GetLiveOrdersRequests godoc
//
// @Summary Get live orders via WebSocket
// @Tags order
// @Produce json
// @Success 200 {object} OrderResponse
// @Router /v1/order/ws [get]
func (h *MainHandler) GetLiveOrdersRequests(c echo.Context) error {
	ctx := c.Request().Context()
	// Upgrade websocket connection
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to upgrade websocket connection", slog.String("error", err.Error()))
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.liveEventSubscribers[ws] = make(chan OrderResponse)

	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.liveEventSubscribers[ws]; ok {
			delete(h.liveEventSubscribers, ws)
			slog.DebugContext(ctx, "removed websocket client")
		}

		ws.Close()
	}()

	slog.DebugContext(ctx, "websocket connection established")

	for {
		slog.DebugContext(ctx, "listening for new orders")

		resp := <-h.liveEventSubscribers[ws]
		err := ws.WriteJSON(resp)
		if err != nil {
			slog.ErrorContext(ctx, "write:", slog.String("error", err.Error()))
			return err
		}
	}
}

// HealthCheck godoc
//
// @Summary Check the health of the service
// @Tags health
// @Produce json
// @Failure 503 {string} string "error"
// @Router /healthz [get]
func (h *MainHandler) HealthCheck(c echo.Context) error {
	return c.String(200, "OK")
}
