package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var state struct {
	sync.Mutex
	db      *sql.DB
	url     string
	ready   bool
	lastTry time.Time
}

// Enabled reports whether PostgreSQL was configured for this process.
func Enabled() bool { return os.Getenv("DATABASE_URL") != "" }

func database() (*sql.DB, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL not configured")
	}
	state.Lock()
	defer state.Unlock()
	if state.ready && state.db != nil && state.url == url {
		return state.db, nil
	}
	if state.db != nil && state.url != url {
		_ = state.db.Close()
		state.db = nil
		state.ready = false
	}
	if !state.lastTry.IsZero() && time.Since(state.lastTry) < 5*time.Second {
		return nil, errors.New("database reconnect backoff")
	}
	state.lastTry = time.Now()
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err == nil {
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS app_documents (
			kind text NOT NULL,
			key text NOT NULL,
			payload jsonb NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (kind, key)
		)`)
	}
	if err != nil {
		_ = db.Close()
		slog.Error("postgres indisponivel; usando persistencia local", "error", err)
		return nil, err
	}
	state.db, state.url, state.ready = db, url, true
	return db, nil
}

func Get(kind, key string, out any) (bool, error) {
	db, err := database()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT payload FROM app_documents WHERE kind=$1 AND key=$2`, kind, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}

func Put(kind, key string, value any) error {
	db, err := database()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `INSERT INTO app_documents(kind,key,payload,updated_at)
		VALUES($1,$2,$3,now()) ON CONFLICT(kind,key) DO UPDATE SET payload=excluded.payload,updated_at=now()`, kind, key, raw)
	return err
}

func List(kind string, out any) error {
	db, err := database()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT payload FROM app_documents WHERE kind=$1 ORDER BY updated_at`, kind)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := make([]json.RawMessage, 0)
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		items = append(items, raw)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func Ping(ctx context.Context) error {
	db, err := database()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}
