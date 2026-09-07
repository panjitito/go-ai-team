// Package dbx lets agents query saved databases without ever holding a
// credential.
//
// Three rules shape this package, and each exists because the alternative is a
// real accident waiting to happen:
//
//  1. Read-only by default. A connection must be explicitly marked writable
//     AND the caller must confirm before anything that changes data runs.
//  2. One statement per call. Stacked statements are refused outright, because
//     "SELECT 1; DROP TABLE users" is the oldest trick there is and an agent
//     composing SQL from a model's output is exactly the case to worry about.
//  3. Results are capped. An agent that pulls a million rows into its context
//     wastes the user's quota and learns nothing it could not have learned from
//     fifty.
//
// The agent names a connection; the password is resolved here, in this process,
// from the vault. It never reaches a prompt or a transcript.
package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/panjitito/go-ai-team/internal/store"
)

// MaxRows caps a result set.
const MaxRows = 200

// writeVerbs begin a statement that changes data or schema.
var writeVerbs = map[string]bool{
	"insert": true, "update": true, "delete": true, "drop": true,
	"truncate": true, "alter": true, "create": true, "replace": true,
	"grant": true, "revoke": true, "rename": true, "merge": true,
	"call": true, "do": true, "load": true, "import": true,
	"attach": true, "vacuum": true, "reindex": true, "cluster": true,
	"copy": true, "set": true, "commit": true, "rollback": true, "begin": true,
}

// Kind classifies a statement.
type Kind string

const (
	KindRead  Kind = "read"
	KindWrite Kind = "write"
)

// Classify decides whether a statement reads or writes.
//
// The check is on the first keyword rather than a search for dangerous words
// anywhere, because a substring search both misses `WITH x AS (DELETE ...)` and
// falsely flags `SELECT * FROM updates`. Comments are stripped first so a
// leading `/* select */ DELETE` cannot disguise itself.
func Classify(sqlText string) Kind {
	s := stripComments(sqlText)
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "("))
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) == 0 {
		return KindRead
	}
	first := strings.Trim(fields[0], ";")

	// A CTE can hide a write in its body, so look through the whole statement
	// for a data-modifying keyword when it starts with WITH.
	if first == "with" {
		low := strings.ToLower(s)
		for _, v := range []string{"insert", "update", "delete", "merge"} {
			if strings.Contains(low, v+" ") {
				return KindWrite
			}
		}
		return KindRead
	}
	if writeVerbs[first] {
		return KindWrite
	}
	return KindRead
}

// stripComments removes -- line and /* block */ comments.
func stripComments(s string) string {
	var b strings.Builder
	inLine, inBlock, inStr := false, false, false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlock = false
				i++
				b.WriteByte(' ')
			}
		case inStr:
			b.WriteByte(c)
			if c == quote {
				inStr = false
			}
		case c == '\'' || c == '"' || c == '`':
			inStr = true
			quote = c
			b.WriteByte(c)
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			inLine = true
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			inBlock = true
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// SingleStatement reports whether the text holds exactly one statement.
// Semicolons inside string literals and comments do not count.
func SingleStatement(sqlText string) bool {
	s := stripComments(sqlText)
	inStr := false
	var quote byte
	count := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			inStr = true
			quote = c
		case ';':
			// A trailing semicolon is fine; anything after it is a second
			// statement.
			if strings.TrimSpace(s[i+1:]) != "" {
				count++
			}
		}
	}
	return count == 0
}

// Tunnel opens a local forward to a remote database and reports the local
// address to dial. Supplied by the caller so this package does not depend on
// the SSH layer.
type Tunnel func(ctx context.Context, hostID, remoteHost string, remotePort int) (localAddr string, closeFn func(), err error)

// Client executes statements against saved connections.
type Client struct {
	st      *store.Store
	resolve func(secretRef string) (string, error)
	tunnel  Tunnel
}

// New builds a Client. resolve turns a secret reference into a password inside
// this process; it is never exposed to a caller.
func New(st *store.Store, resolve func(string) (string, error), tunnel Tunnel) *Client {
	return &Client{st: st, resolve: resolve, tunnel: tunnel}
}

// Result is a capped result set.
type Result struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Truncated bool     `json:"truncated"`
	Affected  int64    `json:"affected,omitempty"`
	Kind      Kind     `json:"kind"`
	Took      string   `json:"took"`
}

