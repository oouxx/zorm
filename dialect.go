package zorm

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//wrapPageSQL 包装分页的SQL语句
//wrapPageSQL SQL statement for wrapping paging
func wrapPageSQL(dbType string, sqlstr string, page *Page) (string, error) {
	//新的分页方法都已经不需要order by了,不再强制检查
	//The new paging method does not require 'order by' anymore, no longer mandatory check.
	//
	/*
		locOrderBy := findOrderByIndex(sqlstr)
		if len(locOrderBy) <= 0 { //如果没有 order by
			return "", errors.New("分页语句必须有 order by")
		}
	*/
	var sqlbuilder strings.Builder
	sqlbuilder.WriteString(sqlstr)
	if dbType == "mysql" || dbType == "sqlite" || dbType == "dm" || dbType == "gbase" || dbType == "clickhouse" || dbType == "tdengine" { //MySQL,sqlite3,dm数据库,南大通用,clickhouse,TDengine
		sqlbuilder.WriteString(" LIMIT ")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize * (page.PageNo - 1)))
		sqlbuilder.WriteString(",")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize))

	} else if dbType == "postgresql" || dbType == "kingbase" || dbType == "shentong" { //postgresql,kingbase,神通数据库
		sqlbuilder.WriteString(" LIMIT ")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize))
		sqlbuilder.WriteString(" OFFSET ")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize * (page.PageNo - 1)))
	} else if dbType == "mssql" || dbType == "oracle" { //sqlserver 2012+,oracle 12c+
		sqlbuilder.WriteString(" OFFSET ")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize * (page.PageNo - 1)))
		sqlbuilder.WriteString(" ROWS FETCH NEXT ")
		sqlbuilder.WriteString(strconv.Itoa(page.PageSize))
		sqlbuilder.WriteString(" ROWS ONLY ")
	} else {
		return "", errors.New("wrapPageSQL()-->不支持的数据库类型:" + dbType)
	}
	sqlstr = sqlbuilder.String()
	return reBindSQL(dbType, sqlstr)
}

//wrapInsertSQL  包装保存Struct语句.返回语句,是否自增,错误信息
//数组传递,如果外部方法有调用append的逻辑，append会破坏指针引用，所以传递指针
//wrapInsertSQL Pack and save 'Struct' statement. Return  SQL statement, whether it is incremented, error message
//Array transfer, if the external method has logic to call append, append will destroy the pointer reference, so the pointer is passed
func wrapInsertSQL(dbType string, typeOf *reflect.Type, entity IEntityStruct, columns *[]reflect.StructField, values *[]interface{}) (string, int, string, error) {
	sqlstr, autoIncrement, pktype, err := wrapInsertSQLNOreBuild(dbType, typeOf, entity, columns, values)
	if err != nil {
		return sqlstr, autoIncrement, pktype, err
	}
	savesql, err := reBindSQL(dbType, sqlstr)
	return savesql, autoIncrement, pktype, err
}

//wrapInsertSQLNOreBuild 包装保存Struct语句.返回语句,没有rebuild,返回原始的SQL,是否自增,主键类型,错误信息
//数组传递,如果外部方法有调用append的逻辑,传递指针,因为append会破坏指针引用
//Pack and save Struct statement. Return  SQL statement, no rebuild, return original SQL, whether it is self-increment, error message
//Array transfer, if the external method has logic to call append, append will destroy the pointer reference, so the pointer is passed
func wrapInsertSQLNOreBuild(dbType string, typeOf *reflect.Type, entity IEntityStruct, columns *[]reflect.StructField, values *[]interface{}) (string, int, string, error) {
	insersql, valuesql, autoIncrement, pktype, err := wrapInsertValueSQLNOreBuild(dbType, typeOf, entity, columns, values)
	if err != nil {
		return "", autoIncrement, pktype, err
	}
	sqlstr := "INSERT INTO " + insersql + " VALUES" + valuesql
	return sqlstr, autoIncrement, pktype, err
}

