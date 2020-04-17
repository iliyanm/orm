package orm

import (
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/segmentio/fasthash/fnv1a"
)

type ORM struct {
	dBData      map[string]interface{}
	value       reflect.Value
	elem        reflect.Value
	tableSchema *TableSchema
}

type CachedQuery struct{}

func (orm *ORM) MarkToDelete() {
	if orm.tableSchema.hasFakeDelete {
		orm.elem.FieldByName("FakeDelete").SetBool(true)
		return
	}
	orm.dBData["_delete"] = true
}

func (orm *ORM) Loaded() bool {
	_, has := orm.dBData["_loaded"]
	return has
}

func (orm *ORM) Load(engine *Engine) error {
	if orm.Loaded() {
		return nil
	}
	return orm.Refresh(engine)
}

func (orm *ORM) Refresh(engine *Engine) error {
	id := orm.elem.Field(1).Uint()
	if id == 0 {
		return nil
	}
	return engine.GetByID(id, orm.value.Interface())
}

func (orm *ORM) ForceMarkToDelete() {
	orm.dBData["_delete"] = true
}

type cachedQueryDefinition struct {
	Max    int
	Query  string
	Fields []string
}

type TableSchema struct {
	TableName        string
	MysqlPoolName    string
	t                reflect.Type
	Tags             map[string]map[string]string
	cachedIndexes    map[string]*cachedQueryDefinition
	cachedIndexesOne map[string]*cachedQueryDefinition
	cachedIndexesAll map[string]*cachedQueryDefinition
	columnNames      []string
	columnPathMap    map[string]string
	refOne           []string
	columnsStamp     string
	localCacheName   string
	redisCacheName   string
	cachePrefix      string
	hasFakeDelete    bool
}

type indexDB struct {
	Skip      sql.NullString
	NonUnique uint8
	KeyName   string
	Seq       int
	Column    string
}

type index struct {
	Unique  bool
	Columns map[int]string
}

type foreignIndex struct {
	Column         string
	Table          string
	ParentDatabase string
	OnDelete       string
}

type foreignKeyDB struct {
	ConstraintName        string
	ColumnName            string
	ReferencedTableName   string
	ReferencedTableSchema string
	OnDelete              string
}

func getTableSchema(c *Config, entityOrType interface{}) *TableSchema {
	asType, ok := entityOrType.(reflect.Type)
	if ok {
		return getTableSchemaFromValue(c, asType)
	}
	return getTableSchemaFromValue(c, reflect.TypeOf(entityOrType))
}

func getTableSchemaFromValue(c *Config, entityType reflect.Type) *TableSchema {
	return c.tableSchemas[entityType]
}

func (tableSchema *TableSchema) GetType() reflect.Type {
	return tableSchema.t
}

