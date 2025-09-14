package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	healthgo "github.com/hellofresh/health-go/v5"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	slogecho "github.com/samber/slog-echo"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var tracer = otel.Tracer("paddock-gateway")

type OrderPubSubber interface {
	PubOrder(ctx context.Context, order Order) error
	SubLiveOrders(ctx context.Context, flusher http.Flusher) (<-chan Order, error)
	UnsubLiveOrders(ctx context.Context, flusher http.Flusher) error
}

type GoChannelOrderPubSubber struct {
	liveEventSubscribers map[http.Flusher]chan Order
	mu                   sync.Mutex
}

func NewGoChannelOrderPubSubber() *GoChannelOrderPubSubber {
	return &GoChannelOrderPubSubber{
		liveEventSubscribers: make(map[http.Flusher]chan Order),
	}
}

var _ OrderPubSubber = (*GoChannelOrderPubSubber)(nil)

// PubOrder implements OrderPubSubber.
func (g *GoChannelOrderPubSubber) PubOrder(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "GoChannelOrderPubSubber.PubOrder")
	defer span.End()

	slog.InfoContext(ctx, "publishing order", slog.String("order_id", order.OrderID))

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, subChan := range g.liveEventSubscribers {
		subChan <- order
	}

	return nil
}

// SubLiveOrders implements OrderPubSubber for SSE.
func (g *GoChannelOrderPubSubber) SubLiveOrders(ctx context.Context, flusher http.Flusher) (<-chan Order, error) {
	ctx, span := tracer.Start(ctx, "GoChannelOrderPubSubber.SubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "subscribing to live orders (SSE)")

	ch := make(chan Order)
	g.mu.Lock()
	g.liveEventSubscribers[flusher] = ch
	g.mu.Unlock()
	return ch, nil
}

// UnsubLiveOrders implements OrderPubSubber for SSE.
func (g *GoChannelOrderPubSubber) UnsubLiveOrders(ctx context.Context, flusher http.Flusher) error {
	ctx, span := tracer.Start(ctx, "GoChannelOrderPubSubber.UnsubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "unsubscribing from live orders (SSE)")

	g.mu.Lock()
	delete(g.liveEventSubscribers, flusher)
	g.mu.Unlock()
	return nil
}

type MainHandler struct {
	orderPubSubber OrderPubSubber
	health         *healthgo.Health
}

func NewMainHandler(e *echo.Echo, settings *Settings, orderPubSubber OrderPubSubber, health *healthgo.Health) *MainHandler {
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
		orderPubSubber: orderPubSubber,
		health:         health,
	}

	e.GET("/healthz", handler.HealthCheck)
	v1 := e.Group("/v1")

	v1.POST("/order", handler.OrderNewPizza)
	v1.GET("/order/sse", handler.GetLiveOrdersSSE)

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

	newOrder := Order{
		Size:        req.Size,
		Toppings:    req.Toppings,
		Destination: req.Destination,
		Username:    req.Username,
		OrderedAt:   time.Now(),
		OrderID:     uuid.New().String(),
		Status:      "pending",
	}

	resp := NewPizzaOrderResponse{
		OrderID:   newOrder.OrderID,
		OrderedAt: newOrder.OrderedAt,
	}

	h.orderPubSubber.PubOrder(c.Request().Context(), newOrder)

	return c.JSON(http.StatusOK, resp)
}

// GetLiveOrdersSSE godoc
//
// @Summary Get live orders via Server-Sent Events (SSE)
// @Tags order
// @Produce  text/event-stream
// @Success 200 {object} Order
// @Router /v1/orders/sse [get]
func (h *MainHandler) GetLiveOrdersSSE(c echo.Context) error {
	ctx := c.Request().Context()
	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		slog.ErrorContext(ctx, "streaming unsupported by response writer")
		return echo.NewHTTPError(http.StatusInternalServerError, "Streaming unsupported")
	}

	ch, err := h.orderPubSubber.SubLiveOrders(ctx, flusher)
	if err != nil {
		slog.ErrorContext(ctx, "failed to subscribe to live orders", slog.String("error", err.Error()))
		return err
	}

	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	notify := c.Request().Context().Done()
	for {
		select {
		case <-notify:
			slog.InfoContext(ctx, "client closed connection")
			return h.orderPubSubber.UnsubLiveOrders(ctx, flusher)
		case resp := <-ch:
			data, err := json.Marshal(resp)
			if err != nil {
				slog.ErrorContext(ctx, "marshal order for SSE", slog.String("error", err.Error()))
				continue
			}
			_, err = c.Response().Writer.Write([]byte("data: " + string(data) + "\n\n"))
			if err != nil {
				slog.ErrorContext(ctx, "write SSE", slog.String("error", err.Error()))
				h.orderPubSubber.UnsubLiveOrders(ctx, flusher)
				return err
			}
			flusher.Flush()
		}
	}
}

// HealthCheck godoc
//
// @Summary Check the health of the service
// @Tags health
// @Produce json
// @Success 200 {object} healthgo.Check
// @Failure 503 {object} healthgo.Check
// @Router /healthz [get]
func (h *MainHandler) HealthCheck(c echo.Context) error {
	check := h.health.Measure(c.Request().Context())

	statusCode := http.StatusOK
	if check.Status != healthgo.StatusOK {
		statusCode = http.StatusServiceUnavailable
	}

	return c.JSON(statusCode, check)
}
