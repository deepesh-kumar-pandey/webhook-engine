package tests

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	dbpkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/database"
)

func TestSQLiteConnection(t *testing.T) {
	ctx := context.Background()

	cfg := dbpkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	}

	db, err := dbpkg.NewSQLite(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to initialize database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	testCases := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "WAL mode",
			query:    "PRAGMA journal_mode",
			expected: "wal",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var actual string

			err := db.ReadDB.QueryRowContext(ctx, testCase.query).Scan(&actual)
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}

			if actual != testCase.expected {
				t.Fatalf("expected %s, got %s", testCase.expected, actual)
			}
		})
	}

	t.Run("write database", func(t *testing.T) {
		_, err := db.WriteDB.ExecContext(ctx, `
			CREATE TABLE test_records (
				id INTEGER PRIMARY KEY,
				name TEXT NOT NULL
			)
		`)
		if err != nil {
			t.Fatalf("failed to create table: %v", err)
		}

		_, err = db.WriteDB.ExecContext(
			ctx,
			"INSERT INTO test_records (name) VALUES (?)",
			"webhook",
		)
		if err != nil {
			t.Fatalf("failed to insert record: %v", err)
		}
	})

	t.Run("read database", func(t *testing.T) {
		var actual string

		err := db.ReadDB.QueryRowContext(
			ctx,
			"SELECT name FROM test_records WHERE id = 1",
		).Scan(&actual)

		if err != nil {
			t.Fatalf("failed to read record: %v", err)
		}

		if actual != "webhook" {
			t.Fatalf("expected webhook, got %s", actual)
		}
	})

	t.Run("read database rejects writes", func(t *testing.T) {
		_, err := db.ReadDB.ExecContext(
			ctx,
			"INSERT INTO test_records (name) VALUES (?)",
			"should-fail",
		)

		if err == nil {
			t.Fatal("expected read-only database to reject write")
		}
	})

	t.Run("DSN pragmas", func(t *testing.T) {
		testCases := []struct {
			name     string
			query    string
			expected int
		}{
			{
				name:     "busy timeout",
				query:    "PRAGMA busy_timeout",
				expected: 5000,
			},
			{
				name:     "foreign keys",
				query:    "PRAGMA foreign_keys",
				expected: 1,
			},
			{
				name:     "synchronous FULL",
				query:    "PRAGMA synchronous",
				expected: 2,
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				var actual int

				err := db.ReadDB.QueryRowContext(
					ctx,
					testCase.query,
				).Scan(&actual)

				if err != nil {
					t.Fatalf("failed to query pragma: %v", err)
				}

				if actual != testCase.expected {
					t.Fatalf(
						"expected %d, got %d",
						testCase.expected,
						actual,
					)
				}
			})
		}
	})

	t.Run("connection pool limits", func(t *testing.T) {
		testCases := []struct {
			name     string
			db       *sql.DB
			expected int
		}{
			{
				name:     "read pool",
				db:       db.ReadDB,
				expected: cfg.MaxReadConnections,
			},
			{
				name:     "write pool",
				db:       db.WriteDB,
				expected: cfg.MaxWriteConnections,
			},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				actual := testCase.db.Stats().MaxOpenConnections

				if actual != testCase.expected {
					t.Fatalf(
						"expected %d connections, got %d",
						testCase.expected,
						actual,
					)
				}
			})
		}
	})
}

func TestSQLiteInvalidConfig(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name string
		cfg  dbpkg.Config
	}{
		{
			name: "empty database path",
			cfg: dbpkg.Config{
				MaxReadConnections:  4,
				MaxWriteConnections: 1,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero read connections",
			cfg: dbpkg.Config{
				Path:                filepath.Join(t.TempDir(), "test.db"),
				MaxReadConnections:  0,
				MaxWriteConnections: 1,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero write connections",
			cfg: dbpkg.Config{
				Path:                filepath.Join(t.TempDir(), "test.db"),
				MaxReadConnections:  4,
				MaxWriteConnections: 0,
				BusyTimeout:         5 * time.Second,
			},
		},
		{
			name: "zero busy timeout",
			cfg: dbpkg.Config{
				Path:                filepath.Join(t.TempDir(), "test.db"),
				MaxReadConnections:  4,
				MaxWriteConnections: 1,
				BusyTimeout:         0,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db, err := dbpkg.NewSQLite(ctx, testCase.cfg)

			if err == nil {
				if db != nil {
					db.Close()
				}
				t.Fatal("expected configuration error")
			}
		})
	}
}