func (tableSchema *TableSchema) DropTable(engine *Engine) error {
	pool := tableSchema.GetMysql(engine)
	_, err := pool.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`;", pool.GetDatabaseName(), tableSchema.TableName))
	return err
}

func (tableSchema *TableSchema) TruncateTable(engine *Engine) error {
	pool := tableSchema.GetMysql(engine)
	_, err := pool.Exec("SET FOREIGN_KEY_CHECKS = 0")
	if err != nil {
		return err
	}
	_, err = pool.Exec(fmt.Sprintf("TRUNCATE TABLE `%s`.`%s`;",
		pool.GetDatabaseName(), tableSchema.TableName))
	if err != nil {
		return err
	}
	_, err = pool.Exec("SET FOREIGN_KEY_CHECKS = 1")
	if err != nil {
		return err
	}
	return nil
}

func (tableSchema *TableSchema) UpdateSchema(engine *Engine) error {
	pool := tableSchema.GetMysql(engine)
	has, alters, err := tableSchema.GetSchemaChanges(engine)
	if err != nil {
		return err
	}
	if has {
		for _, alter := range alters {
			_, err := pool.Exec(alter.SQL)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (tableSchema *TableSchema) UpdateSchemaAndTruncateTable(engine *Engine) error {
	err := tableSchema.UpdateSchema(engine)
	if err != nil {
		return err
	}
	pool := tableSchema.GetMysql(engine)
	_, err = pool.Exec(fmt.Sprintf("TRUNCATE TABLE `%s`.`%s`;", pool.GetDatabaseName(), tableSchema.TableName))
	return err
}

func (tableSchema *TableSchema) GetMysql(engine *Engine) DB {
	db, _ := engine.GetMysql(tableSchema.MysqlPoolName)
	return db
}

func (tableSchema *TableSchema) GetLocalCache(engine *Engine) (cache *LocalCache, has bool) {
	if tableSchema.localCacheName == "" {
		return nil, false
	}
	return engine.GetLocalCache(tableSchema.localCacheName)
}

func (tableSchema *TableSchema) GetRedisCacheContainer(engine *Engine) (cache *RedisCache, has bool) {
	if tableSchema.redisCacheName == "" {
		return nil, false
	}
	return engine.GetRedis(tableSchema.redisCacheName)
}

func (tableSchema *TableSchema) GetReferences() []string {
	return tableSchema.refOne
}

func (tableSchema TableSchema) getCacheKey(id uint64) string {
	return fmt.Sprintf("%s%s:%d", tableSchema.cachePrefix, tableSchema.columnsStamp, id)
}

func (tableSchema TableSchema) getCacheKeySearch(indexName string, parameters ...interface{}) string {
	hash := fnv1a.HashString32(fmt.Sprintf("%v", parameters))
	return fmt.Sprintf("%s_%s_%d", tableSchema.cachePrefix, indexName, hash)
}

func (tableSchema *TableSchema) GetColumns() map[string]string {
	return tableSchema.columnPathMap
}

func (tableSchema *TableSchema) GetUsage(config *Config) (map[reflect.Type][]string, error) {
	results := make(map[reflect.Type][]string)
	if config.entities != nil {
		for _, t := range config.entities {
			schema, _ := config.GetTableSchema(t)
			for _, columnName := range schema.refOne {
				ref, has := schema.Tags[columnName]["ref"]
				if has && ref == tableSchema.t.String() {
					if results[t] == nil {
						results[t] = make([]string, 0)
					}
					results[t] = append(results[t], columnName)
				}
			}
		}
	}
	return results, nil
}

func (tableSchema *TableSchema) GetSchemaChanges(engine *Engine) (has bool, alters []Alter, err error) {
	indexes := make(map[string]*index)
	foreignKeys := make(map[string]*foreignIndex)
	columns, err := tableSchema.checkStruct(engine, tableSchema.t, indexes, foreignKeys, "")

	if err != nil {
		return false, nil, err
	}
	var newIndexes []string
	var newForeignKeys []string
	pool, _ := engine.GetMysql(tableSchema.MysqlPoolName)
	createTableSQL := fmt.Sprintf("CREATE TABLE `%s`.`%s` (\n", pool.GetDatabaseName(), tableSchema.TableName)
	createTableForiegnKeysSQL := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", pool.GetDatabaseName(), tableSchema.TableName)
	columns[0][1] += " AUTO_INCREMENT"
	for _, value := range columns {
		createTableSQL += fmt.Sprintf("  %s,\n", value[1])
	}
	for keyName, indexEntity := range indexes {
		newIndexes = append(newIndexes, tableSchema.buildCreateIndexSQL(keyName, indexEntity))
	}
	sort.Strings(newIndexes)
	for _, value := range newIndexes {
		createTableSQL += fmt.Sprintf("  %s,\n", value[4:])
	}
	for keyName, foreignKey := range foreignKeys {
		newForeignKeys = append(newForeignKeys, tableSchema.buildCreateForeignKeySQL(keyName, foreignKey))
	}
	sort.Strings(newForeignKeys)
	for _, value := range newForeignKeys {
		createTableForiegnKeysSQL += fmt.Sprintf("  %s,\n", value)
	}

	createTableSQL += "  PRIMARY KEY (`ID`)\n"
	createTableSQL += ") ENGINE=InnoDB DEFAULT CHARSET=utf8;"

	var skip string
	err = pool.QueryRow(fmt.Sprintf("SHOW TABLES LIKE '%s'", tableSchema.TableName)).Scan(&skip)
	hasTable := true
	if err != nil {
		hasTable = false
	}

	if !hasTable {
		alters = []Alter{{SQL: createTableSQL, Safe: true, Pool: tableSchema.MysqlPoolName}}
		if len(newForeignKeys) > 0 {
			createTableForiegnKeysSQL = strings.TrimRight(createTableForiegnKeysSQL, ",\n") + ";"
			alters = append(alters, Alter{SQL: createTableForiegnKeysSQL, Safe: true, Pool: tableSchema.MysqlPoolName})
		}
		has = true
		err = nil
		return
	}
	newIndexes = make([]string, 0)
	newForeignKeys = make([]string, 0)

	var tableDBColumns = make([][2]string, 0)
	var createTableDB string
	err = pool.QueryRow(fmt.Sprintf("SHOW CREATE TABLE `%s`", tableSchema.TableName)).Scan(&skip, &createTableDB)
	if err != nil {
		return false, nil, err
	}
	lines := strings.Split(createTableDB, "\n")
	for x := 1; x < len(lines); x++ {
		if lines[x][2] != 96 {
			continue
		}
		var line = strings.TrimRight(lines[x], ",")
		line = strings.TrimLeft(line, " ")
		var columnName = strings.Split(line, "`")[1]
		tableDBColumns = append(tableDBColumns, [2]string{columnName, line})
	}

	var rows []indexDB
	/* #nosec */
	results, def, err := pool.Query(fmt.Sprintf("SHOW INDEXES FROM `%s`", tableSchema.TableName))
	if err != nil {
		return false, nil, err
	}
	defer def()
	for results.Next() {
		var row indexDB
		err = results.Scan(&row.Skip, &row.NonUnique, &row.KeyName, &row.Seq, &row.Column, &row.Skip, &row.Skip, &row.Skip, &row.Skip, &row.Skip, &row.Skip, &row.Skip, &row.Skip)
		if err != nil {
			return false, nil, err
		}
		rows = append(rows, row)
	}
	err = results.Err()
	if err != nil {
		return false, nil, err
	}
	var indexesDB = make(map[string]*index)
	for _, value := range rows {
		current, has := indexesDB[value.KeyName]
		if !has {
			current = &index{Unique: value.NonUnique == 0, Columns: map[int]string{value.Seq: value.Column}}
			indexesDB[value.KeyName] = current
		} else {
			current.Columns[value.Seq] = value.Column
		}
	}

	foreignKeysDB, err := getForeignKeys(engine, createTableDB, tableSchema.TableName, tableSchema.MysqlPoolName)
	if err != nil {
		return false, nil, err
	}

	var newColumns []string
	var changedColumns [][2]string

	hasAlters := false
	for key, value := range columns {
		var tableColumn string
		if key < len(tableDBColumns) {
			tableColumn = tableDBColumns[key][1]
		}
		if tableColumn == value[1] {
			continue
		}
		hasName := -1
		hasDefinition := -1
		for z, v := range tableDBColumns {
			if v[1] == value[1] {
				hasDefinition = z
			}
			if v[0] == value[0] {
				hasName = z
			}
		}
		if hasName == -1 {
			alter := fmt.Sprintf("ADD COLUMN %s", value[1])
			if key > 0 {
				alter += fmt.Sprintf(" AFTER `%s`", columns[key-1][0])
			}
			newColumns = append(newColumns, alter)
			hasAlters = true
		} else {
			if hasDefinition == -1 {
				alter := fmt.Sprintf("CHANGE COLUMN `%s` %s", value[0], value[1])
				if key > 0 {
					/* #nosec */
					alter += fmt.Sprintf(" AFTER `%s`", columns[key-1][0])
				}
				/* #nosec */
				changedColumns = append(changedColumns, [2]string{alter, fmt.Sprintf("CHANGED FROM %s", tableDBColumns[hasName][1])})
				hasAlters = true
			} else {
				alter := fmt.Sprintf("CHANGE COLUMN `%s` %s", value[0], value[1])
				if key > 0 {
					alter += fmt.Sprintf(" AFTER `%s`", columns[key-1][0])
				}
				changedColumns = append(changedColumns, [2]string{alter, "CHANGED ORDER"})
				hasAlters = true
			}
		}
	}
	droppedColumns := make([]string, 0)
