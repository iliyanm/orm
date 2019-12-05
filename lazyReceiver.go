package orm

import (
	"encoding/json"
	"fmt"
)

type LazyReceiver struct {
	QueueName string
}

func (r LazyReceiver) Size() (int64, error) {
	return GetRedis(r.QueueName + "_queue").LLen("lazy_queue")
}

func (r LazyReceiver) Digest() (has bool, err error) {
	redis := GetRedis(r.QueueName + "_queue")
	key := "lazy_queue"
	val, found, err := redis.RPop(key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	var data interface{}
	err = json.Unmarshal([]byte(val), &data)
	if err != nil {
		return true, err
	}
	brokenMap := make(map[string]interface{})
	validMap, ok := data.(map[string]interface{})
	if !ok {
		return true, fmt.Errorf("invalid map: %v", data)
	}
	err = r.handleQueries(validMap, brokenMap)
	if err != nil {
		return true, err
	}
	err = r.handleClearCache(validMap, brokenMap, "cl")
	if err != nil {
		return true, err
	}
	err = r.handleClearCache(validMap, brokenMap, "cr")
	if err != nil {
		return true, err
	}
	if len(brokenMap) > 0 {
		v, err := serializeForLazyQueue(brokenMap)
		if err != nil {
			return true, err
		}
		_, err = getRedisForQueue("default").RPush("lazy_queue", v)
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

func (r *LazyReceiver) handleQueries(validMap map[string]interface{}, brokenMap map[string]interface{}) error {
	queries, has := validMap["q"]
	if has {
		validQueries, ok := queries.([]interface{})
		if !ok {
			return fmt.Errorf("invalid queries: %v", queries)
		}
		for _, query := range validQueries {
			validInsert, ok := query.([]interface{})
			if !ok {
				return fmt.Errorf("invalid query: %v", validInsert)
			}
			db := GetMysql(validInsert[0].(string))
			sql := validInsert[1].(string)
			attributes := validInsert[2].([]interface{})
			_, err := db.Exec(sql, attributes...)
			if err != nil {
				brokenMap["q"] = validMap["q"]
				return err
			}
		}
	}
	return nil
}

func (r *LazyReceiver) handleClearCache(validMap map[string]interface{}, brokenMap map[string]interface{}, key string) error {
	keys, has := validMap[key]
	if has {
		validKeys, ok := keys.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid cache keys: %v", keys)
		}
		for cacheCode, allKeys := range validKeys {
			validAllKeys, ok := allKeys.([]interface{})
			if !ok {
				return fmt.Errorf("invalid cache keys: %v", allKeys)
			}
			stringKeys := make([]string, len(validAllKeys))
			for i, v := range validAllKeys {
				stringKeys[i] = v.(string)
			}
			if key == "cl" {
				cache, has := localCacheContainers[cacheCode]
				if !has {
					return fmt.Errorf("unknown local cache %s", cacheCode)
				}
				cache.Remove(stringKeys...)
			} else {
				cache, has := redisServers[cacheCode]
				if !has {
					return fmt.Errorf("unknown redis cache %s", cacheCode)
				}
				err := cache.Del(stringKeys...)
				if err != nil {
					if brokenMap[key] == nil {
						brokenMap[key] = make(map[string][]interface{})
					}
					brokenMap[key].(map[string][]interface{})[cacheCode] = validAllKeys
					return err
				}
			}
		}
	}
	return nil
}
