package tests

import (
	"github.com/stretchr/testify/assert"
	"github.com/summer-solutions/orm"
	"strconv"
	"testing"
)

type TestEntityByIdsRedisCache struct {
	Orm  *orm.ORM `orm:"table=TestGetByIdsRedis;redisCache"`
	Id   uint
	Name string
}

type TestEntityByIdsRedisCacheRef struct {
	Orm  *orm.ORM `orm:"table=TestEntityByIdsRedisCacheRef;redisCache"`
	Id   uint
	Name string
}

func TestEntityByIdsRedis(t *testing.T) {

	var entity TestEntityByIdsRedisCache
	var entityRef TestEntityByIdsRedisCacheRef
	PrepareTables(entity, entityRef)

	flusher := orm.NewFlusher(100, false)
	for i := 1; i <= 10; i++ {
		e := TestEntityByIdsRedisCache{Name: "Name " + strconv.Itoa(i)}
		flusher.RegisterEntity(&e)
		e2 := TestEntityByIdsRedisCacheRef{Name: "Name " + strconv.Itoa(i)}
		flusher.RegisterEntity(&e2)
	}
	err := flusher.Flush()
	assert.Nil(t, err)

	orm.EnableContextCache(100, 1)

	DBLogger := TestDatabaseLogger{}
	orm.GetMysqlDB("default").AddLogger(&DBLogger)
	CacheLogger := TestCacheLogger{}
	orm.GetRedisCache("default").AddLogger(&CacheLogger)

	var found []TestEntityByIdsRedisCache
	missing := orm.TryByIds([]uint64{2, 13, 1}, &found)
	assert.Len(t, found, 2)
	assert.Len(t, missing, 1)
	assert.Equal(t, []uint64{13}, missing)
	entity = found[0]
	assert.Equal(t, uint(2), entity.Id)
	assert.Equal(t, "Name 2", entity.Name)
	entity = found[1]
	assert.Equal(t, uint(1), entity.Id)
	assert.Equal(t, "Name 1", entity.Name)
	assert.Len(t, DBLogger.Queries, 1)

	missing = orm.TryByIds([]uint64{2, 13, 1}, &found)
	assert.Len(t, found, 2)
	assert.Len(t, missing, 1)
	assert.Equal(t, []uint64{13}, missing)
	entity = found[0]
	assert.Equal(t, uint(2), entity.Id)
	entity = found[1]
	assert.Equal(t, uint(1), entity.Id)
	assert.Len(t, DBLogger.Queries, 1)

	missing = orm.TryByIds([]uint64{25, 26, 27}, &found)
	assert.Len(t, found, 0)
	assert.Len(t, missing, 3)
	assert.Len(t, DBLogger.Queries, 2)

	orm.GetRedisCache("default").FlushDB()
	DBLogger.Queries = make([]string, 0)
	CacheLogger.Requests = make([]string, 0)

	orm.EnableContextCache(100, 1)
	missing = orm.TryByIds([]uint64{8, 9, 10}, &found)
	assert.Len(t, found, 3)
	assert.Len(t, missing, 0)

	missing = orm.TryByIds([]uint64{8, 9, 10}, &found)
	assert.Len(t, found, 3)
	assert.Len(t, missing, 0)
}

func BenchmarkGetByIdsRedis(b *testing.B) {
	var entity TestEntityByIdsRedisCache
	PrepareTables(entity)

	_ = orm.Flush(&TestEntityByIdsRedisCache{Name: "Hi 1"}, &TestEntityByIdsRedisCache{Name: "Hi 2"}, &TestEntityByIdsRedisCache{Name: "Hi 3"})
	var found []TestEntityByIdsRedisCache
	for n := 0; n < b.N; n++ {
		_ = orm.TryByIds([]uint64{1, 2, 3}, &found)
	}
}