OUTER:
	for _, value := range tableDBColumns {
		for _, v := range columns {
			if v[0] == value[0] {
				continue OUTER
			}
		}
		droppedColumns = append(droppedColumns, fmt.Sprintf("DROP COLUMN `%s`", value[0]))
		hasAlters = true
	}

	var droppedIndexes []string
	for keyName, indexEntity := range indexes {
		indexDB, has := indexesDB[keyName]
		if !has {
			newIndexes = append(newIndexes, tableSchema.buildCreateIndexSQL(keyName, indexEntity))
			hasAlters = true
		} else {
			addIndexSQLEntity := tableSchema.buildCreateIndexSQL(keyName, indexEntity)
			addIndexSQLDB := tableSchema.buildCreateIndexSQL(keyName, indexDB)
			if addIndexSQLEntity != addIndexSQLDB {
				droppedIndexes = append(droppedIndexes, fmt.Sprintf("DROP INDEX `%s`", keyName))
				newIndexes = append(newIndexes, addIndexSQLEntity)
				hasAlters = true
			}
		}
	}

	var droppedForeignKeys []string
	for keyName, indexEntity := range foreignKeys {
		indexDB, has := foreignKeysDB[keyName]
		if !has {
			newForeignKeys = append(newForeignKeys, tableSchema.buildCreateForeignKeySQL(keyName, indexEntity))
			hasAlters = true
		} else {
			addIndexSQLEntity := tableSchema.buildCreateForeignKeySQL(keyName, indexEntity)
			addIndexSQLDB := tableSchema.buildCreateForeignKeySQL(keyName, indexDB)
			if addIndexSQLEntity != addIndexSQLDB {
				droppedForeignKeys = append(droppedForeignKeys, fmt.Sprintf("DROP FOREIGN KEY `%s`", keyName))
				newForeignKeys = append(newForeignKeys, addIndexSQLEntity)
				hasAlters = true
			}
		}
	}
	for keyName := range indexesDB {
		_, has := indexes[keyName]
		if !has && keyName != "PRIMARY" {
			_, has = foreignKeys[keyName]
			if !has {
				droppedIndexes = append(droppedIndexes, fmt.Sprintf("DROP INDEX `%s`", keyName))
				hasAlters = true
			}
		}
	}
	for keyName := range foreignKeysDB {
		_, has := foreignKeys[keyName]
		if !has {
			droppedForeignKeys = append(droppedForeignKeys, fmt.Sprintf("DROP FOREIGN KEY `%s`", keyName))
			hasAlters = true
		}
	}
	if !hasAlters {
		return
	}
	alterSQL := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", pool.GetDatabaseName(), tableSchema.TableName)
	newAlters := make([]string, 0)
	comments := make([]string, 0)
	hasAlterNormal := false
	hasAlterAddForeignKey := false
	hasAlterRemoveForeignKey := false

	alterSQLAddForeignKey := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", pool.GetDatabaseName(), tableSchema.TableName)
	newAltersAddForeignKey := make([]string, 0)
	alterSQLRemoveForeignKey := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", pool.GetDatabaseName(), tableSchema.TableName)
	newAltersRemoveForeignKey := make([]string, 0)

	for _, value := range droppedColumns {
		newAlters = append(newAlters, fmt.Sprintf("    %s", value))
		comments = append(comments, "")
		hasAlterNormal = true
	}
	for _, value := range newColumns {
		newAlters = append(newAlters, fmt.Sprintf("    %s", value))
		comments = append(comments, "")
		hasAlterNormal = true
	}
	for _, value := range changedColumns {
		newAlters = append(newAlters, fmt.Sprintf("    %s", value[0]))
		comments = append(comments, value[1])
	}
	sort.Strings(droppedIndexes)
	for _, value := range droppedIndexes {
		newAlters = append(newAlters, fmt.Sprintf("    %s", value))
		comments = append(comments, "")
		hasAlterNormal = true
	}
	sort.Strings(droppedForeignKeys)
	for _, value := range droppedForeignKeys {
		newAltersRemoveForeignKey = append(newAltersRemoveForeignKey, fmt.Sprintf("    %s", value))
		hasAlterRemoveForeignKey = true
	}
	sort.Strings(newIndexes)
	for _, value := range newIndexes {
		newAlters = append(newAlters, fmt.Sprintf("    %s", value))
		comments = append(comments, "")
		hasAlterNormal = true
	}
	sort.Strings(newForeignKeys)
	for _, value := range newForeignKeys {
		newAltersAddForeignKey = append(newAltersAddForeignKey, fmt.Sprintf("    %s", value))
		hasAlterAddForeignKey = true
	}

	for x := 0; x < len(newAlters)-1; x++ {
		hasAlterNormal = true
		alterSQL += newAlters[x] + ","
		if comments[x] != "" {
			alterSQL += fmt.Sprintf("/*%s*/", comments[x])
		}
		alterSQL += "\n"
	}
	lastIndex := len(newAlters) - 1
	if lastIndex >= 0 {
		hasAlterNormal = true
		alterSQL += newAlters[lastIndex] + ";"
		if comments[lastIndex] != "" {
			alterSQL += fmt.Sprintf("/*%s*/", comments[lastIndex])
		}
	}

	for x := 0; x < len(newAltersAddForeignKey); x++ {
		alterSQLAddForeignKey += newAltersAddForeignKey[x] + ","
		alterSQLAddForeignKey += "\n"
	}
	for x := 0; x < len(newAltersRemoveForeignKey); x++ {
		alterSQLRemoveForeignKey += newAltersRemoveForeignKey[x] + ","
		alterSQLRemoveForeignKey += "\n"
	}

	alters = make([]Alter, 0)
	if hasAlterNormal {
		safe := false
		if len(droppedColumns) == 0 && len(changedColumns) == 0 {
			safe = true
		} else {
			isEmpty, err := tableSchema.isTableEmpty(engine)
			if err != nil {
				return false, nil, err
			}
			safe = isEmpty
		}
		alters = append(alters, Alter{SQL: alterSQL, Safe: safe, Pool: tableSchema.MysqlPoolName})
	}
	if hasAlterRemoveForeignKey {
		alterSQLRemoveForeignKey = strings.TrimRight(alterSQLRemoveForeignKey, ",\n") + ";"
		alters = append(alters, Alter{SQL: alterSQLRemoveForeignKey, Safe: true, Pool: tableSchema.MysqlPoolName})
	}
	if hasAlterAddForeignKey {
		alterSQLAddForeignKey = strings.TrimRight(alterSQLAddForeignKey, ",\n") + ";"
		alters = append(alters, Alter{SQL: alterSQLAddForeignKey, Safe: true, Pool: tableSchema.MysqlPoolName})
	}

	has = true
	return has, alters, nil
}

