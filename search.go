package orm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func searchIdsWithCount(skipFakeDelete bool, engine *Engine, where *Where, pager *Pager, entityType reflect.Type) (results []uint64, totalRows int, err error) {
	return searchIds(skipFakeDelete, engine, where, pager, true, entityType)
}

func searchRow(skipFakeDelete bool, engine *Engine, where *Where, value reflect.Value) (bool, error) {
	entityType := value.Elem().Type()
	schema := getTableSchema(engine.config, entityType)
	if schema == nil {
		return false, EntityNotRegisteredError{Name: entityType.String()}
	}
	fieldsList, err := buildFieldList(engine.config, schema, entityType, "")
	if err != nil {
		return false, err
	}
	whereQuery := where.String()
	if skipFakeDelete && schema.hasFakeDelete {
		whereQuery = fmt.Sprintf("`FakeDelete` = 0 AND %s", whereQuery)
	}
	query := fmt.Sprintf("SELECT %s FROM `%s` WHERE %s LIMIT 1", fieldsList, schema.TableName, whereQuery)

	pool := schema.GetMysql(engine)
	results, err := pool.Query(query, where.GetParameters()...)
	if err != nil {
		return false, err
	}
	if !results.Next() {
		return false, nil
	}

	columns, err := results.Columns()
	if err != nil {
		return false, err
	}
	count := len(columns)

	values := make([]string, count)
	valuePointers := make([]interface{}, count)
	for i := range columns {
		valuePointers[i] = &values[i]
	}
	err = results.Scan(valuePointers...)
	if err != nil {
		return false, err
	}
	id, _ := strconv.ParseUint(values[0], 10, 64)
	err = fillFromDBRow(id, engine, values[1:], value, entityType)
	if err != nil {
		return false, err
	}
	return true, nil
}

func search(skipFakeDelete bool, engine *Engine, where *Where, pager *Pager, withCount bool, entities reflect.Value, references ...string) (int, error) {
	if pager == nil {
		pager = &Pager{CurrentPage: 1, PageSize: 50000}
	}
	entities.SetLen(0)
	entityType, has := getEntityTypeForSlice(engine.config, entities.Type())
	if !has {
		return 0, EntityNotRegisteredError{Name: entities.String()}
	}
	schema := getTableSchema(engine.config, entityType)
	if schema == nil {
		return 0, EntityNotRegisteredError{Name: entityType.String()}
	}

	fieldsList, err := buildFieldList(engine.config, schema, entityType, "")
	if err != nil {
		return 0, err
	}
	whereQuery := where.String()
	if skipFakeDelete && schema.hasFakeDelete {
		whereQuery = fmt.Sprintf("`FakeDelete` = 0 AND %s", whereQuery)
	}
	query := fmt.Sprintf("SELECT %s FROM `%s` WHERE %s %s", fieldsList, schema.TableName, whereQuery,
		fmt.Sprintf("LIMIT %d,%d", (pager.CurrentPage-1)*pager.PageSize, pager.PageSize))
	pool := schema.GetMysql(engine)
	results, err := pool.Query(query, where.GetParameters()...)
	if err != nil {
		return 0, err
	}

	columns, err := results.Columns()
	if err != nil {
		return 0, err
	}
	count := len(columns)

	values := make([]string, count)
	valuePointers := make([]interface{}, count)

	valOrigin := entities
	val := valOrigin
	i := 0
	for results.Next() {
		for i := range columns {
			valuePointers[i] = &values[i]
		}
		err = results.Scan(valuePointers...)
		if err != nil {
			return 0, err
		}
		value := reflect.New(entityType)
		id, _ := strconv.ParseUint(values[0], 10, 64)
		err = fillFromDBRow(id, engine, values[1:], value, entityType)
		if err != nil {
			return 0, err
		}
		val = reflect.Append(val, value)
		i++
	}
	totalRows, err := getTotalRows(engine, withCount, pager, where, schema, i)
	if err != nil {
		return 0, err
	}
	if len(references) > 0 && i > 0 {
		err = warmUpReferences(engine, schema, val, references, true)
		if err != nil {
			return 0, err
		}
	}
	valOrigin.Set(val)
	return totalRows, nil
}

