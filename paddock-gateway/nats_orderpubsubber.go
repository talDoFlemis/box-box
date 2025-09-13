package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

type NATSOrderPubSubber struct {
	nc          *nats.Conn
	subject     string
	streamName  string
	subs        map[http.Flusher]jetstream.ConsumeContext
	js          jetstream.JetStream
	stream      jetstream.Stream
	channelSize int
}

var _ OrderPubSubber = (*NATSOrderPubSubber)(nil)

func NewNATSOrderPubSubber(nc *nats.Conn, subject, streamName string) (*NATSOrderPubSubber, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		slog.Error("failed to create jetstream context", "error", err)
		return nil, err
	}

	stream, err := js.CreateStream(context.Background(), jetstream.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject + ".>"},
	})

	pb := &NATSOrderPubSubber{
		nc:         nc,
		subject:    subject,
		streamName: streamName,
		subs:       make(map[http.Flusher]jetstream.ConsumeContext),
		stream:     stream,
		js:         js,
	}

	return pb, nil
}

func (n *NATSOrderPubSubber) PubOrder(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.PubOrder")
	defer span.End()

	msg := &nats.Msg{
		// TODO: Change this to a waiting_payment after we create caixa and maybe higher cardinality subjects
		Subject: fmt.Sprintf("%s.waiting_to_cook.%s", n.subject, order.OrderID),
		Header:  nats.Header{},
	}

	slog.InfoContext(ctx, "Publishing order to NATS", "header", msg.Header)
	telemetry.InjectContextToNatsMsg(ctx, msg)

	data, err := json.Marshal(order)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "after", "header", msg.Header)

	msg.Data = data

	_, err = n.js.PublishMsg(ctx, msg)
	if err != nil {
		slog.InfoContext(ctx, "Failed to publish order to NATS", "error", err)
		span.SetStatus(codes.Error, "failed to publish order to NATS")
		span.RecordError(err)
		return err
	}

	slog.InfoContext(ctx, "Published order to NATS", "order_id", order.OrderID)

	return nil
}

// SubLiveOrders implements OrderPubSubber.
func (n *NATSOrderPubSubber) SubLiveOrders(ctx context.Context, flusher http.Flusher) (<-chan Order, error) {
	ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.SubLiveOrders")
	defer span.End()

	orderCh := make(chan Order, n.channelSize)
	c, err := n.stream.CreateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: n.subject + ".>",
		// We don't want to ack messages, only monitor them
		AckPolicy: jetstream.AckNonePolicy,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create or update consumer", "error", err)
		span.SetStatus(codes.Error, "failed to create or update consumer")
		span.RecordError(err)
		return nil, err
	}

	cons, err := c.Consume(func(msg jetstream.Msg) {
		propagator := otel.GetTextMapPropagator()
		ctx := propagator.Extract(context.Background(), propagation.HeaderCarrier(msg.Headers()))

		ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.Consume")
		defer span.End()

		var order Order
		err := json.Unmarshal(msg.Data(), &order)
		if err != nil {
			slog.ErrorContext(ctx, "failed to unmarshal order from NATS message", "error", err)
			span.SetStatus(codes.Error, "failed to unmarshal order from NATS message")
			span.RecordError(err)
			return
		}

		slog.InfoContext(ctx, "Received order from NATS", "order_id", order.OrderID)

		orderCh <- order
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create consumer", "error", err)
		span.SetStatus(codes.Error, "failed to create consumer")
		span.RecordError(err)
		return nil, err
	}

	n.subs[flusher] = cons

	return orderCh, nil
}

// UnsubLiveOrders implements OrderPubSubber.
func (n *NATSOrderPubSubber) UnsubLiveOrders(ctx context.Context, flusher http.Flusher) error {
	ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.UnsubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "unsubscribing from live orders")

	cons, ok := n.subs[flusher]
	if !ok {
		slog.WarnContext(ctx, "no subscription found for flusher connection")
		return nil
	}

	cons.Stop()

	delete(n.subs, flusher)

	return nil
}