func initTableSchema(registry *Registry, entityType reflect.Type) (*TableSchema, error) {
	tags, columnNames, columnPathMap := extractTags(registry, entityType, "")
	oneRefs := make([]string, 0)
	columnsStamp := fmt.Sprintf("%d", fnv1a.HashString32(fmt.Sprintf("%v", columnNames)))
	mysql, has := tags["Orm"]["mysql"]
	if !has {
		mysql = "default"
	}
	_, has = registry.sqlClients[mysql]
	if !has {
		return nil, fmt.Errorf("unknown mysql pool '%s'", mysql)
	}
	table, has := tags["Orm"]["table"]
	if !has {
		table = entityType.Name()
	}
	localCache := ""
	redisCache := ""
	userValue, has := tags["Orm"]["localCache"]
	if has {
		if userValue == "true" {
			userValue = "default"
		}
		localCache = userValue
	}
	if localCache != "" {
		_, has = registry.localCacheContainers[localCache]
		if !has {
			return nil, fmt.Errorf("unknown local cache pool '%s'", localCache)
		}
	}
	userValue, has = tags["Orm"]["redisCache"]
	if has {
		if userValue == "true" {
			userValue = "default"
		}
		redisCache = userValue
	}
	if redisCache != "" {
		_, has = registry.redisServers[redisCache]
		if !has {
			return nil, fmt.Errorf("unknown redis pool '%s'", redisCache)
		}
	}

	cachePrefix := ""
	if mysql != "default" {
		cachePrefix = mysql
	}
	cachePrefix += table
	cachedQueries := make(map[string]*cachedQueryDefinition)
	cachedQueriesOne := make(map[string]*cachedQueryDefinition)
	cachedQueriesAll := make(map[string]*cachedQueryDefinition)
	hasFakeDelete := false
	fakeDeleteField, has := entityType.FieldByName("FakeDelete")
	if has && fakeDeleteField.Type.String() == "bool" {
		hasFakeDelete = true
	}
	for key, values := range tags {
		isOne := false
		query, has := values["query"]
		if !has {
			query, has = values["queryOne"]
			isOne = true
		}
		fields := make([]string, 0)
		if has {
			re := regexp.MustCompile(":([A-Za-z0-9])+")
			variables := re.FindAllString(query, -1)
			for _, variable := range variables {
				fieldName := variable[1:]
				if fieldName != "ID" {
					fields = append(fields, fieldName)
				}
				query = strings.Replace(query, variable, fmt.Sprintf("`%s`", fieldName), 1)
			}
			if hasFakeDelete && len(variables) > 0 {
				fields = append(fields, "FakeDelete")
			}
			if query == "" {
				query = "1 ORDER BY `ID`"
			}
			if !isOne {
				max := 50000
				maxAttribute, has := values["max"]
				if has {
					maxFromUser, err := strconv.Atoi(maxAttribute)
					if err != nil {
						return nil, err
					}
					max = maxFromUser
				}
				def := &cachedQueryDefinition{max, query, fields}
				cachedQueries[key] = def
				cachedQueriesAll[key] = def
			} else {
				def := &cachedQueryDefinition{1, query, fields}
				cachedQueriesOne[key] = def
				cachedQueriesAll[key] = def
			}
		}
		_, has = values["ref"]
		if has {
			oneRefs = append(oneRefs, key)
		}
	}
	tableSchema := &TableSchema{TableName: table,
		MysqlPoolName:    mysql,
		t:                entityType,
		Tags:             tags,
		columnNames:      columnNames,
		columnPathMap:    columnPathMap,
		columnsStamp:     columnsStamp,
		cachedIndexes:    cachedQueries,
		cachedIndexesOne: cachedQueriesOne,
		cachedIndexesAll: cachedQueriesAll,
		localCacheName:   localCache,
		redisCacheName:   redisCache,
		refOne:           oneRefs,
		cachePrefix:      cachePrefix,
		hasFakeDelete:    hasFakeDelete}
	return tableSchema, nil
}