func searchOne(skipFakeDelete bool, engine *Engine, where *Where, entity interface{}) (bool, error) {

	value := reflect.ValueOf(entity)
	has, err := searchRow(skipFakeDelete, engine, where, value)
	if err != nil {
		return false, err
	}
	return has, nil
}

func searchIds(skipFakeDelete bool, engine *Engine, where *Where, pager *Pager, withCount bool, entityType reflect.Type) (ids []uint64, total int, err error) {
	schema := getTableSchema(engine.config, entityType)
	if schema == nil {
		return nil, 0, EntityNotRegisteredError{Name: entityType.String()}
	}
	whereQuery := where.String()
	if skipFakeDelete && schema.hasFakeDelete {
		whereQuery = fmt.Sprintf("`FakeDelete` = 0 AND %s", whereQuery)
	}
	query := fmt.Sprintf("SELECT `Id` FROM `%s` WHERE %s %s", schema.TableName, whereQuery,
		fmt.Sprintf("LIMIT %d,%d", (pager.CurrentPage-1)*pager.PageSize, pager.PageSize))
	pool := schema.GetMysql(engine)
	results, err := pool.Query(query, where.GetParameters()...)
	if err != nil {
		return nil, 0, err
	}
	result := make([]uint64, 0, pager.GetPageSize())
	for results.Next() {
		var row uint64
		err = results.Scan(&row)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, row)
	}
	totalRows, err := getTotalRows(engine, withCount, pager, where, schema, len(result))
	if err != nil {
		return nil, 0, err
	}
	return result, totalRows, nil
}

func getTotalRows(engine *Engine, withCount bool, pager *Pager, where *Where, schema *TableSchema, foundRows int) (int, error) {
	totalRows := 0
	if withCount {
		totalRows = foundRows
		if totalRows == pager.GetPageSize() {
			query := fmt.Sprintf("SELECT count(1) FROM `%s` WHERE %s", schema.TableName, where)
			var foundTotal string
			pool := schema.GetMysql(engine)
			err := pool.QueryRow(query, where.GetParameters()...).Scan(&foundTotal)
			if err != nil {
				return 0, err
			}
			totalRows, _ = strconv.Atoi(foundTotal)
		} else {
			totalRows += (pager.GetCurrentPage() - 1) * pager.GetPageSize()
		}
	}
	return totalRows, nil
}

func fillFromDBRow(id uint64, engine *Engine, data []string, value reflect.Value, entityType reflect.Type) error {
	orm, err := engine.initIfNeeded(value)
	if err != nil {
		return err
	}
	elem := value.Elem()
	elem.Field(1).SetUint(id)
	_, err = fillStruct(engine.config, orm.tableSchema, 0, data, entityType, elem, "")
	if err != nil {
		return err
	}
	orm.dBData[orm.tableSchema.idFieldName] = data[0]

	_, bind, err := isDirty(elem)
	if err != nil {
		return err
	}
	for key, value := range bind {
		orm.dBData[key] = value
	}
	return nil
}

