// Package sqlite3local provides the subset of a SQLite database/sql driver
// required by Vector, backed by the system libsqlite3 library.
package sqlite3local

/*
#cgo LDFLAGS: -lsqlite3 -lm -ldl -lpthread
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"
)

func init() {
	sql.Register("sqlite3", &Driver{})
}

// Error is a SQLite operation error. Code is the primary SQLite result code.
// Keeping the code allows callers to distinguish expected constraint conflicts
// from storage, corruption, permission, and I/O failures without parsing text.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "sqlite: unknown error"
	}
	return fmt.Sprintf("sqlite (%d): %s", e.Code, e.Message)
}

// IsConstraint reports whether err is a SQLite constraint violation.
func IsConstraint(err error) bool {
	var sqliteErr *Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code == int(C.SQLITE_CONSTRAINT)
}

type Driver struct{}

func (d *Driver) Open(name string) (driver.Conn, error) {
	path, options := splitDSN(name)
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var db *C.sqlite3
	flags := C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX
	if rc := C.sqlite3_open_v2(cpath, &db, C.int(flags), nil); rc != C.SQLITE_OK {
		msg := "unable to open sqlite database"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close_v2(db)
		}
		return nil, &Error{Code: int(C.SQLITE_CANTOPEN), Message: msg}
	}

	conn := &Conn{db: db}
	C.sqlite3_busy_timeout(db, 5000)
	for _, pragma := range []string{"PRAGMA trusted_schema=OFF", "PRAGMA secure_delete=ON"} {
		if _, err := conn.execDirect(pragma); err != nil {
			conn.Close()
			return nil, err
		}
	}

	if truthy(options.Get("_foreign_keys")) || truthy(options.Get("foreign_keys")) {
		if _, err := conn.execDirect("PRAGMA foreign_keys=ON"); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if mode := options.Get("_journal_mode"); mode != "" {
		mode = strings.ToUpper(mode)
		for _, r := range mode {
			if !((r >= 'A' && r <= 'Z') || r == '_') {
				conn.Close()
				return nil, fmt.Errorf("sqlite: invalid journal mode")
			}
		}
		if _, err := conn.execDirect("PRAGMA journal_mode=" + mode); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func splitDSN(name string) (string, url.Values) {
	parts := strings.SplitN(name, "?", 2)
	if len(parts) == 1 {
		return parts[0], url.Values{}
	}
	v, _ := url.ParseQuery(parts[1])
	return parts[0], v
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type Conn struct {
	mu     sync.Mutex
	db     *C.sqlite3
	closed bool
}

func (c *Conn) check() error {
	if c == nil || c.db == nil || c.closed {
		return errors.New("sqlite: connection is closed")
	}
	return nil
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	return &Stmt{conn: c, query: query}, nil
}

func (c *Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.Prepare(query)
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.db == nil {
		return nil
	}
	if rc := C.sqlite3_close_v2(c.db); rc != C.SQLITE_OK {
		return c.errLocked(rc)
	}
	c.db = nil
	c.closed = true
	return nil
}

func (c *Conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.ReadOnly {
		return nil, errors.New("sqlite: read-only transactions are not supported")
	}
	if _, err := c.execDirect("BEGIN"); err != nil {
		return nil, err
	}
	return &Tx{conn: c}, nil
}

func (c *Conn) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.execDirect("SELECT 1")
	return err
}

func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := make([]driver.Value, len(args))
	for i := range args {
		values[i] = args[i].Value
	}
	return c.exec(query, values)
}

func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := make([]driver.Value, len(args))
	for i := range args {
		values[i] = args[i].Value
	}
	return c.query(query, values)
}

func (c *Conn) execDirect(query string) (driver.Result, error) {
	return c.exec(query, nil)
}

func (c *Conn) exec(query string, args []driver.Value) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.check(); err != nil {
		return nil, err
	}

	// sqlite3_exec handles migration strings containing multiple statements.
	if len(args) == 0 {
		cquery := C.CString(query)
		defer C.free(unsafe.Pointer(cquery))
		var errMsg *C.char
		rc := C.sqlite3_exec(c.db, cquery, nil, nil, &errMsg)
		if rc != C.SQLITE_OK {
			msg := "sqlite execution failed"
			if errMsg != nil {
				msg = C.GoString(errMsg)
				C.sqlite3_free(unsafe.Pointer(errMsg))
			}
			return nil, &Error{Code: int(rc) & 0xff, Message: msg}
		}
		return result{lastID: int64(C.sqlite3_last_insert_rowid(c.db)), affected: int64(C.sqlite3_changes(c.db))}, nil
	}

	stmt, err := c.prepareLocked(query)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindAll(stmt, args); err != nil {
		return nil, err
	}
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_ROW:
			continue
		case C.SQLITE_DONE:
			return result{lastID: int64(C.sqlite3_last_insert_rowid(c.db)), affected: int64(C.sqlite3_changes(c.db))}, nil
		default:
			return nil, c.errLocked(rc)
		}
	}
}

func (c *Conn) query(query string, args []driver.Value) (driver.Rows, error) {
	c.mu.Lock()
	if err := c.check(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	stmt, err := c.prepareLocked(query)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if err := bindAll(stmt, args); err != nil {
		C.sqlite3_finalize(stmt)
		c.mu.Unlock()
		return nil, err
	}

	n := int(C.sqlite3_column_count(stmt))
	cols := make([]string, n)
	decls := make([]string, n)
	for i := 0; i < n; i++ {
		cols[i] = C.GoString(C.sqlite3_column_name(stmt, C.int(i)))
		if p := C.sqlite3_column_decltype(stmt, C.int(i)); p != nil {
			decls[i] = strings.ToUpper(C.GoString(p))
		}
	}
	return &Rows{conn: c, stmt: stmt, columns: cols, declTypes: decls}, nil
}

func (c *Conn) prepareLocked(query string) (*C.sqlite3_stmt, error) {
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(c.db, cquery, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, c.errLocked(rc)
	}
	return stmt, nil
}

func (c *Conn) errLocked(rc C.int) error {
	msg := "unknown sqlite error"
	if c != nil && c.db != nil {
		msg = C.GoString(C.sqlite3_errmsg(c.db))
	}
	return &Error{Code: int(rc) & 0xff, Message: msg}
}

type Stmt struct {
	conn  *Conn
	query string
}

func (s *Stmt) Close() error                                    { return nil }
func (s *Stmt) NumInput() int                                   { return -1 }
func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) { return s.conn.exec(s.query, args) }
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error)  { return s.conn.query(s.query, args) }
func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

type Tx struct {
	conn *Conn
	done bool
}

func (t *Tx) Commit() error {
	if t.done {
		return errors.New("sqlite: transaction already completed")
	}
	_, err := t.conn.execDirect("COMMIT")
	t.done = true
	if err != nil {
		// SQLite may leave a transaction active after a failed COMMIT (for
		// example, a deferred foreign-key violation or an I/O failure). Always
		// attempt rollback before database/sql returns this connection to the pool.
		_, _ = t.conn.execDirect("ROLLBACK")
	}
	return err
}

func (t *Tx) Rollback() error {
	if t.done {
		return errors.New("sqlite: transaction already completed")
	}
	t.done = true
	_, err := t.conn.execDirect("ROLLBACK")
	return err
}

type Rows struct {
	conn      *Conn
	stmt      *C.sqlite3_stmt
	columns   []string
	declTypes []string
	closed    bool
}

func (r *Rows) Columns() []string { return r.columns }

func (r *Rows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.stmt != nil {
		C.sqlite3_finalize(r.stmt)
		r.stmt = nil
	}
	r.conn.mu.Unlock()
	return nil
}

func (r *Rows) Next(dest []driver.Value) error {
	if r.closed || r.stmt == nil {
		return io.EOF
	}
	rc := C.sqlite3_step(r.stmt)
	if rc == C.SQLITE_DONE {
		_ = r.Close()
		return io.EOF
	}
	if rc != C.SQLITE_ROW {
		err := r.conn.errLocked(rc)
		_ = r.Close()
		return err
	}

	for i := range dest {
		idx := C.int(i)
		switch C.sqlite3_column_type(r.stmt, idx) {
		case C.SQLITE_NULL:
			dest[i] = nil
		case C.SQLITE_INTEGER:
			dest[i] = int64(C.sqlite3_column_int64(r.stmt, idx))
		case C.SQLITE_FLOAT:
			dest[i] = float64(C.sqlite3_column_double(r.stmt, idx))
		case C.SQLITE_BLOB:
			n := int(C.sqlite3_column_bytes(r.stmt, idx))
			p := C.sqlite3_column_blob(r.stmt, idx)
			if p == nil || n == 0 {
				dest[i] = []byte{}
			} else {
				dest[i] = C.GoBytes(p, C.int(n))
			}
		default:
			n := int(C.sqlite3_column_bytes(r.stmt, idx))
			p := C.sqlite3_column_text(r.stmt, idx)
			s := ""
			if p != nil && n > 0 {
				s = C.GoStringN((*C.char)(unsafe.Pointer(p)), C.int(n))
			}
			if isTimeType(r.declTypes[i]) {
				if t, ok := parseSQLiteTime(s); ok {
					dest[i] = t
					continue
				}
			}
			dest[i] = s
		}
	}
	return nil
}

func isTimeType(decl string) bool {
	return strings.Contains(decl, "DATE") || strings.Contains(decl, "TIME")
}

func parseSQLiteTime(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func bindAll(stmt *C.sqlite3_stmt, args []driver.Value) error {
	expected := int(C.sqlite3_bind_parameter_count(stmt))
	if expected != len(args) {
		return fmt.Errorf("sqlite: expected %d arguments, got %d", expected, len(args))
	}
	for i, value := range args {
		if err := bindValue(stmt, C.int(i+1), value); err != nil {
			return err
		}
	}
	return nil
}

func bindValue(stmt *C.sqlite3_stmt, index C.int, value driver.Value) error {
	var rc C.int
	switch v := value.(type) {
	case nil:
		rc = C.sqlite3_bind_null(stmt, index)
	case int64:
		rc = C.sqlite3_bind_int64(stmt, index, C.sqlite3_int64(v))
	case float64:
		rc = C.sqlite3_bind_double(stmt, index, C.double(v))
	case bool:
		if v {
			rc = C.sqlite3_bind_int64(stmt, index, 1)
		} else {
			rc = C.sqlite3_bind_int64(stmt, index, 0)
		}
	case []byte:
		if len(v) == 0 {
			rc = C.sqlite3_bind_blob(stmt, index, nil, 0, C.SQLITE_TRANSIENT)
		} else {
			rc = C.sqlite3_bind_blob(stmt, index, unsafe.Pointer(&v[0]), C.int(len(v)), C.SQLITE_TRANSIENT)
		}
	case string:
		cs := C.CString(v)
		rc = C.sqlite3_bind_text(stmt, index, cs, C.int(len(v)), C.SQLITE_TRANSIENT)
		C.free(unsafe.Pointer(cs))
	case time.Time:
		s := v.Format("2006-01-02 15:04:05.999999999-07:00")
		cs := C.CString(s)
		rc = C.sqlite3_bind_text(stmt, index, cs, C.int(len(s)), C.SQLITE_TRANSIENT)
		C.free(unsafe.Pointer(cs))
	default:
		return fmt.Errorf("sqlite: unsupported argument type %T", value)
	}
	if rc != C.SQLITE_OK {
		return fmt.Errorf("sqlite: bind failed with code %d", int(rc))
	}
	return nil
}

type result struct {
	lastID   int64
	affected int64
}

func (r result) LastInsertId() (int64, error) { return r.lastID, nil }
func (r result) RowsAffected() (int64, error) { return r.affected, nil }

var (
	_ driver.Driver             = (*Driver)(nil)
	_ driver.Conn               = (*Conn)(nil)
	_ driver.ConnPrepareContext = (*Conn)(nil)
	_ driver.ConnBeginTx        = (*Conn)(nil)
	_ driver.ExecerContext      = (*Conn)(nil)
	_ driver.QueryerContext     = (*Conn)(nil)
	_ driver.Pinger             = (*Conn)(nil)
	_ driver.Stmt               = (*Stmt)(nil)
	_ driver.StmtExecContext    = (*Stmt)(nil)
	_ driver.StmtQueryContext   = (*Stmt)(nil)
	_ driver.Tx                 = (*Tx)(nil)
	_ driver.Rows               = (*Rows)(nil)
)
