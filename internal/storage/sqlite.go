package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS events (id TEXT PRIMARY KEY, payload BLOB NOT NULL, start_time TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS market_mappings (exchange TEXT NOT NULL, ticker TEXT NOT NULL, event_id TEXT NOT NULL, confidence INTEGER NOT NULL, status TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(exchange,ticker))`,
		`CREATE TABLE IF NOT EXISTS audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at TEXT NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS parent_orders (id TEXT PRIMARY KEY, status TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database init: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveEvents(ctx context.Context, events []domain.CanonicalEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events(id,payload,start_time,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,start_time=excluded.start_time,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, event.ID, payload, event.StartTime.Format(time.RFC3339Nano), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadEvents(ctx context.Context) ([]domain.CanonicalEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM events ORDER BY start_time`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.CanonicalEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event domain.CanonicalEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) Audit(ctx context.Context, kind string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_log(occurred_at,kind,payload) VALUES(?,?,?)`, time.Now().UTC().Format(time.RFC3339Nano), kind, payload)
	return err
}

func (s *Store) SaveParentOrder(ctx context.Context, parent domain.ParentOrder) error {
	payload, err := json.Marshal(parent)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO parent_orders(id,status,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,payload=excluded.payload,updated_at=excluded.updated_at`, parent.ID, parent.Status, payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) LoadParentOrders(ctx context.Context) ([]domain.ParentOrder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM parent_orders ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parents := make([]domain.ParentOrder, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var parent domain.ParentOrder
		if err := json.Unmarshal(payload, &parent); err != nil {
			return nil, err
		}
		parents = append(parents, parent)
	}
	return parents, rows.Err()
}

func (s *Store) SaveMapping(ctx context.Context, market domain.CanonicalMarket) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO market_mappings(exchange,ticker,event_id,confidence,status,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(exchange,ticker) DO UPDATE SET event_id=excluded.event_id,confidence=excluded.confidence,status=excluded.status,updated_at=excluded.updated_at`, market.Exchange, market.ExchangeTicker, market.EventID, market.MappingConfidence, market.MappingStatus, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
