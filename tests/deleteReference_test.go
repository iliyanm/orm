package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/summer-solutions/orm"
)

type TestEntityDeleteReference struct {
	Orm *orm.ORM `orm:"localCache"`
	ID  uint
}

type TestEntityDeleteReferenceRefRestrict struct {
	Orm          *orm.ORM `orm:"localCache"`
	ID           uint
	ReferenceOne *orm.ReferenceOne `orm:"ref=tests.TestEntityDeleteReference"`
}

type TestEntityDeleteReferenceRefCascade struct {
	Orm               *orm.ORM `orm:"localCache"`
	ID                uint
	ReferenceOne      *orm.ReferenceOne `orm:"ref=tests.TestEntityDeleteReference;cascade"`
	IndexReferenceOne *orm.CachedQuery  `query:":ReferenceOne = ?"`
}

func TestDeleteReference(t *testing.T) {
	engine := PrepareTables(t, &orm.Registry{}, TestEntityDeleteReference{},
		TestEntityDeleteReferenceRefRestrict{}, TestEntityDeleteReferenceRefCascade{})
	entity1 := &TestEntityDeleteReference{}
	entity2 := &TestEntityDeleteReference{}
	err := engine.Flush(entity1, entity2)
	assert.Nil(t, err)

	entityRestrict := &TestEntityDeleteReferenceRefRestrict{}
	err = engine.Init(entityRestrict)
	assert.Nil(t, err)
	entityRestrict.ReferenceOne.ID = 1
	err = engine.Flush(entityRestrict)
	assert.Nil(t, err)

	entity1.Orm.MarkToDelete()
	err = engine.Flush(entity1)
	assert.NotNil(t, err)
	assert.IsType(t, &orm.ForeignKeyError{}, err)
	assert.Equal(t, "test:TestEntityDeleteReferenceRefRestrict:ReferenceOne", err.(*orm.ForeignKeyError).Constraint)

	entityCascade := &TestEntityDeleteReferenceRefCascade{}
	entityCascade2 := &TestEntityDeleteReferenceRefCascade{}
	err = engine.Init(entityCascade, entityCascade2)
	assert.Nil(t, err)
	entityCascade.ReferenceOne.ID = 2
	entityCascade2.ReferenceOne.ID = 2
	err = engine.Flush(entityCascade, entityCascade2)
	assert.Nil(t, err)

	var rows []*TestEntityDeleteReferenceRefCascade
	total, err := engine.CachedSearch(&rows, "IndexReferenceOne", nil, 2)
	assert.Nil(t, err)
	assert.Equal(t, 2, total)

	entity2.Orm.MarkToDelete()
	err = engine.Flush(entity2)
	assert.Nil(t, err)

	total, err = engine.CachedSearch(&rows, "IndexReferenceOne", nil, 2)
	assert.Nil(t, err)
	assert.Equal(t, 0, total)
}
