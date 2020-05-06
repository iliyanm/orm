package orm

import (
	"testing"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"

	"github.com/streadway/amqp"

	"github.com/stretchr/testify/assert"
)

func TestRabbitMQQueue(t *testing.T) {
	registry := &Registry{}
	registry.RegisterRabbitMQServer("amqp://rabbitmq_user:rabbitmq_password@localhost:5672/")
	registry.RegisterRabbitMQQueue("default", &RabbitMQQueueConfig{Name: "test_queue"})
	validatedRegistry, err := registry.Validate()
	assert.Nil(t, err)
	engine := validatedRegistry.CreateEngine()
	defer engine.Defer()

	r := engine.GetRabbitMQChannel("test_queue")
	testLogger := memory.New()
	r.AddLogger(testLogger)
	r.SetLogLevel(log.InfoLevel)

	msg := amqp.Publishing{
		ContentType: "text/plain",
		Body:        []byte("hello"),
	}

	assert.NotNil(t, r)
	err = r.Publish(false, false, "", msg)
	assert.NoError(t, err)

	consumer, err := r.NewConsumer("test consumer")
	assert.NoError(t, err)
	items, err := consumer.Consume(true, false)
	assert.NoError(t, err)
	assert.NotNil(t, items)
	//item := <-items
	//assert.NotNil(t, item)
	//assert.Equal(t, []byte("hello"), item.Body)
}

func TestRabbitMQQueueExchange(t *testing.T) {
	registry := &Registry{}
	registry.RegisterRabbitMQServer("amqp://rabbitmq_user:rabbitmq_password@localhost:5672/")
	registry.RegisterRabbitMQQueue("default", &RabbitMQQueueConfig{Name: "test_queue_exchange", Exchange: "test_exchange"})
	registry.RegisterRabbitMQExchange("default", &RabbitMQExchangeConfig{Name: "test_exchange", Type: "fanout"})
	validatedRegistry, err := registry.Validate()
	assert.Nil(t, err)
	engine := validatedRegistry.CreateEngine()
	defer engine.Defer()

	r := engine.GetRabbitMQChannel("test_queue_exchange")
	testLogger := memory.New()
	r.AddLogger(testLogger)
	r.SetLogLevel(log.InfoLevel)

	msg := amqp.Publishing{
		ContentType: "text/plain",
		Body:        []byte("hello"),
	}

	assert.NotNil(t, r)

	consumer, err := r.NewConsumer("test consumer")
	assert.NoError(t, err)
	items, err := consumer.Consume(true, false)
	assert.NoError(t, err)
	assert.NotNil(t, items)

	consumer2, err := r.NewConsumer("test consumer")
	assert.NoError(t, err)
	_, err = consumer2.Consume(true, false)
	assert.NoError(t, err)
	assert.NotNil(t, items)

	err = r.Publish(false, false, "", msg)
	assert.NoError(t, err)

	//item := <-items
	//assert.NotNil(t, item)
	//assert.Equal(t, []byte("hello"), item.Body)
	//
	//item2 := <-items2
	//assert.NotNil(t, item2)
	//assert.Equal(t, []byte("hello"), item2.Body)
}
