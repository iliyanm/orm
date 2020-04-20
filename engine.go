package orm

import (
	"fmt"
	"reflect"
)

type Engine struct {
	config      *Config
	dbs         map[string]*DB
	localCache  map[string]*LocalCache
	redis       map[string]*RedisCache
	locks       map[string]*Locker
	logMetaData map[string]interface{}
}

func (e *Engine) SetLogMetaData(key string, value interface{}) {
	if e.logMetaData == nil {
		e.logMetaData = make(map[string]interface{})
	}
	e.logMetaData[key] = value
}

func (e *Engine) RegisterEntity(entity Entity) {
	value := reflect.ValueOf(entity)
	if value.Kind() != reflect.Ptr {
		panic(fmt.Errorf("registered entity '%s' is not a poninter", value.Type().String()))
	}
	initIfNeeded(e, value, true)
}

func (e *Engine) GetConfig() *Config {
	return e.config
}

func (e *Engine) GetMysql(code ...string) *DB {
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	db, has := e.dbs[dbCode]
	if !has {
		panic(fmt.Errorf("unregistered mysql pool '%s'", dbCode))
	}
	return db
}

func (e *Engine) GetLocalCache(code ...string) *LocalCache {
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	cache, has := e.localCache[dbCode]
	if !has {
		panic(fmt.Errorf("unregistered local cache pool '%s'", dbCode))
	}
	return cache
}

func (e *Engine) GetRedis(code ...string) *RedisCache {
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	cache, has := e.redis[dbCode]
	if !has {
		panic(fmt.Errorf("unregistered redis cache pool '%s'", dbCode))
	}
	return cache
}

func (e *Engine) GetLocker(code ...string) *Locker {
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	locker, has := e.locks[dbCode]
	if !has {
		panic(fmt.Errorf("unregistered locker pool '%s'", dbCode))
	}
	return locker
}

func (e *Engine) SearchWithCount(where *Where, pager *Pager, entities interface{}, references ...string) (totalRows int, err error) {
	return search(true, e, where, pager, true, reflect.ValueOf(entities).Elem(), references...)
}

func (e *Engine) Search(where *Where, pager *Pager, entities interface{}, references ...string) error {
	_, err := search(true, e, where, pager, false, reflect.ValueOf(entities).Elem(), references...)
	return err
}

func (e *Engine) SearchIDsWithCount(where *Where, pager *Pager, entity interface{}) (results []uint64, totalRows int, err error) {
	return searchIDsWithCount(true, e, where, pager, reflect.TypeOf(entity))
}

func (e *Engine) SearchIDs(where *Where, pager *Pager, entity interface{}) ([]uint64, error) {
	results, _, err := searchIDs(true, e, where, pager, false, reflect.TypeOf(entity))
	return results, err
}

func (e *Engine) SearchOne(where *Where, entity interface{}) (bool, error) {
	return searchOne(true, e, where, entity)
}

func (e *Engine) CachedSearchOne(entity Entity, indexName string, arguments ...interface{}) (has bool, err error) {
	return cachedSearchOne(e, entity, indexName, arguments...)
}

func (e *Engine) CachedSearch(entities interface{}, indexName string, pager *Pager, arguments ...interface{}) (totalRows int, err error) {
	return cachedSearch(e, entities, indexName, pager, arguments, nil)
}

func (e *Engine) CachedSearchWithReferences(entities interface{}, indexName string, pager *Pager,
	arguments []interface{}, references []string) (totalRows int, err error) {
	return cachedSearch(e, entities, indexName, pager, arguments, references)
}

func (e *Engine) ClearByIDs(entity Entity, ids ...uint64) error {
	return clearByIDs(e, entity, ids...)
}

func (e *Engine) FlushInCache(entities ...interface{}) error {
	return flushInCache(e, entities...)
}

func (e *Engine) LoadByID(id uint64, entity Entity, references ...string) (found bool, err error) {
	return loadByID(e, id, entity, references...)
}

func (e *Engine) LoadByIDs(ids []uint64, entities interface{}, references ...string) (missing []uint64, err error) {
	return tryByIDs(e, ids, reflect.ValueOf(entities).Elem(), references)
}

func (e *Engine) GetAlters() (alters []Alter, err error) {
	return getAlters(e)
}

func (e *Engine) RegisterDatabaseLogger(logger DatabaseLogger) {
	for _, db := range e.dbs {
		db.RegisterLogger(logger)
	}
}

func (e *Engine) RegisterRedisLogger(logger CacheLogger) {
	for _, red := range e.redis {
		red.RegisterLogger(logger)
	}
}

func (e *Engine) getRedisForQueue(code string) *RedisCache {
	return e.GetRedis(code + "_queue")
}
