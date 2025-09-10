package main

import (
	"context"
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var tracer = otel.Tracer("paddock-gateway/handler")

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type OrderPubSubber interface {
	PubOrder(ctx context.Context, order Order) error
	SubLiveOrders(ctx context.Context, ws *websocket.Conn) (<-chan Order, error)
	UnsubLiveOrders(ctx context.Context, ws *websocket.Conn) error
}

type GoChannelOrderPubSubber struct {
	liveEventSubscribers map[*websocket.Conn]chan Order
	mu                   sync.Mutex
}

func NewGoChannelOrderPubSubber() *GoChannelOrderPubSubber {
	return &GoChannelOrderPubSubber{
		liveEventSubscribers: make(map[*websocket.Conn]chan Order),
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

// SubLiveOrders implements OrderPubSubber.
func (g *GoChannelOrderPubSubber) SubLiveOrders(ctx context.Context, ws *websocket.Conn) (<-chan Order, error) {
	ctx, span := tracer.Start(ctx, "GoChannelOrderPubSubber.SubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "subscribing to live orders")

	ch := make(chan Order)

	g.liveEventSubscribers[ws] = ch

	return ch, nil
}

// UnsubLiveOrders implements OrderPubSubber.
func (g *GoChannelOrderPubSubber) UnsubLiveOrders(ctx context.Context, ws *websocket.Conn) error {
	ctx, span := tracer.Start(ctx, "GoChannelOrderPubSubber.UnsubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "unsubscribing from live orders")

	delete(g.liveEventSubscribers, ws)

	return nil
}

type MainHandler struct {
	orders         map[string]Order
	orderPubSubber OrderPubSubber
}

func NewMainHandler(e *echo.Echo, settings *Settings, orderPubSubber OrderPubSubber) *MainHandler {
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
		orders:         make(map[string]Order),
		orderPubSubber: orderPubSubber,
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

	newOrder := Order{
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

	h.orderPubSubber.PubOrder(c.Request().Context(), newOrder)

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

	ch, err := h.orderPubSubber.SubLiveOrders(ctx, ws)
	if err != nil {
		slog.ErrorContext(ctx, "failed to subscribe to live orders", slog.String("error", err.Error()))
		return err
	}

	defer func() {
		defer h.orderPubSubber.UnsubLiveOrders(ctx, ws)
		ws.Close()
	}()

	slog.DebugContext(ctx, "websocket connection established")

	for {
		slog.DebugContext(ctx, "listening for new orders")

		resp := <-ch
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
