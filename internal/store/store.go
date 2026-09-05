// Package store is the time-series store: SQLite (modernc, CGo-free) for a
// file database, or Turso/libSQL over the network. Same DDL, one driver
// switch. All writes are idempotent upserts so a second process (or a second
// serverless instance) never corrupts state.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql" // driver "libsql"
	_ "modernc.org/sqlite"                               // driver "sqlite"
)

// Mode is the storage backend in use.
type Mode string

// Modes.
const (
	ModeFile   Mode = "file"   // modernc.org/sqlite, WAL, single writer goroutine
	ModeRemote Mode = "remote" // libsql driver (Turso), direct writes
)

// DefaultURL is the DIVY_DB_URL default.
const DefaultURL = "file:./data/divy.db"

// Store is one open database.
type Store struct {
	URL  string
	Mode Mode
	Path string // file mode only

	r *sql.DB // readers (file mode) / the only handle (remote)
	w *sql.DB // writer (file mode)

	jobs   chan job
	stop   chan struct{}
	done   chan struct{}
	gen    atomic.Uint64
	closed atomic.Bool

	seriesMu sync.Mutex
	series   map[string]int64 // metric + "\x00" + labels → id
}

type job struct {
	ctx  context.Context
	fn   func(tx *sql.Tx) error
	done chan error
}

// ResolveURL applies the env precedence: TURSO_DATABASE_URL (+TURSO_AUTH_TOKEN
// as authToken) wins over DIVY_DB_URL, which defaults to DefaultURL.
func ResolveURL(getenv func(string) string) string {
	if t := strings.TrimSpace(getenv("TURSO_DATABASE_URL")); t != "" {
		if tok := strings.TrimSpace(getenv("TURSO_AUTH_TOKEN")); tok != "" {
			sep := "?"
			if strings.Contains(t, "?") {
				sep = "&"
			}
			return t + sep + "authToken=" + url.QueryEscape(tok)
		}
		return t
	}
	if u := strings.TrimSpace(getenv("DIVY_DB_URL")); u != "" {
		return u
	}
	return DefaultURL
}

// ModeOf classifies a database URL.
func ModeOf(u string) (Mode, error) {
	switch {
	case strings.HasPrefix(u, "file:"):
		return ModeFile, nil
	case strings.HasPrefix(u, "libsql://"), strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "wss://"), strings.HasPrefix(u, "ws://"):
		return ModeRemote, nil
	case strings.Contains(u, "://"):
		return "", fmt.Errorf("store: unsupported database URL scheme in %q (want file:, libsql://, https://)", u)
	default:
		return ModeFile, nil // bare path
	}
}

// Open opens the database, checks WAL in file mode and runs pending migrations.
func Open(ctx context.Context, dbURL string) (*Store, error) {
	mode, err := ModeOf(dbURL)
	if err != nil {
		return nil, err
	}
	s := &Store{URL: dbURL, Mode: mode, series: map[string]int64{}}
	switch mode {
	case ModeFile:
		path := strings.TrimPrefix(dbURL, "file:")
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			return nil, errors.New("store: empty file path")
		}
		s.Path = path
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("store: mkdir: %w", err)
		}
		wdsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
		rdsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
		w, err := sql.Open("sqlite", wdsn)
		if err != nil {
			return nil, fmt.Errorf("store: open writer: %w", err)
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		var mode string
		if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("store: journal_mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			_ = w.Close()
			return nil, fmt.Errorf("sqlite: WAL not available at %s (network filesystem?)", path)
		}
		r, err := sql.Open("sqlite", rdsn)
		if err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("store: open reader: %w", err)
		}
		n := runtime.GOMAXPROCS(0)
		if n < 4 {
			n = 4
		}
		r.SetMaxOpenConns(n)
		s.w, s.r = w, r
	case ModeRemote:
		db, err := sql.Open("libsql", dbURL)
		if err != nil {
			return nil, fmt.Errorf("store: open libsql: %w", err)
		}
		db.SetMaxOpenConns(8)
		s.r, s.w = db, db
	}
	if _, err := s.Migrate(ctx); err != nil {
		_ = s.closeHandles()
		return nil, err
	}
	if mode == ModeFile {
		s.jobs = make(chan job, 64)
		s.stop = make(chan struct{})
		s.done = make(chan struct{})
		go s.run()
	}
	return s, nil
}

// run is the single writer goroutine (file mode).
func (s *Store) run() {
	defer close(s.done)
	for {
		select {
		case j := <-s.jobs:
			j.done <- s.exec(j.ctx, j.fn)
		case <-s.stop:
			// drain
			for {
				select {
				case j := <-s.jobs:
					j.done <- s.exec(j.ctx, j.fn)
				default:
					return
				}
			}
		}
	}
}

func (s *Store) exec(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Write runs fn inside one write transaction. In file mode it is queued to
// the writer goroutine and blocks until committed; in remote mode it runs
// directly (Turso serialises writers).
func (s *Store) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s.closed.Load() {
		return errors.New("store: closed")
	}
	if s.Mode == ModeRemote {
		return s.exec(ctx, fn)
	}
	j := job{ctx: ctx, fn: fn, done: make(chan error, 1)}
	select {
	case s.jobs <- j:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-j.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reader returns the read handle.
func (s *Store) Reader() *sql.DB { return s.r }

// Generation is bumped after every committed write to series, samples or
// probe_results; response caches key on it.
func (s *Store) Generation() uint64 { return s.gen.Load() }

func (s *Store) bump() { s.gen.Add(1) }

// Ping runs SELECT 1 on the read pool and returns the latency.
func (s *Store) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	var one int
	err := s.r.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	return time.Since(start), err
}

// Checkpoint truncates the WAL (file mode; no-op otherwise).
func (s *Store) Checkpoint(ctx context.Context) error {
	if s.Mode != ModeFile {
		return nil
	}
	_, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Close drains the writer, checkpoints and closes the handles.
func (s *Store) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	if s.Mode == ModeFile {
		close(s.stop)
		<-s.done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Checkpoint(ctx)
		cancel()
	}
	return s.closeHandles()
}

func (s *Store) closeHandles() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	if s.w != nil && s.w != s.r {
		errs = append(errs, s.w.Close())
	}
	return errors.Join(errs...)
}