//wrapInsertValueSQLNOreBuild 包装保存Struct语句.返回语句,没有rebuild,返回原始的InsertSQL,ValueSQL,是否自增,主键类型,错误信息
//数组传递,如果外部方法有调用append的逻辑,传递指针,因为append会破坏指针引用
//Pack and save Struct statement. Return  SQL statement, no rebuild, return original SQL, whether it is self-increment, error message
//Array transfer, if the external method has logic to call append, append will destroy the pointer reference, so the pointer is passed
func wrapInsertValueSQLNOreBuild(dbType string, typeOf *reflect.Type, entity IEntityStruct, columns *[]reflect.StructField, values *[]interface{}) (string, string, int, string, error) {

	//自增类型  0(不自增),1(普通自增),2(序列自增),3(触发器自增)
	//Self-increment type： 0（Not increase）,1(Ordinary increment),2(Sequence increment),3(Trigger increment)
	autoIncrement := 0
	//主键类型
	//Primary key type
	pktype := ""
	//SQL语句的构造器
	//SQL statement constructor
	var sqlBuilder strings.Builder
	//sqlBuilder.WriteString("INSERT INTO ")
	sqlBuilder.WriteString(entity.GetTableName())
	sqlBuilder.WriteString("(")

	//SQL语句中,VALUES(?,?,...)语句的构造器
	//In the SQL statement, the constructor of the VALUES(?,?,...) statement
	var valueSQLBuilder strings.Builder

	valueSQLBuilder.WriteString(" (")
	//主键的名称
	//The name of the primary key.
	pkFieldName, e := entityPKFieldName(entity, typeOf)
	if e != nil {
		return "", "", autoIncrement, pktype, e
	}

	var sequence string
	var sequenceOK bool
	if entity.GetPkSequence() != nil {
		sequence, sequenceOK = entity.GetPkSequence()[dbType]
		if sequenceOK { //存在序列 Existence sequence
			if sequence == "" { //触发器自增,也兼容自增关键字 Auto-increment by trigger, also compatible with auto-increment keywords.
				autoIncrement = 3
			} else { //序列自增 Sequence increment
				autoIncrement = 2
			}

		}

	}

	for i := 0; i < len(*columns); i++ {
		field := (*columns)[i]

		if field.Name == pkFieldName { //如果是主键 | If it is the primary key
			//获取主键类型 | Get the primary key type.
			pkKind := field.Type.Kind()
			if pkKind == reflect.String {
				pktype = "string"
			} else if pkKind == reflect.Int || pkKind == reflect.Int32 || pkKind == reflect.Int16 || pkKind == reflect.Int8 {
				pktype = "int"
			} else if pkKind == reflect.Int64 {
				pktype = "int64"
			} else {
				return "", "", autoIncrement, pktype, errors.New("wrapInsertSQLNOreBuild不支持的主键类型")
			}

			if autoIncrement == 3 {
				//如果是后台触发器生成的主键值,sql语句中不再体现
				//去掉这一列,后续不再处理
				// If it is the primary key value generated by the background trigger, it is no longer reflected in the sql statement
				//Remove this column and will not process it later.
				*columns = append((*columns)[:i], (*columns)[i+1:]...)
				*values = append((*values)[:i], (*values)[i+1:]...)
				i = i - 1
				continue
			}

			//主键的值
			//The value of the primary key
			pkValue := (*values)[i]
			if autoIncrement == 2 { //如果是序列自增 | If it is a sequence increment
				//拼接字符串 | Concatenated string
				//sqlBuilder.WriteString(getStructFieldTagColumnValue(typeOf, field.Name))
				//sqlBuilder.WriteString(field.Tag.Get(tagColumnName))
				colName := getFieldTagName(dbType, &field)
				sqlBuilder.WriteString(colName)
				sqlBuilder.WriteString(",")
				valueSQLBuilder.WriteString(sequence)
				valueSQLBuilder.WriteString(",")
				//去掉这一列,后续不再处理
				//Remove this column and will not process it later.
				*columns = append((*columns)[:i], (*columns)[i+1:]...)
				*values = append((*values)[:i], (*values)[i+1:]...)
				i = i - 1
				continue

			} else if (pktype == "string") && reflect.ValueOf(pkValue).IsZero() { //主键是字符串类型,并且值为"",赋值id
				//生成主键字符串
				//Generate primary key string
				id := FuncGenerateStringID()
				(*values)[i] = id
				//给对象主键赋值
				//Assign a value to the primary key of the object
				v := reflect.ValueOf(entity).Elem()
				v.FieldByName(field.Name).Set(reflect.ValueOf(id))
				//如果是数字类型,并且值为0,认为是数据库自增,从数组中删除掉主键的信息,让数据库自己生成
				//If it is a number type and the value is 0,
				//it is considered to be a database self-increment,
				//delete the primary key information from the array, and let the database generate itself.
			} else if (pktype == "int" || pktype == "int64") && reflect.ValueOf(pkValue).IsZero() {
				//标记是自增主键
				//Mark is auto-incrementing primary key
				autoIncrement = 1
				//去掉这一列,后续不再处理
				//Remove this column and will not process it later.
				*columns = append((*columns)[:i], (*columns)[i+1:]...)
				*values = append((*values)[:i], (*values)[i+1:]...)
				i = i - 1
				continue
			}
		}
		//拼接字符串
		//Concatenated string.
		//sqlBuilder.WriteString(getStructFieldTagColumnValue(typeOf, field.Name))
		// sqlBuilder.WriteString(field.Tag.Get(tagColumnName))
		colName := getFieldTagName(dbType, &field)
		sqlBuilder.WriteString(colName)
		sqlBuilder.WriteString(",")
		//if dbType == "tdengine" && field.Type.Kind() == reflect.String { //tdengine数据库,而且是字符串类型的数据,拼接 '?' ,这实际是驱动的问题,交给zorm解决了
		//	valueSQLBuilder.WriteString("'?',")
		//} else {
		valueSQLBuilder.WriteString("?,")
		//}

	}
	//去掉字符串最后的 ','
	//Remove the',' at the end of the string
	insertsql := sqlBuilder.String()
	if len(insertsql) > 0 {
		insertsql = insertsql[:len(insertsql)-1]
	}
	valuesql := valueSQLBuilder.String()
	if len(valuesql) > 0 {
		valuesql = valuesql[:len(valuesql)-1]
	}
	insertsql = insertsql + ")"
	valuesql = valuesql + ")"
	//savesql, err := wrapSQL(dbType, sqlstr)
	return insertsql, valuesql, autoIncrement, pktype, nil

}

