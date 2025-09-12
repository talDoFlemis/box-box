package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	v1Pb "github.com/taldoflemis/box-box/maestro/v1"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	panettierev1pb "github.com/taldoflemis/box-box/panettiere/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Order struct {
	Size        string    `json:"size"`
	Toppings    []string  `json:"toppings"`
	Destination string    `json:"destination"`
	Username    string    `json:"username"`
	OrderedAt   time.Time `json:"ordered_at"`
	OrderID     string    `json:"order_id"`
	Status      string    `json:"status"` // e.g., "pending", "in_progress", "completed"
}

type NewOrderListener interface {
	ListenToOrders(ctx context.Context, callback func(ctx context.Context, order Order) error) error
}

type NATSNewOrderListener struct {
	nc         *nats.Conn
	subject    string
	streamName string
}

func newOrderListener(nc *nats.Conn, subject, streamName string) NewOrderListener {
	return &NATSNewOrderListener{
		nc:         nc,
		subject:    subject,
		streamName: streamName,
	}
}

var _ NewOrderListener = (*NATSNewOrderListener)(nil)

// ListenToOrders implements NewOrderListener.
func (n *NATSNewOrderListener) ListenToOrders(ctx context.Context, callback func(ctx context.Context, order Order) error) error {
	js, err := jetstream.New(n.nc)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create jetstream context", slog.Any("err", err))
		return err
	}

	stream, err := js.Stream(ctx, n.streamName)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get stream", slog.Any("err", err))
		return err
	}

	c, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       n.streamName + "maestro_new_order_listener",
		FilterSubject: n.subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})

	// TODO: Maybe use fetch here?
	_, err = c.Consume(func(msg jetstream.Msg) {
		ctx := telemetry.GetContextFromJetstreamMsg(msg)
		ctx, span := tracer.Start(ctx, "NATSNewOrderListener.Consume")
		defer span.End()

		var order Order

		err := json.Unmarshal(msg.Data(), &order)
		if err != nil {
			slog.ErrorContext(ctx, "failed to unmarshal order from NATS message", slog.Any("err", err))
			span.RecordError(err)
			return
		}

		err = callback(ctx, order)
		if err != nil {
			return
		}

		slog.DebugContext(ctx, "Acknowledging message")

		msg.Ack()
	})

	return nil
}

type maestroHandlerV1 struct {
	v1Pb.UnimplementedMaestroServiceServer
	panettiereClient panettierev1pb.PanettiereServiceClient
}

var tracer = otel.Tracer("maestro")

func newMaestroHandlerV1(panettiereClient panettierev1pb.PanettiereServiceClient) *maestroHandlerV1 {
	return &maestroHandlerV1{
		panettiereClient: panettiereClient,
	}
}

var _ v1Pb.MaestroServiceServer = (*maestroHandlerV1)(nil)

// SayHello implements v1.MaestroServiceServer.
func (m *maestroHandlerV1) SayHello(ctx context.Context, req *v1Pb.HelloRequest) (*v1Pb.HelloReply, error) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.SayHello")
	defer span.End()

	slog.InfoContext(ctx, "Received request", slog.Any("req", req))

	return &v1Pb.HelloReply{
		Message: "Hello " + req.Name,
	}, nil
}

func (m *maestroHandlerV1) processNewOrder(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.processNewOrder", trace.WithAttributes(
		attribute.String("box-box.orderid", order.OrderID),
		attribute.String("order.size", order.Size),
		attribute.String("order.destination", order.Destination),
		attribute.String("order.username", order.Username),
		attribute.StringSlice("order.toppings", order.Toppings),
	))
	defer span.End()

	slog.DebugContext(ctx, "Processing newjhjh order", slog.Any("order", order))

	doughResponse, err := m.requestDough(ctx, order)
	if err != nil {
		slog.ErrorContext(ctx, "failed to request dough", slog.String("order-id", order.OrderID), slog.Any("err", err))
	}

	slog.DebugContext(ctx, "Dough response", slog.Any("doughResponse", doughResponse))

	slog.InfoContext(ctx, "Order processed successfully", slog.String("order-id", order.OrderID))

	m.smoke(ctx, order)

	return nil
}

func (m *maestroHandlerV1) smoke(ctx context.Context, order Order) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.smoke", trace.WithAttributes(
		attribute.String("box-box.orderid", order.OrderID),
	))
	defer span.End()

	slog.DebugContext(ctx, "Starting smoking after order", slog.String("order-id", order.OrderID))

	m.isSmoking = true
	m.status = "smoking"

	sleepDuration := time.Duration(m.settings.SmokingDurationInSeconds) * time.Second

	hasOversmoked := pacchetto.RandomFunction(uint64(time.Now().UnixNano()), m.settings.ProbabilityOfOversmoking)
	if hasOversmoked {
		sleepDuration := time.Duration(float64(m.settings.SmokingDurationInSeconds)*m.settings.OversmokingFactor) * time.Second
		slog.DebugContext(ctx, "Maestro has oversmoked the pizza", slog.String("order-id", order.OrderID), slog.Float64("oversmoking-factor", m.settings.OversmokingFactor), slog.Duration("new-sleep-duration", sleepDuration))
		span.SetAttributes(attribute.Bool("maestro.oversmoked", true), attribute.Float64("maestro.oversmoking-factor", m.settings.OversmokingFactor), attribute.String("maestro.new-sleep-duration", sleepDuration.String()))
	}

	time.Sleep(sleepDuration)

	slog.InfoContext(ctx, "Finished smoking after order", slog.String("order-id", order.OrderID))
}

func (m *maestroHandlerV1) requestDough(ctx context.Context, order Order) (*panettierev1pb.DoughResponse, error) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.requestDough", trace.WithAttributes(
		attribute.String("box-box.orderid", order.OrderID),
		attribute.String("order.size", order.Size),
		attribute.String("order.destination", order.Destination),
		attribute.String("order.username", order.Username),
		attribute.StringSlice("order.toppings", order.Toppings),
	))
	defer span.End()

	slog.DebugContext(ctx, "Requesting dough from panettiere", slog.Any("order", order))

	doughRequest := &panettierev1pb.DoughRequest{
		OrderId: order.OrderID,
		Border:  panettierev1pb.BorderKind_NoBorder,
		Size:    panettierev1pb.PizzaSize_Small,
	}

	doughResponse, err := m.panettiereClient.MakeDough(ctx, doughRequest)
	if err != nil {
		slog.ErrorContext(ctx, "failed to make dough", slog.String("order-id", order.OrderID), slog.Any("err", err))
		span.RecordError(err)
		return nil, err
	}

	slog.InfoContext(ctx, "Received dough from panettiere", slog.String("order-id", order.OrderID), slog.String("dough-content", doughResponse.Content))
	return doughResponse, nil
}

func parsePizzaSize(size string) (panettierev1pb.PizzaSize, error) {
	switch strings.ToLower(size) {
	case "small":
		return panettierev1pb.PizzaSize_Small, nil
	case "medium":
		return panettierev1pb.PizzaSize_Medium, nil
	case "large":
		return panettierev1pb.PizzaSize_Large, nil
	default:
		return panettierev1pb.PizzaSize_Small, fmt.Errorf("unknown pizza size: %s", size)
	}
}
