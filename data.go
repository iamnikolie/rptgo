package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

// Row is one record of the report's dataset, keyed by lower-case column name.
type Row map[string]any

// runProc executes the stored procedure with named parameters and returns
// its first result set.
func runProc(dsn, proc string, params []string) ([]Row, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var args []any
	for _, p := range params {
		name, val, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("param %q: want Name=Value", p)
		}
		args = append(args, sql.Named(strings.TrimPrefix(name, "@"), paramValue(val)))
	}
	rows, err := db.Query(proc, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", proc, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, c := range cols {
			r[strings.ToLower(c)] = normalize(vals[i])
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func paramValue(s string) any {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func normalize(v any) any {
	switch v := v.(type) {
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case float32:
		return float64(v)
	case []byte: // decimal, money
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return string(v)
		}
		return f
	case time.Time:
		return time.Date(v.Year(), v.Month(), v.Day(), v.Hour(), v.Minute(), v.Second(), 0, time.UTC)
	}
	return v
}
