package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

type NATSOrderPubSubber struct {
	nc          *nats.Conn
	subject     string
	subs        map[*websocket.Conn]*nats.Subscription
	channelSize int
}

var _ OrderPubSubber = (*NATSOrderPubSubber)(nil)

func NewNATSOrderPubSubber(nc *nats.Conn, subject string) *NATSOrderPubSubber {
	return &NATSOrderPubSubber{
		nc:      nc,
		subject: subject,
		subs:    make(map[*websocket.Conn]*nats.Subscription),
	}
}

func (n *NATSOrderPubSubber) PubOrder(ctx context.Context, order Order) error {
	propagator := otel.GetTextMapPropagator()
	msg := &nats.Msg{
		Subject: n.subject,
		Header:  nats.Header{},
	}
	propagator.Inject(ctx, propagation.HeaderCarrier(msg.Header))
	data, err := json.Marshal(order)
	if err != nil {
		return err
	}
	msg.Data = data
	return n.nc.PublishMsg(msg)
}

// SubLiveOrders implements OrderPubSubber.
func (n *NATSOrderPubSubber) SubLiveOrders(ctx context.Context, ws *websocket.Conn) (<-chan Order, error) {
	ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.SubLiveOrders")
	defer span.End()

	propagator := otel.GetTextMapPropagator()

	orderCh := make(chan Order, n.channelSize)
	sub, err := n.nc.Subscribe(n.subject, func(msg *nats.Msg) {
		ctx = propagator.Extract(ctx, propagation.HeaderCarrier(msg.Header))
		var order Order

		err := json.Unmarshal(msg.Data, &order)
		if err != nil {
			slog.ErrorContext(ctx, "failed to unmarshal order from NATS message", "error", err)
			return
		}

		orderCh <- order
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to subscribe to NATS subject", "subject", n.subject, "error", err)
		span.SetStatus(codes.Error, "failed to subscribe to NATS subject")
		span.RecordError(err)
		return nil, err
	}

	n.subs[ws] = sub

	return orderCh, nil
}

// UnsubLiveOrders implements OrderPubSubber.
func (n *NATSOrderPubSubber) UnsubLiveOrders(ctx context.Context, ws *websocket.Conn) error {
	ctx, span := tracer.Start(ctx, "NATSOrderPubSubber.UnsubLiveOrders")
	defer span.End()

	slog.InfoContext(ctx, "unsubscribing from live orders")

	sub, ok := n.subs[ws]
	if !ok {
		slog.WarnContext(ctx, "no subscription found for websocket connection")
		return nil
	}

	sub.Unsubscribe()
	delete(n.subs, ws)

	return nil
}
