package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix/id"

	_ "modernc.org/sqlite"
)

const (
	driverName        = "sqlite"
	connectTimeout    = 5 * time.Second
	busyTimeoutMillis = 5000
)

// Store contains persistent sqlite handles for bot state and Matrix crypto state.
type Store struct {
	StateDB  *sql.DB
	CryptoDB *sql.DB
}

func Open(stateDBPath, cryptoDBPath string) (*Store, error) {
	stateDBPath = strings.TrimSpace(stateDBPath)
	cryptoDBPath = strings.TrimSpace(cryptoDBPath)

	if stateDBPath == "" {
		return nil, errors.New("state db path is required")
	}
	if cryptoDBPath == "" {
		return nil, errors.New("crypto db path is required")
	}
	if stateDBPath == cryptoDBPath {
		return nil, errors.New("state and crypto db paths must be different")
	}

	stateDB, err := openAndInitDB(stateDBPath, stateDDL())
	if err != nil {
		return nil, fmt.Errorf("initialize state db: %w", err)
	}

	cryptoDB, err := openAndInitDB(cryptoDBPath, cryptoDDL())
	if err != nil {
		_ = stateDB.Close()
		return nil, fmt.Errorf("initialize crypto db: %w", err)
	}

	return &Store{StateDB: stateDB, CryptoDB: cryptoDB}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}

	var errs []string
	if s.StateDB != nil {
		if err := s.StateDB.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("close state db: %v", err))
		}
	}
	if s.CryptoDB != nil {
		if err := s.CryptoDB.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("close crypto db: %v", err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// SaveFilterID persists Matrix sync filter IDs for this user.
func (s *Store) SaveFilterID(ctx context.Context, userID id.UserID, filterID string) error {
	if s == nil || s.StateDB == nil {
		return errors.New("state db is not initialized")
	}
	_, err := s.StateDB.ExecContext(ctx, `
		INSERT INTO sync_state (user_id, filter_id)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			filter_id = excluded.filter_id,
			updated_at = CURRENT_TIMESTAMP
	`, string(userID), filterID)
	if err != nil {
		return fmt.Errorf("save filter id: %w", err)
	}
	return nil
}

// LoadFilterID loads Matrix sync filter IDs for this user.
func (s *Store) LoadFilterID(ctx context.Context, userID id.UserID) (string, error) {
	if s == nil || s.StateDB == nil {
		return "", errors.New("state db is not initialized")
	}
	var filterID sql.NullString
	err := s.StateDB.QueryRowContext(ctx, `SELECT filter_id FROM sync_state WHERE user_id = ?`, string(userID)).Scan(&filterID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load filter id: %w", err)
	}
	if !filterID.Valid {
		return "", nil
	}
	return filterID.String, nil
}

// SaveNextBatch persists Matrix sync token for this user.
func (s *Store) SaveNextBatch(ctx context.Context, userID id.UserID, nextBatchToken string) error {
	if s == nil || s.StateDB == nil {
		return errors.New("state db is not initialized")
	}
	_, err := s.StateDB.ExecContext(ctx, `
		INSERT INTO sync_state (user_id, next_batch)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			next_batch = excluded.next_batch,
			updated_at = CURRENT_TIMESTAMP
	`, string(userID), nextBatchToken)
	if err != nil {
		return fmt.Errorf("save next batch: %w", err)
	}
	return nil
}

// LoadNextBatch loads Matrix sync token for this user.
func (s *Store) LoadNextBatch(ctx context.Context, userID id.UserID) (string, error) {
	if s == nil || s.StateDB == nil {
		return "", errors.New("state db is not initialized")
	}
	var nextBatch sql.NullString
	err := s.StateDB.QueryRowContext(ctx, `SELECT next_batch FROM sync_state WHERE user_id = ?`, string(userID)).Scan(&nextBatch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load next batch: %w", err)
	}
	if !nextBatch.Valid {
		return "", nil
	}
	return nextBatch.String, nil
}

func (s *Store) PutBotState(ctx context.Context, key, value string) error {
	if s == nil || s.StateDB == nil {
		return errors.New("state db is not initialized")
	}
	_, err := s.StateDB.ExecContext(ctx, `
		INSERT INTO bot_state (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = CURRENT_TIMESTAMP
	`, key, value)
	if err != nil {
		return fmt.Errorf("put bot state: %w", err)
	}
	return nil
}

func (s *Store) GetBotState(ctx context.Context, key string) (string, error) {
	if s == nil || s.StateDB == nil {
		return "", errors.New("state db is not initialized")
	}
	var value string
	err := s.StateDB.QueryRowContext(ctx, `SELECT value FROM bot_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get bot state: %w", err)
	}
	return value, nil
}

func openAndInitDB(path string, ddl []string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("ensure db directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path, busyTimeoutMillis)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	for _, stmt := range ddl {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("run schema: %w", err)
		}
	}
	return db, nil
}

func stateDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS bot_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS sync_state (
			user_id TEXT PRIMARY KEY,
			filter_id TEXT,
			next_batch TEXT,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}
}

func cryptoDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS crypto_state (
			key TEXT PRIMARY KEY,
			value BLOB NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}
}