//wrapInsertSliceSQL 包装批量保存StructSlice语句.返回语句,是否自增,错误信息
//数组传递,如果外部方法有调用append的逻辑，append会破坏指针引用，所以传递指针
//wrapInsertSliceSQL Package and save Struct Slice statements in batches. Return SQL statement, whether it is incremented, error message
//Array transfer, if the external method has logic to call append, append will destroy the pointer reference, so the pointer is passed
func wrapInsertSliceSQL(dbType string, typeOf *reflect.Type, entityStructSlice []IEntityStruct, columns *[]reflect.StructField, values *[]interface{}) (string, int, error) {
	sliceLen := len(entityStructSlice)
	if entityStructSlice == nil || sliceLen < 1 {
		return "", 0, errors.New("wrapInsertSliceSQL对象数组不能为空")
	}

	//第一个对象,获取第一个Struct对象,用于获取数据库字段,也获取了值
	//The first object, get the first Struct object, used to get the database field, and also get the value
	entity := entityStructSlice[0]

	//先生成一条语句
	//Generate a statement first
	insertsql, valuesql, autoIncrement, _, firstErr := wrapInsertValueSQLNOreBuild(dbType, typeOf, entity, columns, values)
	if firstErr != nil {
		return "", autoIncrement, firstErr
	}
	sqlstr := "INSERT INTO "
	if dbType == "tdengine" { // 如果是tdengine,拼接类似 INSERT INTO table1 values('2','3')  table2 values('4','5'),目前要求字段和类型必须一致,如果不一致,改动略多
		sqlstr = sqlstr + entity.GetTableName() + " VALUES" + valuesql
	} else {
		sqlstr = sqlstr + insertsql + " VALUES" + valuesql
	}
	//如果只有一个Struct对象
	//If there is only one Struct object
	if sliceLen == 1 {
		sqlstr, _ = reBindSQL(dbType, sqlstr)
		return sqlstr, autoIncrement, firstErr
	}
	//主键的名称
	//The name of the primary key
	pkFieldName, e := entityPKFieldName(entity, typeOf)
	if e != nil {
		return "", autoIncrement, e
	}

	//截取生成的SQL语句中 VALUES 后面的字符串值
	//Intercept the string value after VALUES in the generated SQL statement
	/*
		valueIndex := strings.Index(sqlstr, " VALUES (")
		if valueIndex < 1 { //生成的语句异常
			return "", autoIncrement, errors.New("wrapInsertSliceSQL生成的语句异常")
		}
		//value后面的字符串 例如 (?,?,?),用于循环拼接
		//The string after the value, such as (?,?,?), is used for circular splicing
		valuestr := sqlstr[valueIndex+8:]
	*/
	//SQL语句的构造器
	//SQL statement constructor
	var insertSliceSQLBuilder strings.Builder
	insertSliceSQLBuilder.WriteString(sqlstr)
	for i := 1; i < sliceLen; i++ {
		//拼接字符串
		//Splicing string
		if dbType == "tdengine" { // 如果是tdengine,拼接类似 INSERT INTO table1 values('2','3')  table2 values('4','5'),目前要求字段和类型必须一致,如果不一致,改动略多
			insertSliceSQLBuilder.WriteString(" ")
			insertSliceSQLBuilder.WriteString(entityStructSlice[i].GetTableName())
			insertSliceSQLBuilder.WriteString(" VALUES")
			insertSliceSQLBuilder.WriteString(valuesql)
		} else { // 标准语法 类似 INSERT INTO table1(id,name) values('2','3'), values('4','5')
			insertSliceSQLBuilder.WriteString(",")
			insertSliceSQLBuilder.WriteString(valuesql)
		}

		entityStruct := entityStructSlice[i]
		for j := 0; j < len(*columns); j++ {
			// 获取实体类的反射,指针下的struct
			// Get the reflection of the entity class, the struct under the pointer
			valueOf := reflect.ValueOf(entityStruct).Elem()
			field := (*columns)[j]
			if field.Name == pkFieldName { //如果是主键 ｜ If it is the primary key
				pkKind := field.Type.Kind()
				//主键的值
				//The value of the primary key
				pkValue := valueOf.FieldByName(field.Name).Interface()
				//只处理字符串类型的主键,其他类型,columns中并不包含
				//Only handle primary keys of string type, other types, not included in columns
				if (pkKind == reflect.String) && (pkValue.(string) == "") {
					//主键是字符串类型,并且值为"",赋值'id'
					//生成主键字符串
					//The primary key is a string type, and the value is "", assigned the value'id'
					//Generate primary key string
					id := FuncGenerateStringID()
					*values = append(*values, id)
					//给对象主键赋值
					//Assign a value to the primary key of the object
					valueOf.FieldByName(field.Name).Set(reflect.ValueOf(id))
					continue
				}
			}

			//给字段赋值
			//Assign a value to the field.
			*values = append(*values, valueOf.FieldByName(field.Name).Interface())

		}
	}

	//包装sql
	//Wrap sql
	savesql, err := reBindSQL(dbType, insertSliceSQLBuilder.String())
	return savesql, autoIncrement, err

}