func fillStruct(config *Config, schema *TableSchema, index uint16, data []string, t reflect.Type, value reflect.Value, prefix string) (uint16, error) {

	for i := 0; i < t.NumField(); i++ {

		if index == 0 && i <= 1 { //skip id and orm
			continue
		}

		field := value.Field(i)
		name := prefix + t.Field(i).Name

		tags := schema.Tags[name]
		_, has := tags["ignore"]
		if has {
			continue
		}

		fieldType := field.Type().String()
		switch fieldType {
		case "uint":
			integer, _ := strconv.ParseUint(data[index], 10, 32)
			field.SetUint(integer)
		case "uint8":
			integer, _ := strconv.ParseUint(data[index], 10, 8)
			field.SetUint(integer)
		case "uint16":
			integer, _ := strconv.ParseUint(data[index], 10, 16)
			field.SetUint(integer)
		case "uint32":
			integer, _ := strconv.ParseUint(data[index], 10, 32)
			field.SetUint(integer)
		case "uint64":
			integer, _ := strconv.ParseUint(data[index], 10, 64)
			field.SetUint(integer)
		case "int":
			integer, _ := strconv.ParseInt(data[index], 10, 32)
			field.SetInt(integer)
		case "int8":
			integer, _ := strconv.ParseInt(data[index], 10, 8)
			field.SetInt(integer)
		case "int16":
			integer, _ := strconv.ParseInt(data[index], 10, 16)
			field.SetInt(integer)
		case "int32":
			integer, _ := strconv.ParseInt(data[index], 10, 32)
			field.SetInt(integer)
		case "int64":
			integer, _ := strconv.ParseInt(data[index], 10, 64)
			field.SetInt(integer)
		case "string":
			field.SetString(data[index])
		case "[]string":
			if data[index] != "" {
				var values = strings.Split(data[index], ",")
				var length = len(values)
				slice := reflect.MakeSlice(field.Type(), length, length)
				for key, value := range values {
					slice.Index(key).SetString(value)
				}
				field.Set(slice)
			}
		case "[]uint64":
			if data[index] != "" {
				var values = strings.Split(data[index], " ")
				var length = len(values)
				slice := reflect.MakeSlice(field.Type(), length, length)
				for key, value := range values {
					integer, _ := strconv.ParseUint(value, 10, 64)
					slice.Index(key).SetUint(integer)
				}
				field.Set(slice)
			}
		case "[]uint8":
			bytes := data[index]
			if bytes != "" {
				field.SetBytes([]byte(bytes))
			}
		case "bool":
			if schema.hasFakeDelete && name == "FakeDelete" {
				val := true
				if data[index] == "0" {
					val = false
				}
				field.SetBool(val)
				continue
			}
			val := false
			if data[index] == "1" {
				val = true
			}
			field.SetBool(val)
		case "float32":
			float, _ := strconv.ParseFloat(data[index], 32)
			field.SetFloat(float)
		case "float64":
			float, _ := strconv.ParseFloat(data[index], 64)
			field.SetFloat(float)
		case "time.Time":
			layout := "2006-01-02"
			if len(data[index]) == 19 {
				layout += " 15:04:05"
			}
			value, _ := time.Parse(layout, data[index])
			field.Set(reflect.ValueOf(value))
		case "*orm.ReferenceOne":
			integer, _ := strconv.ParseUint(data[index], 10, 64)
			field.Interface().(*ReferenceOne).Id = integer
		case "*orm.CachedQuery":
			continue
		case "interface {}":
			if data[index] != "" {
				var f interface{}
				err := json.Unmarshal([]byte(data[index]), &f)
				if err != nil {
					return 0, err
				}
				field.Set(reflect.ValueOf(f))
			}
		default:
			if field.Kind().String() == "struct" {
				newVal := reflect.New(field.Type())
				value := newVal.Elem()
				newIndex, err := fillStruct(config, schema, index, data, field.Type(), value, name)
				if err != nil {
					return 0, err
				}
				index = newIndex
				field.Set(value)
				continue
			}
			return 0, fmt.Errorf("unsoported field type: %s", field.Type().String())
		}
		index++
	}
	return index, nil
}

func buildFieldList(config *Config, schema *TableSchema, t reflect.Type, prefix string) (string, error) {
	fieldsList := ""
	for i := 0; i < t.NumField(); i++ {
		var columnNameRaw string
		field := t.Field(i)
		tags := schema.Tags[prefix+t.Field(i).Name]
		_, has := tags["ignore"]
		if has {
			continue
		}
		if prefix == "" && (strings.ToLower(field.Name) == "id" || field.Name == "Orm") {
			continue
		}
		if field.Type.String() == "*orm.CachedQuery" {
			continue
		}
		switch field.Type.String() {
		case "string", "[]string", "[]uint8", "interface {}", "uint16", "*orm.ReferenceOne", "time.Time":
			columnNameRaw = prefix + t.Field(i).Name
			fieldsList += fmt.Sprintf(",IFNULL(`%s`,'')", columnNameRaw)
		default:
			if field.Type.Kind().String() == "struct" {
				f, err := buildFieldList(config, schema, field.Type, field.Name)
				if err != nil {
					return "", err
				}
				fieldsList += f
			} else {
				columnNameRaw = prefix + t.Field(i).Name
				fieldsList += fmt.Sprintf(",`%s`", columnNameRaw)
			}
		}
	}
	if prefix == "" {
		fieldsList = "`Id`" + fieldsList
	}
	return fieldsList, nil
}

func getEntityTypeForSlice(config *Config, sliceType reflect.Type) (reflect.Type, bool) {
	name := strings.Trim(sliceType.String(), "*[]")
	return config.getEntityType(name)
}
