package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// readRows loads a report's rows from a file instead of a database: a JSON
// array of objects (or {"rows": [...]}), JSON Lines, or CSV with a header
// line. Keys are column names ("Amount" or "Sales.Amount"); a string becomes a date,
// number or boolean where the report's field of that name has that type.
func readRows(path string, r *Report) ([]Row, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, rec := range recs[min(1, len(recs)):] {
			m := map[string]any{}
			for i, h := range recs[0] {
				if i < len(rec) {
					m[h] = rec[i]
				}
			}
			raw = append(raw, m)
		}
	case ".jsonl", ".ndjson":
		dec := json.NewDecoder(bytes.NewReader(b))
		for dec.More() {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			raw = append(raw, m)
		}
	default:
		if err := json.Unmarshal(b, &raw); err != nil {
			var wrapped struct{ Rows []map[string]any }
			if err2 := json.Unmarshal(b, &wrapped); err2 != nil || wrapped.Rows == nil {
				return nil, fmt.Errorf("%s: want a JSON array of objects or {\"rows\": [...]}: %w", path, err)
			}
			raw = wrapped.Rows
		}
	}
	types := map[string]string{}
	for _, f := range r.Fields {
		if f.Type == "DatabaseField" {
			types[strings.ToLower(f.Name)] = f.DataType
		}
	}
	rows := make([]Row, len(raw))
	for i, m := range raw {
		row := Row{}
		for k, v := range m {
			col := strings.ToLower(k)
			if j := strings.LastIndexByte(col, '.'); j >= 0 {
				col = col[j+1:]
			}
			row[col] = typedValue(v, types[col])
		}
		rows[i] = row
	}
	return rows, nil
}

// typedValue converts a value read from a file to the field's type.
func typedValue(v any, typ string) any {
	s, ok := v.(string)
	if !ok {
		return v // float64, bool or nil from JSON
	}
	t := strings.ToLower(typ)
	switch {
	case strings.Contains(t, "date") || strings.Contains(t, "time"):
		if s == "" {
			return nil
		}
		for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339, "1/2/2006", "1/2/2006 15:04:05"} {
			if d, err := time.Parse(layout, s); err == nil {
				return normalize(d)
			}
		}
	case t == "currency" || t == "number" || strings.HasPrefix(t, "float") || strings.HasPrefix(t, "int") || t == "decimal":
		if s == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil {
			return f
		}
	case t == "boolean":
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}
	return s
}