//wrapUpdateSQL 包装更新Struct语句
//数组传递,如果外部方法有调用append的逻辑，append会破坏指针引用，所以传递指针
//wrapUpdateSQL Package update Struct statement
//Array transfer, if the external method has logic to call append, append will destroy the pointer reference, so the pointer is passed
func wrapUpdateSQL(dbType string, typeOf *reflect.Type, entity IEntityStruct, columns *[]reflect.StructField, values *[]interface{}, onlyUpdateNotZero bool) (string, error) {

	//SQL语句的构造器
	//SQL statement constructor
	var sqlBuilder strings.Builder

	sqlBuilder.WriteString("UPDATE ")
	sqlBuilder.WriteString(entity.GetTableName())
	sqlBuilder.WriteString(" SET ")

	//主键的值
	//The value of the primary key
	var pkValue interface{}
	//主键的名称
	//The name of the primary key
	pkFieldName, e := entityPKFieldName(entity, typeOf)
	if e != nil {
		return "", e
	}

	for i := 0; i < len(*columns); i++ {
		field := (*columns)[i]
		if field.Name == pkFieldName {
			//如果是主键
			//If it is the primary key.
			pkValue = (*values)[i]
			//去掉这一列,最后处理主键
			//Remove this column, and finally process the primary key
			*columns = append((*columns)[:i], (*columns)[i+1:]...)
			*values = append((*values)[:i], (*values)[i+1:]...)
			i = i - 1
			continue
		}

		//如果是默认值字段,删除掉,不更新
		//If it is the default value field, delete it and do not update
		if onlyUpdateNotZero && (reflect.ValueOf((*values)[i]).IsZero()) {
			//去掉这一列,不再处理
			//Remove this column and no longer process
			*columns = append((*columns)[:i], (*columns)[i+1:]...)
			*values = append((*values)[:i], (*values)[i+1:]...)
			i = i - 1
			continue

		}
		//sqlBuilder.WriteString(getStructFieldTagColumnValue(typeOf, field.Name))
		// sqlBuilder.WriteString(field.Tag.Get(tagColumnName))
		colName := getFieldTagName(dbType, &field)
		sqlBuilder.WriteString(colName)
		sqlBuilder.WriteString("=?,")

	}
	//主键的值是最后一个
	//The value of the primary key is the last
	*values = append(*values, pkValue)
	//去掉字符串最后的 ','
	//Remove the',' at the end of the string
	sqlstr := sqlBuilder.String()
	sqlstr = sqlstr[:len(sqlstr)-1]

	sqlstr = sqlstr + " WHERE " + entity.GetPKColumnName() + "=?"

	return reBindSQL(dbType, sqlstr)
}

//wrapDeleteSQL 包装删除Struct语句
//wrapDeleteSQL Package delete Struct statement
func wrapDeleteSQL(dbType string, entity IEntityStruct) (string, error) {

	//SQL语句的构造器
	//SQL statement constructor
	var sqlBuilder strings.Builder

	sqlBuilder.WriteString("DELETE FROM ")
	sqlBuilder.WriteString(entity.GetTableName())
	sqlBuilder.WriteString(" WHERE ")
	sqlBuilder.WriteString(entity.GetPKColumnName())
	sqlBuilder.WriteString("=?")
	sqlstr := sqlBuilder.String()

	return reBindSQL(dbType, sqlstr)

}

//wrapInsertEntityMapSQL 包装保存Map语句,Map因为没有字段属性,无法完成Id的类型判断和赋值,需要确保Map的值是完整的
//wrapInsertEntityMapSQL Pack and save the Map statement. Because Map does not have field attributes,
//it cannot complete the type judgment and assignment of Id. It is necessary to ensure that the value of Map is complete
func wrapInsertEntityMapSQL(dbType string, entity IEntityMap) (string, []interface{}, bool, error) {
	insertsql, valuesql, values, autoIncrement, err := wrapInsertValueEntityMapSQL(dbType, entity)
	if err != nil {
		return "", nil, autoIncrement, err
	}
	//拼接SQL语句,带上列名,因为Map取值是无序的
	sqlstr := "INSERT INTO " + insertsql + " VALUES" + valuesql
	var e error
	sqlstr, e = reBindSQL(dbType, sqlstr)
	if e != nil {
		return "", nil, autoIncrement, e
	}
	return sqlstr, values, autoIncrement, nil
}

