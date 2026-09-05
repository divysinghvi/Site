package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var migrationNameRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one embedded SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// MigrationStatus is one row of `divy migrate --status`.
type MigrationStatus struct {
	Version   int
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// Migrations lists the embedded migrations in version order.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: bad migration file name %q", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("store: duplicate migration version %04d", out[i].Version)
		}
	}
	return out, nil
}

// splitStatements strips -- comments and splits on semicolons.
func splitStatements(sqlText string) []string {
	var sb strings.Builder
	for _, ln := range strings.Split(sqlText, "\n") {
		if i := strings.Index(ln, "--"); i >= 0 {
			ln = ln[:i]
		}
		sb.WriteString(ln)
		sb.WriteByte('\n')
	}
	var out []string
	for _, st := range strings.Split(sb.String(), ";") {
		if st = strings.TrimSpace(st); st != "" {
			out = append(out, st)
		}
	}
	return out
}

// Migrate applies every pending migration (forward only) and returns the
// names applied. It is idempotent and safe to run from several processes.
func (s *Store) Migrate(ctx context.Context) ([]string, error) {
	return s.migrateTo(ctx, -1)
}

// MigrateTo applies pending migrations with version ≤ target (-1 = all).
func (s *Store) MigrateTo(ctx context.Context, target int) ([]string, error) {
	return s.migrateTo(ctx, target)
}

func (s *Store) migrateTo(ctx context.Context, target int) ([]string, error) {
	all, err := Migrations()
	if err != nil {
		return nil, err
	}
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  applied_ms INTEGER NOT NULL
)`); err != nil {
		return nil, fmt.Errorf("store: schema_migrations: %w", err)
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	// applied must be a prefix of the embedded set
	for i, v := range applied {
		if i >= len(all) {
			return nil, fmt.Errorf("database is newer than binary: has %04d, binary knows %04d", v, all[len(all)-1].Version)
		}
		if all[i].Version != v {
			return nil, fmt.Errorf("migration gap: %04d applied but %04d not", v, all[i].Version)
		}
	}
	current := 0
	if len(applied) > 0 {
		current = applied[len(applied)-1]
	}
	if target >= 0 && target < current {
		return nil, fmt.Errorf("down migrations are not supported (current %04d, requested %04d); restore a backup", current, target)
	}
	var names []string
	for _, m := range all[len(applied):] {
		if target >= 0 && m.Version > target {
			break
		}
		err := s.exec(ctx, func(tx *sql.Tx) error {
			for _, st := range splitStatements(m.SQL) {
				if _, err := tx.ExecContext(ctx, st); err != nil {
					return fmt.Errorf("%04d_%s: %w", m.Version, m.Name, err)
				}
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, applied_ms) VALUES (?, ?, ?)", m.Version, m.Name, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			// A concurrent process may have applied it first: re-check.
			if again, aerr := s.appliedVersions(ctx); aerr == nil && contains(again, m.Version) {
				continue
			}
			return names, fmt.Errorf("store: migrate: %w", err)
		}
		names = append(names, fmt.Sprintf("%04d_%s", m.Version, m.Name))
	}
	return names, nil
}

func contains(vs []int, v int) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Store) appliedVersions(ctx context.Context) ([]int, error) {
	rows, err := s.w.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Status reports every embedded migration and whether it is applied.
func (s *Store) Status(ctx context.Context) ([]MigrationStatus, error) {
	all, err := Migrations()
	if err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, "SELECT version, applied_ms FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	at := map[int]int64{}
	for rows.Next() {
		var v int
		var ms int64
		if err := rows.Scan(&v, &ms); err != nil {
			return nil, err
		}
		at[v] = ms
	}
	var out []MigrationStatus
	for _, m := range all {
		st := MigrationStatus{Version: m.Version, Name: m.Name}
		if ms, ok := at[m.Version]; ok {
			st.Applied = true
			st.AppliedAt = time.UnixMilli(ms).UTC()
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
