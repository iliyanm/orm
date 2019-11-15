package tests

import (
	"github.com/stretchr/testify/assert"
	"github.com/summer-solutions/orm"
	"testing"
)

type TestEntityFlusherInCacheRedis struct {
	Orm      *orm.ORM `orm:"redisCache"`
	Id       uint
	Name     string
	Age      uint16
	IndexAge *orm.CachedQuery `query:":Age = ? ORDER BY :Id"`
}

type TestEntityFlusherInCacheLocal struct {
	Orm *orm.ORM
	Id  uint
}

func TestFlushInCache(t *testing.T) {

	entityRedis := TestEntityFlusherInCacheRedis{Name: "Name", Age: 18}
	entityLocal := TestEntityFlusherInCacheLocal{}
	PrepareTables(entityRedis, entityLocal)

	err := orm.Flush(&entityRedis)
	assert.Nil(t, err)

	pager := orm.NewPager(1, 100)
	var rows []TestEntityFlusherInCacheRedis
	totalRows := orm.CachedSearch(&rows, "IndexAge", pager, 18)
	assert.Equal(t, 1, totalRows)
	assert.Len(t, rows, 1)
	totalRows = orm.CachedSearch(&rows, "IndexAge", pager, 10)
	assert.Equal(t, 0, totalRows)
	assert.Len(t, rows, 0)

	LoggerDB := TestDatabaseLogger{}
	orm.GetMysql().AddLogger(&LoggerDB)
	LoggerRedisCache := TestCacheLogger{}
	orm.GetRedis().AddLogger(&LoggerRedisCache)
	LoggerRedisQueue := TestCacheLogger{}
	orm.GetRedis("default_queue").AddLogger(&LoggerRedisQueue)

	entityRedis.Name = "Name 2"
	entityRedis.Age = 10

	err = orm.FlushInCache(&entityLocal, &entityRedis)
	assert.Nil(t, err)
	assert.Len(t, LoggerDB.Queries, 1)
	assert.Equal(t, "INSERT INTO TestEntityFlusherInCacheLocal() VALUES () []", LoggerDB.Queries[0])
	assert.Len(t, LoggerRedisCache.Requests, 1)
	assert.Equal(t, "MSET [TestEntityFlusherInCacheRedisce:1 ] ", LoggerRedisCache.Requests[0])
	assert.Len(t, LoggerRedisQueue.Requests, 1)
	assert.Equal(t, "ZADD 1 values dirty_queue", LoggerRedisQueue.Requests[0])

	var loadedEntity TestEntityFlusherInCacheRedis
	orm.GetById(1, &loadedEntity)
	assert.Equal(t, "Name 2", loadedEntity.Name)
	assert.Len(t, LoggerRedisCache.Requests, 2)
	assert.Equal(t, "GET TestEntityFlusherInCacheRedisce:1", LoggerRedisCache.Requests[1])

	receiver := orm.FlushInCacheReceiver{RedisName: "default_queue"}
	assert.Equal(t, int64(1), receiver.Size())

	err = receiver.Digest()
	assert.Nil(t, err)
	assert.Equal(t, int64(0), receiver.Size())

	var inDB TestEntityFlusherInCacheRedis
	orm.SearchOne(orm.NewWhere("`Id` = ?", 1), &inDB)
	assert.Equal(t, "Name 2", inDB.Name)

	totalRows = orm.CachedSearch(&rows, "IndexAge", pager, 18)
	assert.Equal(t, 0, totalRows)
	assert.Len(t, rows, 0)
	totalRows = orm.CachedSearch(&rows, "IndexAge", pager, 10)
	assert.Equal(t, 1, totalRows)
	assert.Len(t, rows, 1)

}