//wrapInsertValueEntityMapSQL 包装保存Map语句,Map因为没有字段属性,无法完成Id的类型判断和赋值,需要确保Map的值是完整的
//wrapInsertValueEntityMapSQL Pack and save the Map statement. Because Map does not have field attributes,
//it cannot complete the type judgment and assignment of Id. It is necessary to ensure that the value of Map is complete
func wrapInsertValueEntityMapSQL(dbType string, entity IEntityMap) (string, string, []interface{}, bool, error) {
	//是否自增,默认false
	autoIncrement := false
	dbFieldMap := entity.GetDBFieldMap()
	if len(dbFieldMap) < 1 {
		return "", "", nil, autoIncrement, errors.New("wrapInsertEntityMapSQL-->GetDBFieldMap返回值不能为空")
	}
	//SQL对应的参数
	//SQL corresponding parameters
	values := []interface{}{}

	//SQL语句的构造器
	//SQL statement constructor
	var sqlBuilder strings.Builder
	//sqlBuilder.WriteString("INSERT INTO ")
	sqlBuilder.WriteString(entity.GetTableName())
	sqlBuilder.WriteString("(")

	//SQL语句中,VALUES(?,?,...)语句的构造器
	//In the SQL statement, the constructor of the VALUES(?,?,...) statement.
	var valueSQLBuilder strings.Builder
	valueSQLBuilder.WriteString(" (")
	//是否Set了主键
	//Whether the primary key is set.
	_, hasPK := dbFieldMap[entity.GetPKColumnName()]
	if entity.GetPKColumnName() != "" && !hasPK { //如果有主键字段,却没值,认为是自增或者序列 | If the primary key is not set, it is considered to be auto-increment or sequence
		autoIncrement = true
		if sequence, ok := entity.GetPkSequence()[dbType]; ok { //如果是序列 | If it is a sequence.
			sqlBuilder.WriteString(entity.GetPKColumnName())
			sqlBuilder.WriteString(",")
			valueSQLBuilder.WriteString(sequence)
			valueSQLBuilder.WriteString(",")
		}
	}

	for k, v := range dbFieldMap {
		//拼接字符串
		//Concatenated string
		sqlBuilder.WriteString(k)
		sqlBuilder.WriteString(",")
		//if dbType == "tdengine" && reflect.TypeOf(v).Kind() == reflect.String { //tdengine数据库,而且是字符串类型的数据,拼接 '?' ,这实际是驱动的问题,交给zorm解决了
		//	valueSQLBuilder.WriteString("'?',")
		//} else {
		valueSQLBuilder.WriteString("?,")
		//}

		values = append(values, v)
	}
	//去掉字符串最后的 ','
	//Remove the',' at the end of the string
	insertsql := sqlBuilder.String()
	if len(insertsql) > 0 {
		insertsql = insertsql[:len(insertsql)-1]
	}
	valuesql := valueSQLBuilder.String()
	if len(valuesql) > 0 {
		valuesql = valuesql[:len(valuesql)-1]
	}
	insertsql = insertsql + ")"
	valuesql = valuesql + ")"

	return insertsql, valuesql, values, autoIncrement, nil
}

//wrapUpdateEntityMapSQL 包装Map更新语句,Map因为没有字段属性,无法完成Id的类型判断和赋值,需要确保Map的值是完整的
//wrapUpdateEntityMapSQL Wrap the Map update statement. Because Map does not have field attributes,
//it cannot complete the type judgment and assignment of Id. It is necessary to ensure that the value of Map is complete
func wrapUpdateEntityMapSQL(dbType string, entity IEntityMap) (string, []interface{}, error) {
	dbFieldMap := entity.GetDBFieldMap()
	if len(dbFieldMap) < 1 {
		return "", nil, errors.New("wrapUpdateEntityMapSQL-->GetDBFieldMap返回值不能为空")
	}
	//SQL语句的构造器
	//SQL statement constructor
	var sqlBuilder strings.Builder

	sqlBuilder.WriteString("UPDATE ")
	sqlBuilder.WriteString(entity.GetTableName())
	sqlBuilder.WriteString(" SET ")

	//SQL对应的参数
	//SQL corresponding parameters
	values := []interface{}{}
	//主键名称
	//Primary key name
	var pkValue interface{}

	for k, v := range dbFieldMap {

		if k == entity.GetPKColumnName() { //如果是主键  | If it is the primary key
			pkValue = v
			continue
		}

		//拼接字符串 | Splicing string.
		sqlBuilder.WriteString(k)
		sqlBuilder.WriteString("=?,")
		values = append(values, v)
	}
	//主键的值是最后一个
	//The value of the primary key is the last
	values = append(values, pkValue)
	//去掉字符串最后的 ','
	//Remove the',' at the end of the string
	sqlstr := sqlBuilder.String()
	sqlstr = sqlstr[:len(sqlstr)-1]

	sqlstr = sqlstr + " WHERE " + entity.GetPKColumnName() + "=?"

	var e error
	sqlstr, e = reBindSQL(dbType, sqlstr)
	if e != nil {
		return "", nil, e
	}
	return sqlstr, values, nil
}

