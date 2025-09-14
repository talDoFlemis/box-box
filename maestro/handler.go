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
	"github.com/taldoflemis/box-box/pacchetto"
	"github.com/taldoflemis/box-box/pacchetto/telemetry"
	panettierev1pb "github.com/taldoflemis/box-box/panettiere/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
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

type maestroHandlerV1 struct {
	v1Pb.UnimplementedMaestroServiceServer
	panettiereClient panettierev1pb.PanettiereServiceClient
	isSmoking        bool
	status           string
	settings         MaestroSettings
	isLunching       bool
	subject          string
	consumer         jetstream.Consumer
	jsClient         jetstream.JetStream
	lunchCounter     metric.Int64Counter
	lunchHistogram   metric.Float64Histogram
	smokeCounter     metric.Int64Counter
	smokeHistogram   metric.Float64Histogram
}

var (
	tracer = otel.Tracer("maestro")
	meter  = otel.Meter("maestro")
)

func newMaestroHandlerV1(settings MaestroSettings,
	panettiereClient panettierev1pb.PanettiereServiceClient,
	nc *nats.Conn,
	streamName string,
	subject string,
) (*maestroHandlerV1, error) {
	ctx := context.Background()

	lunchCounter, err := meter.Int64Counter(
		"maestro.lunch.count",
		metric.WithDescription("Number of lunch cycles the maestro has taken"),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create sleep counter", slog.Any("err", err))
		return nil, err
	}

	lunchHistogram, err := meter.Float64Histogram(
		"maestro.lunch.duration",
		metric.WithDescription("Duration of lunch cycles the maestro has taken"),
		metric.WithUnit("s"),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create sleep histogram", slog.Any("err", err))
		return nil, err
	}

	smokeCounter, err := meter.Int64Counter(
		"maestro.smoke.count",
		metric.WithDescription("Number of smokes the maestro has taken"),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create smoke counter", slog.Any("err", err))
		return nil, err
	}

	smokeHistogram, err := meter.Float64Histogram(
		"maestro.smoke.duration",
		metric.WithDescription("Duration of smokes the maestro has taken"),
		metric.WithUnit("s"),
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create smoke histogram", slog.Any("err", err))
		return nil, err
	}

	js, err := jetstream.New(nc)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create jetstream context", slog.Any("err", err))
		return nil, err
	}

	stream, err := js.Stream(ctx, streamName)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get stream", slog.Any("err", err))
		return nil, err
	}

	c, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       streamName + "_maestro_new_order_listener_v1",
		FilterSubject: fmt.Sprintf("%s.waiting_to_cook.*", subject),
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create consumer", slog.Any("err", err))
		return nil, err
	}

	return &maestroHandlerV1{
		panettiereClient: panettiereClient,
		settings:         settings,
		consumer:         c,
		subject:          subject,
		jsClient:         js,
		lunchCounter:     lunchCounter,
		lunchHistogram:   lunchHistogram,
		smokeCounter:     smokeCounter,
		smokeHistogram:   smokeHistogram,
	}, nil
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

func (m *maestroHandlerV1) startTurn() {
	slog.Info("Maestro is starting his turn")

	lunchTicker := time.NewTicker(time.Duration(m.settings.PeriodBetweenLunchInSeconds) * time.Second)
	hasTicketed := false
	go func() {
		<-lunchTicker.C
		hasTicketed = true
	}()

	for {
		ctx := context.Background()
		slog.DebugContext(ctx, "Starting internal loop")

		orders, err := m.getNewBatchMessages(ctx)
		if err != nil {
			continue
		}

		for order := range orders {
			err = order.InProgress()
			if err != nil {
				slog.ErrorContext(ctx, "failed to set message in progress", slog.Any("err", err))
				continue
			}
			m.processNewOrder(ctx, order)
		}

		if !hasTicketed {
			continue
		}

		m.lunch(ctx)
		hasTicketed = false
		lunchTicker.Reset(time.Duration(m.settings.PeriodBetweenLunchInSeconds) * time.Second)

		// We garantee that this goroutine will run only once
		go func() {
			<-lunchTicker.C
			hasTicketed = true
		}()
	}
}

func (m *maestroHandlerV1) getNewBatchMessages(ctx context.Context) (<-chan jetstream.Msg, error) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.getNewBatchMessages")
	defer span.End()

	slog.DebugContext(ctx, "Fetching new batch of messages")
	msgs, err := m.consumer.Fetch(m.settings.OrderBatchSize,
		jetstream.FetchMaxWait(time.Duration(m.settings.FetchMaxWaitInSeconds)*time.Second),
	)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to consume messages", slog.Any("err", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	slog.DebugContext(ctx, "Fetched new batch of messages")

	return msgs.Messages(), nil
}

