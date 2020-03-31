package tests

import (
	"github.com/stretchr/testify/assert"
	"github.com/summer-solutions/orm"
	"testing"
)

type TestEntityDeleteReference struct {
	Orm *orm.ORM `orm:"localCache"`
	Id  uint
}

type TestEntityDeleteReferenceRefRestrict struct {
	Orm          *orm.ORM `orm:"localCache"`
	Id           uint
	ReferenceOne *orm.ReferenceOne `orm:"ref=tests.TestEntityDeleteReference"`
}

type TestEntityDeleteReferenceRefCascade struct {
	Orm               *orm.ORM `orm:"localCache"`
	Id                uint
	ReferenceOne      *orm.ReferenceOne `orm:"ref=tests.TestEntityDeleteReference;cascade"`
	IndexReferenceOne *orm.CachedQuery  `query:":ReferenceOne = ?"`
}

func TestDeleteReference(t *testing.T) {
	engine := PrepareTables(t, &orm.Config{}, TestEntityDeleteReference{},
		TestEntityDeleteReferenceRefRestrict{}, TestEntityDeleteReferenceRefCascade{})
	entity1 := &TestEntityDeleteReference{}
	entity2 := &TestEntityDeleteReference{}
	err := engine.Flush(entity1, entity2)
	assert.Nil(t, err)

	entityRestrict := &TestEntityDeleteReferenceRefRestrict{}
	err = engine.Init(entityRestrict)
	assert.Nil(t, err)
	entityRestrict.ReferenceOne.Id = 1
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
	entityCascade.ReferenceOne.Id = 2
	entityCascade2.ReferenceOne.Id = 2
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