func extractTags(registry *Registry, entityType reflect.Type, prefix string) (fields map[string]map[string]string,
	columnNames []string, columnPathMap map[string]string) {
	fields = make(map[string]map[string]string)
	columnNames = make([]string, 0)
	columnPathMap = make(map[string]string)
	for i := 0; i < entityType.NumField(); i++ {
		field := entityType.Field(i)

		subTags, subFields, subMap := extractTag(registry, field)
		for k, v := range subTags {
			fields[prefix+k] = v
		}
		_, hasIgnore := fields[field.Name]["ignore"]
		if hasIgnore {
			continue
		}
		refOne := ""
		hasRef := false
		if field.Type.Kind().String() == "ptr" {
			refName := field.Type.Elem().String()
			_, hasRef = registry.entities[refName]
			if hasRef {
				refOne = refName
			}
		}

		query, hasQuery := field.Tag.Lookup("query")
		queryOne, hasQueryOne := field.Tag.Lookup("queryOne")
		if subFields != nil {
			if !hasQuery && !hasQueryOne {
				columnNames = append(columnNames, subFields...)
			}
			for k, v := range subMap {
				columnPathMap[k] = v
			}
		} else if i != 0 || prefix != "" {
			if !hasQuery && !hasQueryOne {
				columnNames = append(columnNames, prefix+field.Name)
				path := strings.TrimLeft(prefix+"."+field.Name, ".")
				if hasRef {
					path += ".ID"
				}
				columnPathMap[path] = prefix + field.Name
			}
		}

		if hasQuery {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["query"] = query
		}
		if hasQueryOne {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["queryOne"] = queryOne
		}
		if hasRef {
			if fields[field.Name] == nil {
				fields[field.Name] = make(map[string]string)
			}
			fields[field.Name]["ref"] = refOne
		}
	}
	return
}

func extractTag(registry *Registry, field reflect.StructField) (map[string]map[string]string, []string, map[string]string) {
	tag, ok := field.Tag.Lookup("orm")
	if ok {
		args := strings.Split(tag, ";")
		length := len(args)
		var attributes = make(map[string]string, length)
		for j := 0; j < length; j++ {
			arg := strings.Split(args[j], "=")
			if len(arg) == 1 {
				attributes[arg[0]] = "true"
			} else {
				attributes[arg[0]] = arg[1]
			}
		}
		return map[string]map[string]string{field.Name: attributes}, nil, nil
	} else if field.Type.Kind().String() == "struct" {
		if field.Type.String() != "time.Time" {
			return extractTags(registry, field.Type, field.Name)
		}
	}
	return make(map[string]map[string]string), nil, nil
}