func (m *maestroHandlerV1) lunch(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.lunch", trace.WithAttributes(
		attribute.Int("maestro.lunch-duration-in-seconds", m.settings.LunchDurationInSeconds),
	))
	defer span.End()

	m.isLunching = true
	m.status = "lunching"

	slog.InfoContext(ctx, "Maestro is having lunch", slog.Int("lunch-duration-in-seconds", m.settings.LunchDurationInSeconds))
	time.Sleep(time.Duration(m.settings.LunchDurationInSeconds) * time.Second)
	slog.InfoContext(ctx, "Maestro finished lunch")

	m.lunchCounter.Add(ctx, 1)
	m.lunchHistogram.Record(ctx, float64(m.settings.LunchDurationInSeconds))

	m.isLunching = false
	m.status = "idle"
}

func (m *maestroHandlerV1) processNewOrder(ctx context.Context, msg jetstream.Msg) {
	ctx = telemetry.GetContextFromJetstreamMsg(ctx, msg)
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.processNewOrder")
	defer span.End()

	var order Order

	err := json.Unmarshal(msg.Data(), &order)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal order from NATS message", slog.Any("err", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}

	slog.DebugContext(ctx, "Deserialized order", slog.Any("order", order))

	span.SetAttributes(
		attribute.String("box-box.orderid", order.OrderID),
		attribute.String("order.size", order.Size),
		attribute.String("order.destination", order.Destination),
		attribute.String("order.username", order.Username),
		attribute.StringSlice("order.toppings", order.Toppings),
	)

	m.status = fmt.Sprintf("processing order %s", order.OrderID)

	doughResponse, err := m.requestDough(ctx, order)
	if err != nil {
		slog.ErrorContext(ctx, "failed to request dough", slog.String("order-id", order.OrderID), slog.Any("err", err))
		return
	}

	slog.DebugContext(ctx, "Dough response", slog.Any("doughResponse", doughResponse))

	slog.InfoContext(ctx, "Order processed successfully", slog.String("order-id", order.OrderID))

	err = m.sendToDeliveryQueue(ctx, order)
	if err != nil {
		return
	}

	slog.DebugContext(ctx, "Acknowledging message")

	err = msg.Ack()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acknowledge message", slog.Any("err", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}

	slog.DebugContext(ctx, "Acknowledged message")

	m.smoke(ctx, order)
}

func (m *maestroHandlerV1) sendToDeliveryQueue(ctx context.Context, order Order) error {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.sendToDeliveryQueue", trace.WithAttributes(
		attribute.String("box-box.orderid", order.OrderID),
	))
	defer span.End()

	slog.DebugContext(ctx, "Sending order to delivery queue", slog.String("order-id", order.OrderID))

	msg := &nats.Msg{
		Subject: fmt.Sprintf("%s.waiting_delivery.%s", m.subject, order.OrderID),
		Header:  nats.Header{},
	}

	order.Status = "waiting_delivery"

	telemetry.InjectContextToNatsMsg(ctx, msg)
	data, err := json.Marshal(order)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal order to json", slog.Any("err", err))
		span.SetStatus(codes.Error, "failed to marshal order")
		span.RecordError(err)
		return err
	}

	msg.Data = data

	_, err = m.jsClient.PublishMsg(ctx, msg)
	if err != nil {
		slog.ErrorContext(ctx, "failed to publish order to delivery queue", slog.Any("err", err))
		span.SetStatus(codes.Error, "failed to publish order to delivery queue")
		span.RecordError(err)
		return err
	}

	slog.InfoContext(ctx, "Published order to delivery queue", slog.String("order-id", order.OrderID))

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
	m.smokeCounter.Add(ctx, 1)
	m.smokeHistogram.Record(ctx, sleepDuration.Seconds())

	m.isSmoking = false
	m.status = "idle"

	slog.InfoContext(ctx, "Finished smoking after order", slog.String("order-id", order.OrderID))
}

func (m *maestroHandlerV1) requestDough(ctx context.Context, order Order) (*panettierev1pb.DoughResponse, error) {
	ctx, span := tracer.Start(ctx, "maestroHandlerV1.requestDough", trace.WithAttributes(
		attribute.String("box-box.orderid", order.OrderID),
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
