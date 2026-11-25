package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type SQLite struct {
	ReadDB  *sql.DB
	WriteDB *sql.DB
}

func NewSQLite(ctx context.Context, cfg Config) (*SQLite, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}

	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("database path cannot be empty")
	}

	if cfg.MaxReadConnections <= 0 {
		return nil, errors.New("MaxReadConnections must be greater than zero")
	}

	if cfg.MaxWriteConnections <= 0 {
		return nil, errors.New("MaxWriteConnections must be greater than zero")
	}

	if cfg.BusyTimeout <= 0 {
		return nil, errors.New("BusyTimeout must be greater than zero")
	}

	writeDSN := buildDSN(cfg, "rwc")

	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open write database: %w", err)
	}

	if err := configureDB(writeDB, cfg.MaxWriteConnections); err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("failed to configure write database: %w", err)
	}

	if err := writeDB.PingContext(ctx); err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("failed to ping write database: %w", err)
	}

	if _, err := writeDB.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	readDSN := buildDSN(cfg, "ro")

	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("failed to open read database: %w", err)
	}

	if err := configureDB(readDB, cfg.MaxReadConnections); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("failed to configure read database: %w", err)
	}

	if err := readDB.PingContext(ctx); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("failed to ping read database: %w", err)
	}

	return &SQLite{
		ReadDB:  readDB,
		WriteDB: writeDB,
	}, nil
}

func buildDSN(cfg Config, mode string) string {
	query := url.Values{}

	query.Set("mode", mode)
	busyTimeout := cfg.BusyTimeout.Milliseconds()
	if busyTimeout < 1 {
		busyTimeout = 1
	}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout))
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "synchronous(FULL)")

	databasePath := cfg.Path
	if !filepath.IsAbs(databasePath) {
		if absolutePath, err := filepath.Abs(databasePath); err == nil {
			databasePath = absolutePath
		}
	}

	u := url.URL{
		Scheme:   "file",
		Path:     databasePath,
		RawQuery: query.Encode(),
	}

	return u.String()
}

func configureDB(db *sql.DB, maxConns int) error {
	if db == nil {
		return errors.New("database connection is nil")
	}

	if maxConns <= 0 {
		return errors.New("maxConns must be greater than zero")
	}

	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(0)

	return nil
}

func (s *SQLite) Close() error {
	if s == nil {
		return errors.New("database instance is nil")
	}

	var readErr, writeErr error

	if s.ReadDB != nil {
		readErr = s.ReadDB.Close()
	}

	if s.WriteDB != nil {
		writeErr = s.WriteDB.Close()
	}

	return errors.Join(
		wrapError("failed to close read database", readErr),
		wrapError("failed to close write database", writeErr),
	)
}

func wrapError(message string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", message, err)
}
