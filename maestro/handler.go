package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	v1Pb "github.com/taldoflemis/box-box/maestro/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
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
		propagator := otel.GetTextMapPropagator()
		ctx := propagator.Extract(context.Background(), propagation.HeaderCarrier(msg.Headers()))
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
}

var tracer = otel.Tracer("maestro")

var _ v1Pb.MaestroServiceServer = (*maestroHandlerV1)(nil)

func newMaestroHandlerV1() *maestroHandlerV1 {
	return &maestroHandlerV1{}
}

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
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.processNewOrder")
	defer span.End()

	slog.DebugContext(ctx, "Processing new order", slog.Any("order", order))

	return nil
}