func (tableSchema *TableSchema) checkColumn(engine *Engine, t reflect.Type, field *reflect.StructField, indexes map[string]*index,
	foreignKeys map[string]*foreignIndex, prefix string) ([][2]string, error) {
	var definition string
	var addNotNullIfNotSet bool
	addDefaultNullIfNullable := true
	defaultValue := "nil"
	var typeAsString = field.Type.String()
	columnName := prefix + field.Name

	attributes := tableSchema.Tags[columnName]

	_, has := attributes["ignore"]
	if has {
		return nil, nil
	}

	keys := []string{"index", "unique"}
	var refOneSchema *TableSchema
	for _, key := range keys {
		indexAttribute, has := attributes[key]
		unique := key == "unique"
		if key == "index" && field.Type.Kind().String() == "ptr" {
			refOneSchema, _ = engine.config.GetTableSchema(field.Type.Elem())
			if refOneSchema != nil {
				onDelete := "RESTRICT"
				_, hasCascade := attributes["cascade"]
				if hasCascade {
					onDelete = "CASCADE"
				}
				pool := refOneSchema.GetMysql(engine)
				foreignKey := &foreignIndex{Column: field.Name, Table: refOneSchema.TableName,
					ParentDatabase: pool.GetDatabaseName(), OnDelete: onDelete}
				name := fmt.Sprintf("%s:%s:%s", pool.GetDatabaseName(), tableSchema.TableName, field.Name)
				foreignKeys[name] = foreignKey
			}
		}

		if has {
			indexColumns := strings.Split(indexAttribute, ",")
			for _, value := range indexColumns {
				indexColumn := strings.Split(value, ":")
				location := 1
				if len(indexColumn) > 1 {
					userLocation, err := strconv.Atoi(indexColumn[1])
					if err != nil {
						return nil, err
					}
					location = userLocation
				}
				current, has := indexes[indexColumn[0]]
				if !has {
					current = &index{Unique: unique, Columns: map[int]string{location: field.Name}}
					indexes[indexColumn[0]] = current
				} else {
					current.Columns[location] = field.Name
				}
			}
		}
	}

	if refOneSchema != nil {
		hasValidIndex := false
		for _, i := range indexes {
			if i.Columns[1] == columnName {
				hasValidIndex = true
				break
			}
		}
		if !hasValidIndex {
			indexes[columnName] = &index{Unique: false, Columns: map[int]string{1: columnName}}
		}
	}

	required, hasRequired := attributes["required"]
	isRequired := hasRequired && required == "true"

	var err error
	switch typeAsString {
	case "uint",
		"uint8",
		"uint32",
		"uint64",
		"int8",
		"int16",
		"int32",
		"int64",
		"int":
		definition, addNotNullIfNotSet, defaultValue = tableSchema.handleInt(typeAsString, attributes)
	case "uint16":
		if attributes["year"] == "true" {
			if isRequired {
				return [][2]string{{columnName, fmt.Sprintf("`%s` year(4) NOT NULL DEFAULT '0000'", columnName)}}, nil
			}
			return [][2]string{{columnName, fmt.Sprintf("`%s` year(4) DEFAULT NULL", columnName)}}, nil
		}
		definition, addNotNullIfNotSet, defaultValue = tableSchema.handleInt(typeAsString, attributes)
	case "bool":
		if columnName == "FakeDelete" {
			return nil, nil
		}
		definition, addNotNullIfNotSet, defaultValue = "tinyint(1)", true, "'0'"
	case "string", "[]string":
		definition, addNotNullIfNotSet, addDefaultNullIfNullable, defaultValue, err = tableSchema.handleString(engine.config, attributes, false)
		if err != nil {
			return nil, err
		}
	case "interface {}":
		definition, addNotNullIfNotSet, addDefaultNullIfNullable, defaultValue, err = tableSchema.handleString(engine.config, attributes, true)
		if err != nil {
			return nil, err
		}
	case "float32":
		definition, addNotNullIfNotSet, defaultValue = tableSchema.handleFloat("float", attributes)
	case "float64":
		definition, addNotNullIfNotSet, defaultValue = tableSchema.handleFloat("double", attributes)
	case "time.Time":
		definition, addNotNullIfNotSet, addDefaultNullIfNullable, defaultValue = tableSchema.handleTime(attributes, false)
	case "*time.Time":
		definition, addNotNullIfNotSet, addDefaultNullIfNullable, defaultValue = tableSchema.handleTime(attributes, true)
	case "[]uint8":
		definition = "blob"
		addDefaultNullIfNullable = false
	case "*orm.CachedQuery":
		return nil, nil
	default:
		kind := field.Type.Kind().String()
		valid := false
		if kind == "struct" {
			structFields, err := tableSchema.checkStruct(engine, field.Type, indexes, foreignKeys, field.Name)
			if err != nil {
				return nil, err
			}
			return structFields, nil
		} else if kind == "ptr" {
			subSchema, has := engine.config.GetTableSchema(field.Type.Elem())
			if has {
				definition = tableSchema.handleReferenceOne(subSchema, attributes)
				addNotNullIfNotSet = false
				addDefaultNullIfNullable = true
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("unsupported field type: %s %s in %s", field.Name, field.Type.String(), t.String())
		}
	}
	isNotNull := false
	if addNotNullIfNotSet || isRequired {
		definition += " NOT NULL"
		isNotNull = true
	}
	if defaultValue != "nil" && columnName != "ID" {
		definition += " DEFAULT " + defaultValue
	} else if !isNotNull && addDefaultNullIfNullable {
		definition += " DEFAULT NULL"
	}
	return [][2]string{{columnName, fmt.Sprintf("`%s` %s", columnName, definition)}}, nil
}

func (tableSchema *TableSchema) handleInt(typeAsString string, attributes map[string]string) (string, bool, string) {
	return tableSchema.convertIntToSchema(typeAsString, attributes), true, "'0'"
}

func (tableSchema *TableSchema) handleFloat(floatDefinition string, attributes map[string]string) (string, bool, string) {
	decimal, hasDecimal := attributes["decimal"]
	var definition string
	defaultValue := "'0'"
	if hasDecimal {
		decimalArgs := strings.Split(decimal, ",")
		definition = fmt.Sprintf("decimal(%s,%s)", decimalArgs[0], decimalArgs[1])
		defaultValue = fmt.Sprintf("'%s'", fmt.Sprintf("%."+decimalArgs[1]+"f", float32(0)))
	} else {
		definition = floatDefinition
	}
	unsigned, hasUnsigned := attributes["unsigned"]
	if !hasUnsigned || unsigned == "true" {
		definition += " unsigned"
	}
	return definition, true, defaultValue
}

func (tableSchema *TableSchema) handleString(config *Config, attributes map[string]string, forceMax bool) (string, bool, bool, string, error) {
	var definition string
	enum, hasEnum := attributes["enum"]
	if hasEnum {
		return tableSchema.handleSetEnum(config, "enum", enum, attributes)
	}
	set, haSet := attributes["set"]
	if haSet {
		return tableSchema.handleSetEnum(config, "set", set, attributes)
	}
	var addDefaultNullIfNullable = true
	length, hasLength := attributes["length"]
	if !hasLength {
		length = "255"
	}
	if forceMax || length == "max" {
		definition = "mediumtext"
		addDefaultNullIfNullable = false
	} else {
		i, err := strconv.Atoi(length)
		if err != nil {
			return "", false, false, "", err
		}
		if i > 65535 {
			return "", false, false, "", fmt.Errorf("length to heigh: %s", length)
		}
		definition = fmt.Sprintf("varchar(%s)", strconv.Itoa(i))
	}
	defaultValue := "nil"
	required, hasRequired := attributes["required"]
	if hasRequired && required == "true" {
		defaultValue = "''"
	}
	return definition, false, addDefaultNullIfNullable, defaultValue, nil
}

func (tableSchema *TableSchema) handleSetEnum(config *Config, fieldType string, attribute string, attributes map[string]string) (string, bool, bool, string, error) {
	if config.enums == nil {
		return "", false, false, "", fmt.Errorf("unregistered enum %s", attribute)
	}
	enum, has := config.enums[attribute]
	if !has {
		return "", false, false, "", fmt.Errorf("unregistered enum %s", attribute)
	}
	values := make([]string, 0)
	for i := 0; i < enum.Type().NumField(); i++ {
		values = append(values, enum.Field(i).String())
	}

	var definition = fieldType + "("
	for key, value := range values {
		if key > 0 {
			definition += ","
		}
		definition += fmt.Sprintf("'%s'", value)
	}
	definition += ")"
	required, hasRequired := attributes["required"]
	defaultValue := "nil"
	if hasRequired && required == "true" {
		defaultValue = fmt.Sprintf("'%s'", values[0])
	}
	return definition, hasRequired && required == "true", true, defaultValue, nil
}

func (tableSchema *TableSchema) handleTime(attributes map[string]string, nullable bool) (string, bool, bool, string) {
	t := attributes["time"]
	defaultValue := "nil"
	if t == "true" {
		return "datetime", !nullable, true, "nil"
	}
	if !nullable {
		defaultValue = "'0001-01-01'"
	}
	return "date", !nullable, true, defaultValue
}

func (tableSchema *TableSchema) handleReferenceOne(schema *TableSchema, attributes map[string]string) string {
	return tableSchema.convertIntToSchema(schema.t.Field(1).Type.String(), attributes)
}

func (tableSchema *TableSchema) convertIntToSchema(typeAsString string, attributes map[string]string) string {
	switch typeAsString {
	case "uint":
		return "int(10) unsigned"
	case "uint8":
		return "tinyint(3) unsigned"
	case "uint16":
		return "smallint(5) unsigned"
	case "uint32":
		if attributes["mediumint"] == "true" {
			return "mediumint(8) unsigned"
		}
		return "int(10) unsigned"
	case "uint64":
		return "bigint(20) unsigned"
	case "int8":
		return "tinyint(4)"
	case "int16":
		return "smallint(6)"
	case "int32":
		if attributes["mediumint"] == "true" {
			return "mediumint(9)"
		}
		return "int(11)"
	case "int64":
		return "bigint(20)"
	default:
		return "int(11)"
	}
}

func (tableSchema *TableSchema) checkStruct(engine *Engine, t reflect.Type, indexes map[string]*index,
	foreignKeys map[string]*foreignIndex, prefix string) ([][2]string, error) {
	columns := make([][2]string, 0, t.NumField())
	max := t.NumField() - 1
	for i := 0; i <= max; i++ {
		if i == 0 && prefix == "" {
			continue
		}
		field := t.Field(i)
		fieldColumns, err := tableSchema.checkColumn(engine, t, &field, indexes, foreignKeys, prefix)
		if err != nil {
			return nil, err
		}
		if fieldColumns != nil {
			columns = append(columns, fieldColumns...)
		}
	}
	if tableSchema.hasFakeDelete {
		def := fmt.Sprintf("`FakeDelete` %s unsigned NOT NULL DEFAULT '0'", strings.Split(columns[0][1], " ")[1])
		columns = append(columns, [2]string{"FakeDelete", def})
	}
	return columns, nil
}

func (tableSchema *TableSchema) buildCreateIndexSQL(keyName string, definition *index) string {
	var indexColumns []string
	for i := 1; i <= 100; i++ {
		value, has := definition.Columns[i]
		if has {
			indexColumns = append(indexColumns, fmt.Sprintf("`%s`", value))
		} else {
			break
		}
	}
	indexType := "INDEX"
	if definition.Unique {
		indexType = "UNIQUE " + indexType
	}
	return fmt.Sprintf("ADD %s `%s` (%s)", indexType, keyName, strings.Join(indexColumns, ","))
}

func (tableSchema *TableSchema) buildCreateForeignKeySQL(keyName string, definition *foreignIndex) string {
	/* #nosec */
	return fmt.Sprintf("ADD CONSTRAINT `%s` FOREIGN KEY (`%s`) REFERENCES `%s`.`%s` (`ID`) ON DELETE %s",
		keyName, definition.Column, definition.ParentDatabase, definition.Table, definition.OnDelete)
}

func (tableSchema *TableSchema) isTableEmpty(engine *Engine) (bool, error) {
	var lastID uint64
	pool := tableSchema.GetMysql(engine)
	/* #nosec */
	err := pool.QueryRow(fmt.Sprintf("SELECT `ID` FROM `%s` LIMIT 1", tableSchema.TableName)).Scan(&lastID)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func getForeignKeys(engine *Engine, createTableDB string, tableName string, poolName string) (map[string]*foreignIndex, error) {
	var rows2 []foreignKeyDB
	query := "SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_TABLE_SCHEMA " +
		"FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_SCHEMA IS NOT NULL " +
		"AND TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'"
	pool, _ := engine.GetMysql(poolName)
	results, def, err := pool.Query(fmt.Sprintf(query, pool.GetDatabaseName(), tableName))
	if err != nil {
		return nil, err
	}
	defer def()
	for results.Next() {
		var row foreignKeyDB
		err = results.Scan(&row.ConstraintName, &row.ColumnName, &row.ReferencedTableName, &row.ReferencedTableSchema)
		if err != nil {
			return nil, err
		}
		row.OnDelete = "RESTRICT"
		for _, line := range strings.Split(createTableDB, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, ","))
			if strings.Index(line, fmt.Sprintf("CONSTRAINT `%s`", row.ConstraintName)) == 0 {
				words := strings.Split(line, " ")
				if strings.ToUpper(words[len(words)-2]) == "DELETE" {
					row.OnDelete = strings.ToUpper(words[len(words)-1])
				}
			}
		}
		rows2 = append(rows2, row)
	}
	err = results.Err()
	if err != nil {
		return nil, err
	}
	var foreignKeysDB = make(map[string]*foreignIndex)
	for _, value := range rows2 {
		foreignKey := &foreignIndex{ParentDatabase: value.ReferencedTableSchema, Table: value.ReferencedTableName,
			Column: value.ColumnName, OnDelete: value.OnDelete}
		foreignKeysDB[value.ConstraintName] = foreignKey
	}
	return foreignKeysDB, nil
}

func getDropForeignKeysAlter(engine *Engine, tableName string, poolName string) (string, error) {
	var skip string
	var createTableDB string
	pool, _ := engine.GetMysql(poolName)
	err := pool.QueryRow(fmt.Sprintf("SHOW CREATE TABLE `%s`", tableName)).Scan(&skip, &createTableDB)
	if err != nil {
		return "", err
	}
	alter := fmt.Sprintf("ALTER TABLE `%s`.`%s`\n", pool.GetDatabaseName(), tableName)
	foreignKeysDB, err := getForeignKeys(engine, createTableDB, tableName, poolName)
	if err != nil {
		return "", err
	}
	if len(foreignKeysDB) == 0 {
		return "", nil
	}
	droppedForeignKeys := make([]string, 0)
	for keyName := range foreignKeysDB {
		droppedForeignKeys = append(droppedForeignKeys, fmt.Sprintf("DROP FOREIGN KEY `%s`", keyName))
	}
	alter += strings.Join(droppedForeignKeys, ",\t\n")
	alter = strings.TrimRight(alter, ",") + ";"
	return alter, nil
}
