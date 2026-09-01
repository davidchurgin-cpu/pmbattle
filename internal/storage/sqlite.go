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
	// database/sql keeps a pool of connections and a PRAGMA only applies to
	// the connection it ran on, so busy_timeout and WAL are set through the
	// DSN to reach every connection. _txlock=immediate makes write
	// transactions take the lock up front instead of failing with SQLITE_BUSY
	// when two writers (schedule save, exchange restart) overlap.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
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
		`CREATE TABLE IF NOT EXISTS mapping_reviews (id TEXT PRIMARY KEY, exchange TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS mapping_overrides (exchange TEXT NOT NULL, ticker TEXT NOT NULL, event_id TEXT NOT NULL, status TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(exchange,ticker))`,
		`CREATE TABLE IF NOT EXISTS audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at TEXT NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS parent_orders (id TEXT PRIMARY KEY, status TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settlements (exchange TEXT NOT NULL, ticker TEXT NOT NULL, settled_at TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(exchange,ticker))`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("database init: %w", err)
		}
	}
	return nil
}

func (s *Store) SaveSettlements(ctx context.Context, settlements []domain.Settlement) error {
	if len(settlements) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO settlements(exchange,ticker,settled_at,payload,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(exchange,ticker) DO UPDATE SET settled_at=excluded.settled_at,payload=excluded.payload,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, settlement := range settlements {
		payload, err := json.Marshal(settlement)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, settlement.Exchange, settlement.Ticker, settlement.SettledAt.UTC().Format(time.RFC3339Nano), payload, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadSettlements(ctx context.Context, limit int) ([]domain.Settlement, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM settlements ORDER BY settled_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settlements := make([]domain.Settlement, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var settlement domain.Settlement
		if err := json.Unmarshal(payload, &settlement); err != nil {
			return nil, err
		}
		settlements = append(settlements, settlement)
	}
	return settlements, rows.Err()
}

func (s *Store) LatestSettlementTime(ctx context.Context, exchange string) (time.Time, error) {
	var value sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(settled_at) FROM settlements WHERE exchange = ?`, exchange).Scan(&value); err != nil {
		return time.Time{}, err
	}
	if !value.Valid || value.String == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse latest settlement time: %w", err)
	}
	return parsed, nil
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

func (s *Store) LoadAudit(ctx context.Context, beforeID int64, limit int) ([]domain.AuditRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	// One extra row lets the service report whether another page exists while
	// the public API still caps each returned page at 200 records.
	if limit > 201 {
		limit = 201
	}
	query := `SELECT id,occurred_at,kind,payload FROM audit_log`
	args := make([]any, 0, 2)
	if beforeID > 0 {
		query += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]domain.AuditRecord, 0, limit)
	for rows.Next() {
		var record domain.AuditRecord
		var occurred string
		var payload []byte
		if err := rows.Scan(&record.ID, &occurred, &record.Kind, &payload); err != nil {
			return nil, err
		}
		record.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return nil, fmt.Errorf("parse audit time: %w", err)
		}
		record.Payload = append(json.RawMessage(nil), payload...)
		records = append(records, record)
	}
	return records, rows.Err()
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

func (s *Store) ReplaceMappingReviews(ctx context.Context, exchange string, reviews []domain.MappingReview) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mapping_reviews WHERE lower(exchange)=lower(?)`, exchange); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO mapping_reviews(id,exchange,payload,updated_at) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for _, review := range reviews {
		review.UpdatedAt = now
		payload, err := json.Marshal(review)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, review.ID, review.Exchange, payload, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadMappingReviews(ctx context.Context, limit int) ([]domain.MappingReview, error) {
	if limit <= 0 {
		limit = 250
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM mapping_reviews ORDER BY updated_at DESC,id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviews := make([]domain.MappingReview, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var review domain.MappingReview
		if err := json.Unmarshal(payload, &review); err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

func (s *Store) MappingReview(ctx context.Context, id string) (domain.MappingReview, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM mapping_reviews WHERE id=?`, id).Scan(&payload)
	if err == sql.ErrNoRows {
		return domain.MappingReview{}, false, nil
	}
	if err != nil {
		return domain.MappingReview{}, false, err
	}
	var review domain.MappingReview
	if err := json.Unmarshal(payload, &review); err != nil {
		return domain.MappingReview{}, false, err
	}
	return review, true, nil
}

func (s *Store) DeleteMappingReview(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mapping_reviews WHERE id=?`, id)
	return err
}

func (s *Store) SaveMappingOverrides(ctx context.Context, overrides []domain.MappingOverride) error {
	if len(overrides) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO mapping_overrides(exchange,ticker,event_id,status,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(exchange,ticker) DO UPDATE SET event_id=excluded.event_id,status=excluded.status,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for _, override := range overrides {
		if _, err := stmt.ExecContext(ctx, override.Exchange, override.Ticker, override.EventID, override.Status, now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadMappingOverrides(ctx context.Context, exchange string) (map[string]domain.MappingOverride, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exchange,ticker,event_id,status,updated_at FROM mapping_overrides WHERE lower(exchange)=lower(?)`, exchange)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	overrides := make(map[string]domain.MappingOverride)
	for rows.Next() {
		var override domain.MappingOverride
		var updated string
		if err := rows.Scan(&override.Exchange, &override.Ticker, &override.EventID, &override.Status, &updated); err != nil {
			return nil, err
		}
		override.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, fmt.Errorf("parse mapping override time: %w", err)
		}
		overrides[override.Ticker] = override
	}
	return overrides, rows.Err()
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
