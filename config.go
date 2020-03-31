package orm

import (
	"database/sql"
	"fmt"
	"github.com/go-redis/redis/v7"
	"github.com/golang/groupcache/lru"
	"reflect"
	"strings"
)

type EntityNotRegisteredError struct {
	Name string
}

func (e EntityNotRegisteredError) Error() string {
	return fmt.Sprintf("entity is not registered %s", strings.Trim(e.Name, "*[]"))
}

type DBPoolNotRegisteredError struct {
	Name string
}

func (e DBPoolNotRegisteredError) Error() string {
	return fmt.Sprintf("db pool %s is not registered", e.Name)
}

type LocalCachePoolNotRegisteredError struct {
	Name string
}

func (e LocalCachePoolNotRegisteredError) Error() string {
	return fmt.Sprintf("local cache pool %s is not registered", e.Name)
}

type RedisCachePoolNotRegisteredError struct {
	Name string
}

func (e RedisCachePoolNotRegisteredError) Error() string {
	return fmt.Sprintf("redis cache pool %s is not registered", e.Name)
}

type Config struct {
	tableSchemas          map[reflect.Type]*TableSchema
	sqlClients            map[string]*DBConfig
	localCacheContainers  map[string]*LocalCacheConfig
	redisServers          map[string]*RedisCacheConfig
	entities              map[string]reflect.Type
	enums                 map[string]reflect.Value
	dirtyQueuesCodes      map[string]string
	dirtyQueuesCodesNames []string
	lazyQueuesCodes       map[string]string
	lazyQueuesCodesNames  []string
}

func (c *Config) RegisterEntity(entity ...interface{}) {
	if c.entities == nil {
		c.entities = make(map[string]reflect.Type)
	}
	for _, e := range entity {
		t := reflect.TypeOf(e)
		c.entities[t.String()] = t
	}
}

func (c *Config) RegisterEnum(name string, enum interface{}) {
	if c.enums == nil {
		c.enums = make(map[string]reflect.Value)
	}
	c.enums[name] = reflect.Indirect(reflect.ValueOf(enum))
}

func (c *Config) RegisterMySqlPool(dataSourceName string, code ...string) error {
	return c.registerSqlPool(dataSourceName, code...)
}

func (c *Config) RegisterLocalCache(size int, code ...string) {
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	if c.localCacheContainers == nil {
		c.localCacheContainers = make(map[string]*LocalCacheConfig)
	}
	c.localCacheContainers[dbCode] = &LocalCacheConfig{code: dbCode, lru: lru.New(size)}
}

func (c *Config) RegisterRedis(address string, db int, code ...string) {
	client := redis.NewClient(&redis.Options{
		Addr: address,
		DB:   db,
	})
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	redisCache := &RedisCacheConfig{code: dbCode, client: client}
	if c.redisServers == nil {
		c.redisServers = make(map[string]*RedisCacheConfig)
	}
	c.redisServers[dbCode] = redisCache
}

func (c *Config) RegisterDirtyQueue(code string, redisCode string) {
	if c.dirtyQueuesCodes == nil {
		c.dirtyQueuesCodes = make(map[string]string)
	}
	c.dirtyQueuesCodes[code] = redisCode
	if c.dirtyQueuesCodesNames == nil {
		c.dirtyQueuesCodesNames = make([]string, 0)
	}
	c.dirtyQueuesCodesNames = append(c.dirtyQueuesCodesNames, code)
}

func (c *Config) GetTableSchema(entityOrTypeOrName interface{}) (schema *TableSchema, has bool, err error) {
	asString, is := entityOrTypeOrName.(string)
	if is {
		t, has := c.getEntityType(asString)
		if !has {
			return nil, false, nil
		}
		return getTableSchema(c, t)
	}
	return getTableSchema(c, entityOrTypeOrName)
}

func (c *Config) GetDirtyQueueCodes() []string {
	if c.dirtyQueuesCodes == nil {
		c.dirtyQueuesCodesNames = make([]string, 0)
	}
	return c.dirtyQueuesCodesNames
}

func (c *Config) RegisterLazyQueue(code string, redisCode string) {
	if c.lazyQueuesCodes == nil {
		c.lazyQueuesCodes = make(map[string]string)
	}
	c.lazyQueuesCodes[code] = redisCode
	if c.lazyQueuesCodesNames == nil {
		c.lazyQueuesCodesNames = make([]string, 0)
	}
	c.lazyQueuesCodesNames = append(c.lazyQueuesCodesNames, code)
}

func (c *Config) GetLazyQueueCodes() []string {
	if c.lazyQueuesCodesNames == nil {
		c.lazyQueuesCodesNames = make([]string, 0)
	}
	return c.lazyQueuesCodesNames
}

func (c *Config) Validate() error {
	for _, entity := range c.entities {
		_, _, err := getTableSchemaFromValue(c, entity)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) registerSqlPool(dataSourceName string, code ...string) error {
	sqlDB, _ := sql.Open("mysql", dataSourceName)
	dbCode := "default"
	if len(code) > 0 {
		dbCode = code[0]
	}
	db := &DBConfig{code: dbCode, db: sqlDB}
	if c.sqlClients == nil {
		c.sqlClients = make(map[string]*DBConfig)
	}
	c.sqlClients[dbCode] = db
	db.databaseName = strings.Split(dataSourceName, "/")[1]
	return nil
}

func (c *Config) getEntityType(name string) (t reflect.Type, has bool) {
	t, is := c.entities[name]
	if !is {
		return nil, false
	}
	return t, true
}