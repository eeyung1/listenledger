package storage

import (
	"strconv"
	"strings"
)

// Dialect identifies the SQL dialect spoken by the underlying database engine.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite3"
	DialectPostgres Dialect = "postgres"
)

// IsPostgres reports whether the dialect is PostgreSQL.
func (d Dialect) IsPostgres() bool {
	return d == DialectPostgres
}

// IsSQLite reports whether the dialect is SQLite.
func (d Dialect) IsSQLite() bool {
	return d == DialectSQLite || d == ""
}

// rebindQuery converts '?' positional placeholders in a SQL query into '$1', '$2', ...
// when targeting PostgreSQL. Single-quoted strings ('...') are left intact.
func rebindQuery(dialect Dialect, query string) string {
	if !dialect.IsPostgres() {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 16)

	paramIdx := 1
	inString := false

	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			inString = !inString
			b.WriteByte(ch)
		} else if ch == '?' && !inString {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(paramIdx))
			paramIdx++
		} else {
			b.WriteByte(ch)
		}
	}

	return b.String()
}