//wrapQuerySQL 封装查询语句
//wrapQuerySQL Encapsulated query statement
func wrapQuerySQL(dbType string, finder *Finder, page *Page) (string, error) {

	//获取到没有page的sql的语句
	//Get the SQL statement without page.
	sqlstr, err := finder.GetSQL()
	if err != nil {
		return "", err
	}
	if page == nil {
		sqlstr, err = reBindSQL(dbType, sqlstr)
	} else {
		sqlstr, err = wrapPageSQL(dbType, sqlstr, page)
	}

	if err != nil {
		return "", err
	}
	return sqlstr, err
}

//reBindSQL 包装基础的SQL语句,根据数据库类型,调整SQL变量符号,例如?,? $1,$2这样的
//reBindSQL Pack basic SQL statements, adjust the SQL variable symbols according to the database type, such as?,? $1,$2
func reBindSQL(dbType string, sqlstr string) (string, error) {
	if dbType == "mysql" || dbType == "sqlite" || dbType == "dm" || dbType == "gbase" || dbType == "clickhouse" || dbType == "tdengine" {
		return sqlstr, nil
	}

	strs := strings.Split(sqlstr, "?")
	if len(strs) < 1 {
		return sqlstr, nil
	}
	var sqlBuilder strings.Builder
	sqlBuilder.WriteString(strs[0])
	for i := 1; i < len(strs); i++ {
		if dbType == "postgresql" || dbType == "kingbase" { //postgresql,kingbase
			sqlBuilder.WriteString("$")
			sqlBuilder.WriteString(strconv.Itoa(i))
		} else if dbType == "mssql" { //mssql
			sqlBuilder.WriteString("@p")
			sqlBuilder.WriteString(strconv.Itoa(i))
		} else if dbType == "oracle" || dbType == "shentong" { //oracle,神州通用
			sqlBuilder.WriteString(":")
			sqlBuilder.WriteString(strconv.Itoa(i))
		} else { //其他情况,还是使用 ? | In other cases, or use  ?
			sqlBuilder.WriteString("?")
		}
		sqlBuilder.WriteString(strs[i])
	}
	return sqlBuilder.String(), nil
}

//reUpdateFinderSQL 根据数据类型更新 手动编写的 UpdateFinder的语句,用于处理数据库兼容,例如 clickhouse的 UPDATE 和 DELETE
func reUpdateFinderSQL(dbType string, sqlstr *string) (*string, error) {

	//处理clickhouse的特殊更新语法
	if dbType == "clickhouse" {
		//SQL语句的构造器
		//SQL statement constructor
		var sqlBuilder strings.Builder
		sqlBuilder.WriteString("ALTER TABLE ")
		sqls := findUpdateTableName(sqlstr)
		if len(sqls) >= 2 { //如果是更新语句
			sqlBuilder.WriteString(sqls[1])
			sqlBuilder.WriteString(" UPDATE ")
		} else { //如果不是更新语句
			sqls = findDeleteTableName(sqlstr)
			if len(sqls) < 2 { //如果也不是删除语句
				return sqlstr, nil
			}
			sqlBuilder.WriteString(sqls[1])
			sqlBuilder.WriteString(" DELETE WHERE ")
		}

		//截取字符串
		content := (*sqlstr)[len(sqls[0]):]
		sqlBuilder.WriteString(content)
		sql := sqlBuilder.String()
		return &sql, nil
	}
	return sqlstr, nil

}

//查询'order by'在sql中出现的开始位置和结束位置
//Query the start position and end position of'order by' in SQL
var orderByExpr = "(?i)\\s(order)\\s+by\\s"
var orderByRegexp, _ = regexp.Compile(orderByExpr)

//findOrderByIndex 查询order by在sql中出现的开始位置和结束位置
// findOrderByIndex Query the start position and end position of'order by' in SQL
func findOrderByIndex(strsql string) []int {
	loc := orderByRegexp.FindStringIndex(strsql)
	return loc
}

//查询'group by'在sql中出现的开始位置和结束位置
//Query the start position and end position of'group by' in sql。
var groupByExpr = "(?i)\\s(group)\\s+by\\s"
var groupByRegexp, _ = regexp.Compile(groupByExpr)

