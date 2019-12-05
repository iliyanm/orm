package orm

import (
	"fmt"
	"github.com/go-redis/redis"
	"strconv"
	"strings"
)

type DirtyReceiver struct {
	QueueCode string
}

type DirtyData struct {
	TableSchema *TableSchema
	Id          uint64
	Inserted    bool
	Updated     bool
	Deleted     bool
}

type DirtyHandler func([]DirtyData) (invalid []redis.Z, err error)

func (r DirtyReceiver) Size() (int64, error) {
	red, err := r.getRedis()
	if err != nil {
		return 0, err
	}
	return red.ZCard(r.QueueCode)
}

func (r DirtyReceiver) Digest(max int, handler DirtyHandler) (has bool, err error) {
	cache, err := r.getRedis()
	if err != nil {
		return false, err
	}
	values, err := cache.ZPopMin(r.QueueCode, int64(max))
	if err != nil {
		return false, err
	}
	if len(values) == 0 {
		return false, nil
	}
	results := make([]DirtyData, 0, len(values))
	for _, v := range values {
		val := strings.Split(v.Member.(string), ":")
		if len(val) != 3 {
			continue
		}
		tableSchema := getTableSchema(getEntityType(val[0]))
		id, err := strconv.ParseUint(val[2], 10, 64)
		if err != nil {
			continue
		}
		data := DirtyData{
			TableSchema: tableSchema,
			Id:          id,
			Inserted:    val[1] == "i",
			Updated:     val[1] == "u",
			Deleted:     val[1] == "d",
		}
		results = append(results, data)
	}
	invalid, err := handler(results)
	if err != nil {
		if invalid != nil {
			_, _ = cache.ZAdd(r.QueueCode, invalid...)
		}
		return true, err
	}
	return true, nil
}

func (r DirtyReceiver) getRedis() (*RedisCache, error) {
	redisCode, has := dirtyQueuesCodes[r.QueueCode]
	if !has {
		return nil, fmt.Errorf("unregistered dirty queue %s", r.QueueCode)
	}
	return GetRedis(redisCode), nil
}
