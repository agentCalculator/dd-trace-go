// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama_test

import (
	"context"
	"errors"
	"log"

	saramatrace "github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/IBM/sarama"
)

func ExampleWrapAsyncProducer() {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0 // minimum version that supports headers which are required for tracing

	producer, err := sarama.NewAsyncProducer([]string{"localhost:9092"}, cfg)
	if err != nil {
		panic(err)
	}
	defer producer.Close()

	producer = saramatrace.WrapAsyncProducer(cfg, producer)

	msg := &sarama.ProducerMessage{
		Topic: "some-topic",
		Value: sarama.StringEncoder("Hello World"),
	}
	producer.Input() <- msg
}

func ExampleWrapSyncProducer() {
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer([]string{"localhost:9092"}, cfg)
	if err != nil {
		panic(err)
	}
	defer producer.Close()

	producer = saramatrace.WrapSyncProducer(cfg, producer)

	msg := &sarama.ProducerMessage{
		Topic: "some-topic",
		Value: sarama.StringEncoder("Hello World"),
	}
	_, _, err = producer.SendMessage(msg)
	if err != nil {
		panic(err)
	}
}

func ExampleWrapConsumer() {
	consumer, err := sarama.NewConsumer([]string{"localhost:9092"}, nil)
	if err != nil {
		panic(err)
	}
	defer consumer.Close()

	consumer = saramatrace.WrapConsumer(consumer)

	partitionConsumer, err := consumer.ConsumePartition("some-topic", 0, sarama.OffsetNewest)
	if err != nil {
		panic(err)
	}
	defer partitionConsumer.Close()

	consumed := 0
	for msg := range partitionConsumer.Messages() {
		// if you want to use the kafka message as a parent span:
		if spanctx, err := tracer.Extract(saramatrace.NewConsumerMessageCarrier(msg)); err == nil {
			// you can create a span using ChildOf(spanctx)
			_ = spanctx
		}

		log.Printf("Consumed message offset %d\n", msg.Offset)
		consumed++
	}
}

func ExampleWrapConsumerGroupHandler() {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0 // first version that supports headers
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 1

	const groupID = "group-id"

	consumerGroup, err := sarama.NewConsumerGroup([]string{"localhost:9092"}, groupID, cfg)
	if err != nil {
		panic(err)
	}

	// trace your sarama.ConsumerGroupHandler implementation
	var myHandler sarama.ConsumerGroupHandler
	handler := saramatrace.WrapConsumerGroupHandler(myHandler, saramatrace.WithGroupID(groupID))

	ctx := context.Background()
	for {
		// `Consume` should be called inside an infinite loop, when a
		// server-side rebalance happens, the consumer session will need to be
		// recreated to get the new claims
		if err := consumerGroup.Consume(ctx, []string{"my-topic"}, handler); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				return
			}
			log.Panicf("Error from consumer: %v", err)
		}
		// check if context was cancelled, signaling that the consumer should stop
		if ctx.Err() != nil {
			return
		}
	}
}