//findGroupByIndex 查询group by在sql中出现的开始位置和结束位置
//findGroupByIndex Query the start position and end position of'group by' in sql
func findGroupByIndex(strsql string) []int {
	loc := groupByRegexp.FindStringIndex(strsql)
	return loc
}

//查询 from 在sql中出现的开始位置和结束位置
//Query the start position and end position of 'from' in sql
//var fromExpr = "(?i)(^\\s*select)(.+?\\(.+?\\))*.*?(from)"
//感谢奔跑(@zeqjone)提供的正则,排除不在括号内的from,已经满足绝大部分场景,
//select id1,(select (id2) from t1 where id=2) _s FROM table select的子查询 _s中的 id2还有括号,才会出现问题,建议使用CountFinder处理分页语句
var fromExpr = "(?i)(^\\s*select)(\\(.*?\\)|[^()]+)*?(from)"
var fromRegexp, _ = regexp.Compile(fromExpr)

//findFromIndexa 查询from在sql中出现的开始位置和结束位置
//findSelectFromIndex Query the start position and end position of 'from' in sql
func findSelectFromIndex(strsql string) []int {
	//匹配出来的是完整的字符串,用最后的FROM即可
	loc := fromRegexp.FindStringIndex(strsql)
	if len(loc) < 2 {
		return loc
	}
	//最后的FROM前推4位字符串
	loc[0] = loc[1] - 4
	return loc
}

/*
var fromExpr = `\(([\s\S]+?)\)`
var fromRegexp, _ = regexp.Compile(fromExpr)

//查询 from 在sql中出现的开始位置
//Query the start position of 'from' in sql
func findSelectFromIndex(strsql string) int {
	sql := strings.ToLower(strsql)
	m := fromRegexp.FindAllString(sql, -1)
	for i := 0; i < len(m); i++ {
		str := m[i]
		strnofrom := strings.ReplaceAll(str, " from ", " zorm ")
		sql = strings.ReplaceAll(sql, str, strnofrom)
	}
	fromIndex := strings.LastIndex(sql, " from ")
	if fromIndex < 0 {
		return fromIndex
	}
	//补上一个空格
	fromIndex = fromIndex + 1
	return fromIndex
}
*/
// 从更新语句中获取表名
//update\\s(.+)set\\s.*
var updateExper = "(?i)^\\s*update\\s+(\\w+)\\s+set\\s"
var updateRegexp, _ = regexp.Compile(updateExper)

// findUpdateTableName 获取语句中表名
// 第一个是符合的整体数据,第二个是表名
func findUpdateTableName(strsql *string) []string {
	matchs := updateRegexp.FindStringSubmatch(*strsql)
	return matchs
}

// 从删除语句中获取表名
//delete\\sfrom\\s(.+)where\\s(.*)
var deleteExper = "(?i)^\\s*delete\\s+from\\s+(\\w+)\\s+where\\s"
var deleteRegexp, _ = regexp.Compile(deleteExper)

// findDeleteTableName 获取语句中表名
// 第一个是符合的整体数据,第二个是表名
func findDeleteTableName(strsql *string) []string {
	matchs := deleteRegexp.FindStringSubmatch(*strsql)
	return matchs

}

//converValueColumnType 根据数据库的字段类型,转化成golang的类型,不处理sql.Nullxxx类型
//converValueColumnType According to the field type of the database, it is converted to the type of golang, and the sql.Nullxxx type is not processed
func converValueColumnType(v interface{}, columnType *sql.ColumnType) interface{} {

	if v == nil {
		return nil
	}

	//如果是字节数组
	//If it is a byte array
	value, ok := v.([]byte)
	if !ok { //转化失败,不是字节数组,例如:string,直接返回值
		return v
	}
	if len(value) < 1 { //值为空,为nil
		return value
	}

	//获取数据库类型,自己对应golang的基础类型值,不处理sql.Nullxxx类型
	//Get the database type, corresponding to the basic type value of golang, and do not process the sql.Nullxxx type.
	databaseTypeName := strings.ToUpper(columnType.DatabaseTypeName())
	switch databaseTypeName {
	case "CHAR", "NCHAR", "VARCHAR", "NVARCHAR", "VARCHAR2", "NVARCHAR2", "TINYTEXT", "MEDIUMTEXT", "TEXT", "NTEXT", "LONGTEXT", "LONG":
		return typeConvertString(v)
	case "INT", "INT4", "INTEGER", "SERIAL", "TINYINT", "BIT", "SMALLINT", "SMALLSERIAL", "INT2":
		return typeConvertInt(v)
	case "BIGINT", "BIGSERIAL", "INT8":
		return typeConvertInt64(v)
	case "FLOAT", "REAL":
		return typeConvertFloat32(v)
	case "DOUBLE":
		return typeConvertFloat64(v)
	case "DECIMAL", "NUMBER", "NUMERIC", "DEC":
		return typeConvertDecimal(v)
	case "DATE":
		return typeConvertTime(v, "2006-01-02", time.Local)
	case "TIME":
		return typeConvertTime(v, "15:04:05", time.Local)
	case "DATETIME":
		return typeConvertTime(v, "2006-01-02 15:04:05", time.Local)
	case "TIMESTAMP":
		return typeConvertTime(v, "2006-01-02 15:04:05.000", time.Local)
	case "BOOLEAN", "BOOL":
		return typeConvertBool(v)
	}
	//其他类型以后再写.....
	//Other types will be written later...
	return v
}

