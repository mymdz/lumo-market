// Package postgres is the driven adapter persisting the simulation into
// PostgreSQL.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mymdz/lumo-market/internal/ports"
)

//go:embed schema.sql
var schemaSQL string

//go:embed constraints.sql
var constraintsSQL string

type job struct {
	ops  []op
	done chan struct{}
	size int
}

// Store implements ports.Store.
type Store struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	applied map[string]bool

	queue   chan *job
	wg      sync.WaitGroup
	errMu   sync.Mutex
	err     error
	last    *job
	started bool
}

func New(ctx context.Context, dsn string, log *slog.Logger) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET synchronous_commit = off")
		return err
	}
	var pool *pgxpool.Pool
	for i := 0; i < 30; i++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
			pool.Close()
		}
		log.Info("waiting for postgres", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool, log: log, applied: map[string]bool{}, queue: make(chan *job, 1)}
	return s, nil
}

func (s *Store) startWriter() {
	if s.started {
		return
	}
	s.started = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for j := range s.queue {
			if s.failed() == nil {
				if err := s.write(context.Background(), j.ops); err != nil {
					s.errMu.Lock()
					s.err = err
					s.errMu.Unlock()
				}
			}
			close(j.done)
		}
	}()
}

func (s *Store) failed() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *Store) Close() {
	if s.started {
		close(s.queue)
		s.wg.Wait()
	}
	s.pool.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, "SELECT id FROM sim.migrations")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		s.applied[id] = true
	}
	return rows.Err()
}

func (s *Store) Meta(ctx context.Context) (*ports.Meta, error) {
	var m ports.Meta
	var seed int64
	var state []byte
	err := s.pool.QueryRow(ctx, "SELECT phase, sim_time, horizon, start_time, seed, state FROM sim.meta WHERE id = 1").
		Scan(&m.Phase, &m.SimTime, &m.Horizon, &m.Start, &seed, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Seed = uint64(seed)
	m.StateJSON = state
	m.SimTime, m.Horizon, m.Start = m.SimTime.UTC(), m.Horizon.UTC(), m.Start.UTC()
	return &m, nil
}

func (s *Store) Reset(ctx context.Context) error {
	if err := s.Flush(ctx); err != nil {
		s.log.Warn("flush before reset failed", "err", err)
	}
	_, err := s.pool.Exec(ctx, "DROP SCHEMA IF EXISTS shop CASCADE; DROP SCHEMA IF EXISTS sim CASCADE")
	s.applied = map[string]bool{}
	return err
}

// Apply encodes the batch synchronously and hands it to the writer.
func (s *Store) Apply(ctx context.Context, b *ports.Batch) error {
	if err := s.failed(); err != nil {
		return err
	}
	s.startWriter()
	ops := encodeBatch(b, s.applied)
	j := &job{ops: ops, done: make(chan struct{})}
	select {
	case s.queue <- j:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.last = j
	return nil
}

func (s *Store) Flush(ctx context.Context) error {
	if s.last != nil {
		select {
		case <-s.last.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.failed()
}

func (s *Store) write(ctx context.Context, ops []op) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	for i := range ops {
		o := &ops[i]
		switch o.kind {
		case opExec:
			if _, err := tx.Exec(ctx, o.sql, o.args...); err != nil {
				return fmt.Errorf("exec %q: %w", short(o.sql), err)
			}
		case opCopy:
			if _, err := tx.CopyFrom(ctx, tableIdent(o.table), o.cols, pgx.CopyFromRows(o.rows)); err != nil {
				return fmt.Errorf("copy %s: %w", o.table, err)
			}
		case opUpdate, opUpsert:
			if err := s.viaTemp(ctx, tx, o, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func tableIdent(name string) pgx.Identifier {
	return pgx.Identifier(strings.Split(name, "."))
}

// viaTemp stages rows in a temporary table and applies them with a single
// UPDATE ... FROM or INSERT ... ON CONFLICT statement.
func (s *Store) viaTemp(ctx context.Context, tx pgx.Tx, o *op, n int) error {
	tmp := fmt.Sprintf("tmp_%s_%d", strings.ReplaceAll(o.table, ".", "_"), n)
	cols := strings.Join(o.cols, ", ")
	if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE TEMP TABLE %s ON COMMIT DROP AS SELECT %s FROM %s WITH NO DATA", tmp, cols, o.table)); err != nil {
		return fmt.Errorf("temp %s: %w", o.table, err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{tmp}, o.cols, pgx.CopyFromRows(o.rows)); err != nil {
		return fmt.Errorf("copy temp %s: %w", o.table, err)
	}
	isKey := map[string]bool{}
	for _, k := range o.keys {
		isKey[k] = true
	}
	var sets, conds []string
	for _, cl := range o.cols {
		if isKey[cl] {
			conds = append(conds, fmt.Sprintf("t.%s = s.%s", cl, cl))
		} else if o.kind == opUpdate {
			sets = append(sets, fmt.Sprintf("%s = s.%s", cl, cl))
		} else {
			sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", cl, cl))
		}
	}
	var sql string
	if o.kind == opUpdate {
		sql = fmt.Sprintf("UPDATE %s t SET %s FROM %s s WHERE %s", o.table, strings.Join(sets, ", "), tmp, strings.Join(conds, " AND "))
	} else {
		sql = fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s ON CONFLICT (%s) DO UPDATE SET %s", o.table, cols, cols, tmp, strings.Join(o.keys, ", "), strings.Join(sets, ", "))
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("apply %s: %w", o.table, err)
	}
	return nil
}

// FinalizeBulkLoad creates constraints and indexes once.
func (s *Store) FinalizeBulkLoad(ctx context.Context) error {
	if err := s.Flush(ctx); err != nil {
		return err
	}
	var done bool
	if err := s.pool.QueryRow(ctx, "SELECT finalized FROM sim.meta WHERE id = 1").Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	var code []string
	for _, l := range strings.Split(constraintsSQL, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "--") {
			code = append(code, l)
		}
	}
	for _, stmt := range strings.Split(strings.Join(code, "\n"), ";") {
		sql := strings.TrimSpace(stmt)
		if sql == "" {
			continue
		}
		if _, err := s.pool.Exec(ctx, sql); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && (pe.Code == "42710" || pe.Code == "42P07") {
				continue // already exists
			}
			return fmt.Errorf("%s: %w", short(sql), err)
		}
	}
	if _, err := s.pool.Exec(ctx, "ANALYZE"); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "UPDATE sim.meta SET finalized = true WHERE id = 1")
	return err
}

func (s *Store) Stats(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.relname, c.reltuples::bigint FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'shop' AND c.relkind = 'r'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}
