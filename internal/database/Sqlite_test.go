package database

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestWrapError(t *testing.T) {
	originalErr := errors.New("connection refused")

	testCases := []struct {
		name        string
		message     string
		err         error
		expectError bool
	}{
		{
			name:        "nil error",
			message:     "database failure",
			err:         nil,
			expectError: false,
		},
		{
			name:        "non-nil error",
			message:     "database failure",
			err:         originalErr,
			expectError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := wrapError(testCase.message, testCase.err)

			if !testCase.expectError {
				if actual != nil {
					t.Fatalf("expected nil, got %v", actual)
				}
				return
			}

			if actual == nil {
				t.Fatal("expected an error, got nil")
			}

			if !errors.Is(actual, originalErr) {
				t.Fatal("original error was not preserved")
			}

			expected := "database failure: connection refused"
			if actual.Error() != expected {
				t.Fatalf("expected %q, got %q", expected, actual.Error())
			}
		})
	}
}

func TestConfigureDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	testCases := []struct {
		name        string
		db          *sql.DB
		maxConns    int
		expectError bool
	}{
		{
			name:        "nil database",
			db:          nil,
			maxConns:    4,
			expectError: true,
		},
		{
			name:        "zero connections",
			db:          db,
			maxConns:    0,
			expectError: true,
		},
		{
			name:        "negative connections",
			db:          db,
			maxConns:    -1,
			expectError: true,
		},
		{
			name:     "valid configuration",
			db:       db,
			maxConns: 4,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := configureDB(testCase.db, testCase.maxConns)

			if testCase.expectError && err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !testCase.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if err == nil {
				actual := testCase.db.Stats().MaxOpenConnections

				if actual != testCase.maxConns {
					t.Fatalf(
						"expected %d connections, got %d",
						testCase.maxConns,
						actual,
					)
				}
			}
		})
	}
}

func TestSQLiteClose(t *testing.T) {
	readDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	writeDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		readDB.Close()
		t.Fatal(err)
	}

	testCases := []struct {
		name        string
		db          *SQLite
		expectError bool
	}{
		{
			name:        "nil receiver",
			db:          nil,
			expectError: true,
		},
		{
			name: "nil database connections",
			db:   &SQLite{},
		},
		{
			name: "read database only",
			db:   &SQLite{ReadDB: readDB},
		},
		{
			name: "write database only",
			db:   &SQLite{WriteDB: writeDB},
		},
		{
			name: "both database connections",
			db: &SQLite{
				ReadDB:  mustOpenSQLite(t),
				WriteDB: mustOpenSQLite(t),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.db.Close()

			if testCase.expectError && err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !testCase.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func mustOpenSQLite(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	return db
}

func TestBuildDSN(t *testing.T) {
	cfg := Config{
		Path:                "test.db",
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	testCases := []struct {
		name string
		mode string
	}{
		{
			name: "write mode",
			mode: "rwc",
		},
		{
			name: "read mode",
			mode: "ro",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dsn := buildDSN(cfg, testCase.mode)

			parsedURL, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}

			query := parsedURL.Query()

			if query.Get("mode") != testCase.mode {
				t.Errorf("expected mode %q, got %q", testCase.mode, query.Get("mode"))
			}

			if parsedURL.Path != filepath.Join(mustGetwd(t), cfg.Path) {
				t.Errorf("unexpected database path: %s", parsedURL.Path)
			}

			pragmas := query["_pragma"]

			expectedPragmas := []string{
				"busy_timeout(5000)",
				"foreign_keys(ON)",
				"synchronous(FULL)",
			}

			for _, expected := range expectedPragmas {
				found := false

				for _, pragma := range pragmas {
					if pragma == expected {
						found = true
						break
					}
				}

				if !found {
					t.Errorf("expected pragma %q, got %v", expected, pragmas)
				}
			}
		})
	}
}

func TestBuildDSNMinimumBusyTimeout(t *testing.T) {
	cfg := Config{
		Path:        "test.db",
		BusyTimeout: 500 * time.Microsecond,
	}

	dsn := buildDSN(cfg, "rwc")

	parsedURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	if got := parsedURL.Query().Get("_pragma"); got == "" {
		t.Fatal("expected pragmas in DSN")
	}

	found := false
	for _, pragma := range parsedURL.Query()["_pragma"] {
		if pragma == "busy_timeout(1)" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected busy_timeout(1), got %v", parsedURL.Query()["_pragma"])
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()

	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	return wd
}

func TestNewSQLiteSuccess(t *testing.T) {
	ctx := context.Background()

	cfg := Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	db, err := NewSQLite(ctx, cfg)
	if err != nil {
		t.Fatalf("expected successful initialization, got %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	if db.ReadDB == nil {
		t.Fatal("expected read database connection")
	}

	if db.WriteDB == nil {
		t.Fatal("expected write database connection")
	}

	var journalMode string
	err = db.WriteDB.QueryRowContext(
		ctx,
		"PRAGMA journal_mode",
	).Scan(&journalMode)

	if err != nil {
		t.Fatalf("failed to query journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Fatalf("expected WAL mode, got %s", journalMode)
	}

	_, err = db.ReadDB.ExecContext(
		ctx,
		"CREATE TABLE test_read_only (id INTEGER)",
	)

	if err == nil {
		t.Fatal("expected read-only database to reject write")
	}

	if actual := db.ReadDB.Stats().MaxOpenConnections; actual != 4 {
		t.Fatalf("expected 4 read connections, got %d", actual)
	}

	if actual := db.WriteDB.Stats().MaxOpenConnections; actual != 1 {
		t.Fatalf("expected 1 write connection, got %d", actual)
	}
}

func TestNewSQLiteCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	_, err := NewSQLite(ctx, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestNewSQLiteInvalidPath(t *testing.T) {
	ctx := context.Background()

	cfg := Config{
		Path:                filepath.Join(t.TempDir(), "missing", "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	_, err := NewSQLite(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for invalid database path")
	}
}

func TestNewSQLiteInvalidConfig(t *testing.T) {
	validConfig := Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	testCases := []struct {
		name   string
		config Config
	}{
		{
			name: "empty database path",
			config: Config{
				Path:                "",
				MaxReadConnections:  4,
				MaxWriteConnections: 1,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero read connections",
			config: Config{
				Path:                validConfig.Path,
				MaxReadConnections:  0,
				MaxWriteConnections: 1,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero write connections",
			config: Config{
				Path:                validConfig.Path,
				MaxReadConnections:  4,
				MaxWriteConnections: 0,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero busy timeout",
			config: Config{
				Path:                validConfig.Path,
				MaxReadConnections:  4,
				MaxWriteConnections: 1,
				BusyTimeout:         0,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewSQLite(context.Background(), testCase.config)

			if err == nil {
				t.Fatal("expected configuration validation error")
			}
		})
	}
}