//FuncGenerateStringID 默认生成字符串ID的函数.方便自定义扩展
//FuncGenerateStringID Function to generate string ID by default. Convenient for custom extension
var FuncGenerateStringID func() string = generateStringID

//generateStringID 生成主键字符串
//generateStringID Generate primary key string
func generateStringID() string {

	// 使用 crypto/rand 真随机9位数
	randNum, randErr := rand.Int(rand.Reader, big.NewInt(1000000000))
	if randErr != nil {
		return ""
	}
	//获取9位数,前置补0,确保9位数
	rand9 := fmt.Sprintf("%09d", randNum)

	//获取纳秒 按照 年月日时分秒毫秒微秒纳秒 拼接为长度23位的字符串
	pk := time.Now().Format("2006.01.02.15.04.05.000000000")
	pk = strings.ReplaceAll(pk, ".", "")

	//23位字符串+9位随机数=32位字符串,这样的好处就是可以使用ID进行排序
	pk = pk + rand9
	return pk
}

//generateStringID 生成主键字符串
//generateStringID Generate primary key string
/*
func generateStringID() string {
	//pk := strconv.FormatInt(time.Now().UnixNano(), 10)
	pk, errUUID := gouuid.NewV4()
	if errUUID != nil {
		return ""
	}
	return pk.String()
}
*/

// getFieldTagName 获取模型中定义的数据库的 column tag
func getFieldTagName(dbType string, field *reflect.StructField) string {
	colName := field.Tag.Get(tagColumnName)
	if dbType == "kingbase" {
		// kingbase R3 驱动大小写敏感，通常是大写。数据库全的列名部换成双引号括住的大写字符，避免与数据库内置关键词冲突时报错
		colName = strings.ReplaceAll(colName, "\"", "")
		colName = fmt.Sprintf(`"%s"`, strings.ToUpper(colName))
	}
	return colName
}

// wrapSQLHint 在sql语句中增加hint
func wrapSQLHint(ctx context.Context, sqlstr *string) (*string, error) {
	//获取hint
	contextValue := ctx.Value(contextSQLHintValueKey)
	if contextValue == nil { //如果没有设置hint
		return sqlstr, nil
	}
	hint := contextValue.(string)
	if len(hint) < 1 {
		return sqlstr, nil
	}
	//sql去空格
	sqlTrim := strings.TrimSpace(*sqlstr)
	sqlIndex := strings.Index(sqlTrim, " ")
	if sqlIndex < 0 {
		return sqlstr, nil
	}
	sql := sqlTrim[:sqlIndex] + " " + hint + sqlTrim[sqlIndex:]
	sqlstr = &sql
	return sqlstr, nil
}

//reTDengineSQL 重新包装TDengine的sql语句,把 string类型的值对应的 ? 修改为 '?'
func reTDengineSQL(dbType string, sqlstr *string, args []interface{}) (*string, error) {
	if dbType != "tdengine" {
		return sqlstr, nil
	}

	strs := strings.Split(*sqlstr, "?")
	if len(strs) < 1 {
		return sqlstr, nil
	}
	if len(strs)-1-len(args) != 0 { //分隔之后,字符串比值多1个
		return sqlstr, errors.New("reTDengineSQL()-->参数数量和值不一致")
	}
	var sqlBuilder strings.Builder
	sqlBuilder.WriteString(strs[0])
	for i := 0; i < len(args); i++ {

		//不应该允许再手动拼接 '?' 单引号了,强制统一使用zorm实现,保证书写统一
		/*
			pre := strings.TrimSpace(strs[i])
			after := strings.TrimSpace(strs[i+1])
			if strings.HasSuffix(pre, "'") && strings.HasPrefix(after, "'") { //用户手动拼接了 '?'
				sqlBuilder.WriteString("?")
				sqlBuilder.WriteString(strs[i+1])
				continue
			}
		*/

		typeOf := reflect.TypeOf(args[i])
		if typeOf.Kind() == reflect.Ptr {
			//获取指针下的类型
			typeOf = typeOf.Elem()
		}
		if typeOf.Kind() == reflect.String { //如果值是字符串
			sqlBuilder.WriteString("'?'")
		} else { //其他情况,还是使用 ?
			sqlBuilder.WriteString("?")
		}
		sqlBuilder.WriteString(strs[i+1])
	}
	*sqlstr = sqlBuilder.String()
	return sqlstr, nil
}
