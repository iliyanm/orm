package orm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"

	"github.com/stretchr/testify/assert"
)

type testEntityValidatedRegistry struct {
	ORM
	ID uint
}
type testEntityValidatedRegistryUnregistered struct {
	ORM
	ID uint
}

func TestValidatedRegistry(t *testing.T) {
	registry := &Registry{}
	registry.RegisterEntity(&testEntityValidatedRegistry{})
	registry.RegisterMySQLPool("root:root@tcp(localhost:3308)/test")
	registry.RegisterRabbitMQServer("amqp://rabbitmq_user:rabbitmq_password@localhost:5672/test")
	vr, err := registry.Validate()
	assert.Nil(t, err)

	vrFull := vr.(*validatedRegistry)
	vr.AddLogger(memory.New())
	assert.Nil(t, vrFull.log)
	vr.SetLogLevel(log.WarnLevel)
	assert.NotNil(t, vrFull.log)
	assert.Equal(t, log.WarnLevel, vrFull.log.Level)
	vr.EnableDebug()
	assert.Equal(t, log.DebugLevel, vrFull.log.Level)
	require.PanicsWithError(t, "entity 'orm.testEntityValidatedRegistryUnregistered' is not registered", func() {
		vr.GetTableSchemaForEntity(&testEntityValidatedRegistryUnregistered{})
	})
	schema := vr.GetTableSchemaForEntity(&testEntityValidatedRegistry{})
	assert.NotNil(t, schema)
}