// Query runs one statement against a named connection.
//
// confirmWrite must be true for a write, and the connection must be writable.
// Both gates are required: a writable connection still should not be changed by
// an agent that only meant to read.
func (c *Client) Query(ctx context.Context, connName, sqlText string, confirmWrite bool) (*Result, error) {
	conn, ok := c.st.DBConnByName(connName)
	if !ok {
		return nil, fmt.Errorf("no saved connection named %q", connName)
	}
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return nil, fmt.Errorf("no statement given")
	}
	if !SingleStatement(sqlText) {
		return nil, fmt.Errorf("only one statement per call; stacked statements are refused")
	}

	kind := Classify(sqlText)
	if kind == KindWrite {
		if !conn.Writable {
			return nil, fmt.Errorf("%q is a read-only connection, so that statement is refused", connName)
		}
		if !confirmWrite {
			return nil, fmt.Errorf("that statement would change data on %q; it needs an explicit confirmation", connName)
		}
		if conn.Production {
			return nil, fmt.Errorf("%q is flagged as production; writes must be run from the database panel, "+
				"where the confirmation names the connection", connName)
		}
	}

	if conn.Driver == "mongodb" {
		return nil, fmt.Errorf("MongoDB connections are saved and tunnelled, but statement execution is not implemented yet; " +
			"use mysql or postgres for now")
	}

	host, port := conn.Host, conn.Port
	if conn.TunnelHostID != "" {
		if c.tunnel == nil {
			return nil, fmt.Errorf("%q needs an SSH tunnel but tunnelling is not available", connName)
		}
		local, closeFn, err := c.tunnel(ctx, conn.TunnelHostID, conn.Host, conn.Port)
		if err != nil {
			return nil, fmt.Errorf("could not open the tunnel for %q: %w", connName, err)
		}
		defer closeFn()
		h, p, err := net.SplitHostPort(local)
		if err != nil {
			return nil, err
		}
		host = h
		port, _ = strconv.Atoi(p)
	}

	password := ""
	if conn.SecretRef != "" {
		if c.resolve == nil {
			return nil, fmt.Errorf("%q references a secret but the vault is unavailable", connName)
		}
		pw, err := c.resolve(conn.SecretRef)
		if err != nil {
			return nil, fmt.Errorf("could not read the secret %q for this connection: %w", conn.SecretRef, err)
		}
		password = pw
	}

	dsn, driver, err := buildDSN(conn, host, port, password)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("could not open the connection: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(2 * time.Minute)

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("could not reach the database: %w", err)
	}

	start := time.Now()
	res := &Result{Kind: kind}

	if kind == KindWrite {
		out, err := db.ExecContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		if n, err := out.RowsAffected(); err == nil {
			res.Affected = n
		}
		res.Took = time.Since(start).Round(time.Millisecond).String()
		return res, nil
	}

	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res.Columns = cols

	for rows.Next() {
		if res.RowCount >= MaxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		// Drivers hand back []byte for text columns; turning those into strings
		// keeps the JSON readable instead of base64.
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		res.Rows = append(res.Rows, vals)
		res.RowCount++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Took = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

// buildDSN assembles a driver-specific connection string.
func buildDSN(c *store.DBConn, host string, port int, password string) (dsn, driver string, err error) {
	switch c.Driver {
	case "mysql":
		if port == 0 {
			port = 3306
		}
		tls := "false"
		if c.TLS {
			tls = "true"
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&timeout=10s&tls=%s",
			c.User, password, host, port, c.DBName, tls), "mysql", nil
	case "postgres", "postgresql":
		if port == 0 {
			port = 5432
		}
		ssl := "disable"
		if c.TLS {
			ssl = "require"
		}
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=10",
			host, port, c.User, password, c.DBName, ssl), "postgres", nil
	}
	return "", "", fmt.Errorf("unsupported driver %q", c.Driver)
}

// Render formats a result as a text table for an agent to read. A table costs
// far fewer tokens than the equivalent JSON and is easier for a model to reason
// over.
func (r *Result) Render() string {
	if r.Kind == KindWrite {
		return fmt.Sprintf("Statement ran in %s. Rows affected: %d.", r.Took, r.Affected)
	}
	if r.RowCount == 0 {
		return fmt.Sprintf("No rows. (%s)", r.Took)
	}
	widths := make([]int, len(r.Columns))
	for i, c := range r.Columns {
		widths[i] = len(c)
	}
	cells := make([][]string, len(r.Rows))
	for ri, row := range r.Rows {
		cells[ri] = make([]string, len(r.Columns))
		for ci := range r.Columns {
			var s string
			if ci < len(row) {
				s = fmt.Sprintf("%v", row[ci])
				if row[ci] == nil {
					s = "NULL"
				}
			}
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			cells[ri][ci] = s
			if len(s) > widths[ci] {
				widths[ci] = len(s)
			}
		}
	}
	var b strings.Builder
	for i, c := range r.Columns {
		fmt.Fprintf(&b, "%-*s  ", widths[i], c)
	}
	b.WriteString("\n")
	for i := range r.Columns {
		b.WriteString(strings.Repeat("-", widths[i]) + "  ")
	}
	b.WriteString("\n")
	for _, row := range cells {
		for i, s := range row {
			fmt.Fprintf(&b, "%-*s  ", widths[i], s)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%d rows in %s", r.RowCount, r.Took)
	if r.Truncated {
		fmt.Fprintf(&b, " (capped at %d; add a LIMIT and an ORDER BY to page through)", MaxRows)
	}
	return b.String()
}
