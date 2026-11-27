package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	databasepkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/database"
	jobpkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/job"
)

func TestCreate(t *testing.T) {
	ctx := context.Background()

	db, err := databasepkg.NewSQLite(ctx, databasepkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.WriteDB.ExecContext(ctx, `
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY NOT NULL,
			target_url TEXT NOT NULL,
			payload BLOB NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
			next_attempt_at INTEGER NOT NULL,
			locked_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (attempts <= max_attempts)
		)
	`)
	if err != nil {
		t.Fatalf("failed to create jobs table: %v", err)
	}

	repo := NewJobRepository(db)
	now := time.Now()

	validJob := &jobpkg.Job{
		ID:             "job-1",
		TargetURL:      "https://example.com/webhook",
		Payload:        []byte(`{"event":"payment.created"}`),
		IdempotencyKey: "key-1",
		Status:         jobpkg.StatusPending,
		Attempts:       0,
		MaxAttempts:    5,
		NextAttemptAt:  now,
		LastError:      "",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	testCases := []struct {
		name    string
		job     *jobpkg.Job
		wantErr bool
	}{
		{
			name:    "successful insertion",
			job:     validJob,
			wantErr: false,
		},
		{
			name:    "nil job",
			job:     nil,
			wantErr: true,
		},
		{
			name: "duplicate idempotency key",
			job: &jobpkg.Job{
				ID:             "job-2",
				TargetURL:      "https://example.com/webhook",
				Payload:        []byte(`{"event":"payment.failed"}`),
				IdempotencyKey: "key-1",
				Status:         jobpkg.StatusPending,
				Attempts:       0,
				MaxAttempts:    5,
				NextAttemptAt:  now,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := repo.Create(ctx, testCase.job)

			if testCase.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if testCase.name == "successful insertion" {
				var storedID string

				err := db.ReadDB.QueryRowContext(
					ctx,
					"SELECT id FROM jobs WHERE id = ?",
					validJob.ID,
				).Scan(&storedID)

				if err != nil {
					t.Fatalf("failed to retrieve created job: %v", err)
				}

				if storedID != validJob.ID {
					t.Fatalf("expected ID %q, got %q", validJob.ID, storedID)
				}
			}

			if testCase.name == "duplicate idempotency key" &&
				!strings.Contains(strings.ToLower(err.Error()), "unique") {
				t.Fatalf("expected unique constraint error, got: %v", err)
			}
		})
	}
}

func TestGetByID(t *testing.T) {
	ctx := context.Background()

	db, err := databasepkg.NewSQLite(ctx, databasepkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.WriteDB.ExecContext(ctx, `
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY NOT NULL,
			target_url TEXT NOT NULL,
			payload BLOB NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
			next_attempt_at INTEGER NOT NULL,
			locked_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (attempts <= max_attempts)
		)
	`)
	if err != nil {
		t.Fatalf("failed to create jobs table: %v", err)
	}

	repo := NewJobRepository(db)
	now := time.Now().Truncate(time.Millisecond)

	validJob := &jobpkg.Job{
		ID:             "job-1",
		TargetURL:      "https://example.com/webhook",
		Payload:        []byte(`{"event":"payment.created"}`),
		IdempotencyKey: "key-1",
		Status:         jobpkg.StatusPending,
		Attempts:       0,
		MaxAttempts:    5,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := repo.Create(ctx, validJob); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	lockedTime := now.Add(time.Minute)

	testCases := []struct {
		name            string
		id              string
		wantErr         bool
		wantLockedUntil *time.Time
	}{
		{
			name:    "existing job without lease",
			id:      "job-1",
			wantErr: false,
		},
		{
			name:            "existing job with lease",
			id:              "job-1",
			wantLockedUntil: &lockedTime,
		},
		{
			name:    "non-existent job",
			id:      "job-999",
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var lockedUntilValue interface{}

			if testCase.wantLockedUntil != nil {
				lockedUntilValue = testCase.wantLockedUntil.UnixMilli()
			}

			_, err := db.WriteDB.ExecContext(
				ctx,
				"UPDATE jobs SET locked_until = ? WHERE id = ?",
				lockedUntilValue,
				validJob.ID,
			)
			if err != nil {
				t.Fatalf("failed to update locked_until: %v", err)
			}

			got, err := repo.GetByID(ctx, testCase.id)

			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.ID != validJob.ID {
				t.Errorf("expected ID %q, got %q", validJob.ID, got.ID)
			}

			if got.TargetURL != validJob.TargetURL {
				t.Errorf("expected target URL %q, got %q",
					validJob.TargetURL, got.TargetURL)
			}

			if string(got.Payload) != string(validJob.Payload) {
				t.Errorf("expected payload %q, got %q",
					validJob.Payload, got.Payload)
			}

			if !got.NextAttemptAt.Equal(validJob.NextAttemptAt) {
				t.Errorf("expected NextAttemptAt %v, got %v",
					validJob.NextAttemptAt, got.NextAttemptAt)
			}

			if testCase.wantLockedUntil == nil {
				if got.LockedUntil != nil {
					t.Errorf("expected LockedUntil to be nil, got %v",
						*got.LockedUntil)
				}
			} else {
				if got.LockedUntil == nil {
					t.Fatal("expected LockedUntil to be non-nil")
				}

				if !got.LockedUntil.Equal(*testCase.wantLockedUntil) {
					t.Errorf("expected LockedUntil %v, got %v",
						*testCase.wantLockedUntil, *got.LockedUntil)
				}
			}
		})
	}
}

func TestGetByIdempotencyKey(t *testing.T) {
	ctx := context.Background()

	db, err := databasepkg.NewSQLite(ctx, databasepkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.WriteDB.ExecContext(ctx, `
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY NOT NULL,
			target_url TEXT NOT NULL,
			payload BLOB NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
			next_attempt_at INTEGER NOT NULL,
			locked_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (attempts <= max_attempts)
		)
	`)
	if err != nil {
		t.Fatalf("failed to create jobs table: %v", err)
	}

	repo := NewJobRepository(db)
	now := time.Now().Truncate(time.Millisecond)

	validJob := &jobpkg.Job{
		ID:             "job-1",
		TargetURL:      "https://example.com/webhook",
		Payload:        []byte(`{"event":"payment.created"}`),
		IdempotencyKey: "key-1",
		Status:         jobpkg.StatusPending,
		Attempts:       0,
		MaxAttempts:    5,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := repo.Create(ctx, validJob); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	lockedTime := now.Add(time.Minute)

	testCases := []struct {
		name            string
		key             string
		wantErr         bool
		wantLockedUntil *time.Time
	}{
		{
			name: "existing idempotency key without lease",
			key:  "key-1",
		},
		{
			name:            "existing idempotency key with lease",
			key:             "key-1",
			wantLockedUntil: &lockedTime,
		},
		{
			name:    "non-existent idempotency key",
			key:     "key-999",
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var lockedUntilValue interface{}

			if testCase.wantLockedUntil != nil {
				lockedUntilValue = testCase.wantLockedUntil.UnixMilli()
			}

			_, err := db.WriteDB.ExecContext(
				ctx,
				"UPDATE jobs SET locked_until = ? WHERE id = ?",
				lockedUntilValue,
				validJob.ID,
			)
			if err != nil {
				t.Fatalf("failed to update locked_until: %v", err)
			}

			got, err := repo.GetByIdempotencyKey(ctx, testCase.key)

			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.ID != validJob.ID {
				t.Errorf("expected ID %q, got %q", validJob.ID, got.ID)
			}

			if got.IdempotencyKey != validJob.IdempotencyKey {
				t.Errorf("expected idempotency key %q, got %q",
					validJob.IdempotencyKey, got.IdempotencyKey)
			}

			if got.TargetURL != validJob.TargetURL {
				t.Errorf("expected target URL %q, got %q",
					validJob.TargetURL, got.TargetURL)
			}

			if testCase.wantLockedUntil == nil {
				if got.LockedUntil != nil {
					t.Errorf("expected LockedUntil to be nil, got %v",
						*got.LockedUntil)
				}
			} else {
				if got.LockedUntil == nil {
					t.Fatal("expected LockedUntil to be non-nil")
				}

				if !got.LockedUntil.Equal(*testCase.wantLockedUntil) {
					t.Errorf("expected LockedUntil %v, got %v",
						*testCase.wantLockedUntil, *got.LockedUntil)
				}
			}
		})
	}
}

func TestClaim(t *testing.T) {
	ctx := context.Background()

	db, err := databasepkg.NewSQLite(ctx, databasepkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.WriteDB.ExecContext(ctx, `
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY NOT NULL,
			target_url TEXT NOT NULL,
			payload BLOB NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
			next_attempt_at INTEGER NOT NULL,
			locked_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (attempts <= max_attempts)
		)
	`)
	if err != nil {
		t.Fatalf("failed to create jobs table: %v", err)
	}

	repo := NewJobRepository(db)
	now := time.Now().Truncate(time.Millisecond)

	testJobs := []*jobpkg.Job{
		{
			ID:             "job-1",
			TargetURL:      "https://example.com/webhook",
			Payload:        []byte(`{"event":"payment.created"}`),
			IdempotencyKey: "key-1",
			Status:         jobpkg.StatusPending,
			Attempts:       0,
			MaxAttempts:    5,
			NextAttemptAt:  now.Add(-time.Minute),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "job-2",
			TargetURL:      "https://example.com/webhook",
			Payload:        []byte(`{"event":"payment.failed"}`),
			IdempotencyKey: "key-2",
			Status:         jobpkg.StatusPending,
			Attempts:       0,
			MaxAttempts:    5,
			NextAttemptAt:  now.Add(time.Hour),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "job-3",
			TargetURL:      "https://example.com/webhook",
			Payload:        []byte(`{"event":"payment.updated"}`),
			IdempotencyKey: "key-3",
			Status:         jobpkg.StatusPending,
			Attempts:       1,
			MaxAttempts:    5,
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "job-4",
			TargetURL:      "https://example.com/webhook",
			Payload:        []byte(`{"event":"payment.deleted"}`),
			IdempotencyKey: "key-4",
			Status:         jobpkg.StatusPending,
			Attempts:       1,
			MaxAttempts:    5,
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}

	for _, testJob := range testJobs {
		if err := repo.Create(ctx, testJob); err != nil {
			t.Fatalf("failed to create test job %q: %v", testJob.ID, err)
		}
	}

	_, err = db.WriteDB.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, locked_until = ?
		WHERE id = ?
	`, jobpkg.StatusProcessing, now.Add(-time.Minute).UnixMilli(), "job-3")
	if err != nil {
		t.Fatalf("failed to set expired lease: %v", err)
	}

	_, err = db.WriteDB.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, locked_until = ?
		WHERE id = ?
	`, jobpkg.StatusProcessing, now.Add(time.Minute).UnixMilli(), "job-4")
	if err != nil {
		t.Fatalf("failed to set active lease: %v", err)
	}

	claimedJobs, err := repo.Claim(ctx, 2)
	if err != nil {
		t.Fatalf("failed to claim jobs: %v", err)
	}

	if len(claimedJobs) != 2 {
		t.Fatalf("expected 2 claimed jobs, got %d", len(claimedJobs))
	}

	claimedJobIDs := make(map[string]bool)

	for _, claimedJob := range claimedJobs {
		claimedJobIDs[claimedJob.ID] = true

		if claimedJob.Status != jobpkg.StatusProcessing {
			t.Errorf("expected status %q, got %q for job %q",
				jobpkg.StatusProcessing, claimedJob.Status, claimedJob.ID)
		}

		if claimedJob.LockedUntil == nil {
			t.Errorf("expected LockedUntil to be set for job %q", claimedJob.ID)
			continue
		}

		if time.Until(*claimedJob.LockedUntil) <= 0 {
			t.Errorf("expected future lease for job %q", claimedJob.ID)
		}
	}

	if !claimedJobIDs["job-1"] {
		t.Error("expected due pending job-1 to be claimed")
	}

	if !claimedJobIDs["job-3"] {
		t.Error("expected expired processing job-3 to be reclaimed")
	}

	if claimedJobIDs["job-2"] {
		t.Error("future pending job-2 should not be claimed")
	}

	if claimedJobIDs["job-4"] {
		t.Error("active processing job-4 should not be claimed")
	}

	claimedJobs, err = repo.Claim(ctx, 2)
	if err != nil {
		t.Fatalf("failed to claim jobs again: %v", err)
	}

	if len(claimedJobs) != 0 {
		t.Errorf("expected 0 jobs on second claim, got %d", len(claimedJobs))
	}

	claimedJobs, err = repo.Claim(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error for zero limit: %v", err)
	}

	if len(claimedJobs) != 0 {
		t.Errorf("expected 0 jobs for zero limit, got %d", len(claimedJobs))
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	db, err := databasepkg.NewSQLite(ctx, databasepkg.Config{
		Path:                filepath.Join(t.TempDir(), "test.db"),
		MaxReadConnections:  4,
		MaxWriteConnections: 1,
		BusyTimeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	_, err = db.WriteDB.ExecContext(ctx, `
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY NOT NULL,
			target_url TEXT NOT NULL,
			payload BLOB NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter')),
			attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
			max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
			next_attempt_at INTEGER NOT NULL,
			locked_until INTEGER,
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (attempts <= max_attempts)
		)
	`)
	if err != nil {
		t.Fatalf("failed to create jobs table: %v", err)
	}

	repo := NewJobRepository(db)
	now := time.Now().Truncate(time.Millisecond)

	validJob := &jobpkg.Job{
		ID:             "job-1",
		TargetURL:      "https://example.com/webhook",
		Payload:        []byte(`{"event":"payment.created"}`),
		IdempotencyKey: "key-1",
		Status:         jobpkg.StatusPending,
		Attempts:       0,
		MaxAttempts:    5,
		NextAttemptAt:  now,
		LastError:      "",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := repo.Create(ctx, validJob); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	lockedTime := now.Add(time.Minute)

	updatedJob := *validJob
	updatedJob.TargetURL = "https://example.com/updated"
	updatedJob.Payload = []byte(`{"event":"payment.updated"}`)
	updatedJob.Status = jobpkg.StatusProcessing
	updatedJob.Attempts = 1
	updatedJob.NextAttemptAt = now.Add(time.Minute)
	updatedJob.LockedUntil = &lockedTime
	updatedJob.LastError = "previous attempt failed"
	updatedJob.UpdatedAt = now.Add(time.Minute)

	testCases := []struct {
		name    string
		job     *jobpkg.Job
		wantErr bool
	}{
		{
			name:    "successful update",
			job:     &updatedJob,
			wantErr: false,
		},
		{
			name:    "nil job",
			job:     nil,
			wantErr: true,
		},
		{
			name: "invalid job",
			job: &jobpkg.Job{
				ID: "",
			},
			wantErr: true,
		},
		{
			name: "non-existent job",
			job: &jobpkg.Job{
				ID:             "job-999",
				TargetURL:      "https://example.com/webhook",
				Payload:        []byte(`{"event":"payment.failed"}`),
				IdempotencyKey: "key-999",
				Status:         jobpkg.StatusPending,
				Attempts:       0,
				MaxAttempts:    5,
				NextAttemptAt:  now,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := repo.Update(ctx, testCase.job)

			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := repo.GetByID(ctx, validJob.ID)
			if err != nil {
				t.Fatalf("failed to retrieve updated job: %v", err)
			}

			if got.TargetURL != updatedJob.TargetURL {
				t.Errorf("expected target URL %q, got %q",
					updatedJob.TargetURL, got.TargetURL)
			}

			if string(got.Payload) != string(updatedJob.Payload) {
				t.Errorf("expected payload %q, got %q",
					updatedJob.Payload, got.Payload)
			}

			if got.Status != updatedJob.Status {
				t.Errorf("expected status %q, got %q",
					updatedJob.Status, got.Status)
			}

			if got.Attempts != updatedJob.Attempts {
				t.Errorf("expected attempts %d, got %d",
					updatedJob.Attempts, got.Attempts)
			}

			if got.MaxAttempts != updatedJob.MaxAttempts {
				t.Errorf("expected max attempts %d, got %d",
					updatedJob.MaxAttempts, got.MaxAttempts)
			}

			if !got.NextAttemptAt.Equal(updatedJob.NextAttemptAt) {
				t.Errorf("expected next attempt at %v, got %v",
					updatedJob.NextAttemptAt, got.NextAttemptAt)
			}

			if got.LockedUntil == nil {
				t.Fatal("expected LockedUntil to be non-nil")
			}

			if !got.LockedUntil.Equal(*updatedJob.LockedUntil) {
				t.Errorf("expected LockedUntil %v, got %v",
					*updatedJob.LockedUntil, *got.LockedUntil)
			}

			if got.LastError != updatedJob.LastError {
				t.Errorf("expected last error %q, got %q",
					updatedJob.LastError, got.LastError)
			}

			if !got.CreatedAt.Equal(validJob.CreatedAt) {
				t.Errorf("expected created at %v, got %v",
					validJob.CreatedAt, got.CreatedAt)
			}

			if !got.UpdatedAt.Equal(updatedJob.UpdatedAt) {
				t.Errorf("expected updated at %v, got %v",
					updatedJob.UpdatedAt, got.UpdatedAt)
			}

			if got.IdempotencyKey != validJob.IdempotencyKey {
				t.Errorf("expected idempotency key %q, got %q",
					validJob.IdempotencyKey, got.IdempotencyKey)
			}
		})
	}
}

func TestUpdateDatabaseErrors(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name          string
		setupMock     func(sqlmock.Sqlmock)
		expectedError string
	}{
		{
			name: "update execution error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("UPDATE jobs").
					WillReturnError(errors.New("database failure"))
			},
			expectedError: "failed to update job",
		},
		{
			name: "rows affected error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("UPDATE jobs").
					WillReturnResult(
						sqlmock.NewErrorResult(errors.New("rows affected failure")),
					)
			},
			expectedError: "failed to check updated job",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create sqlmock database: %v", err)
			}
			defer mockDB.Close()

			database := &databasepkg.SQLite{
				WriteDB: mockDB,
			}

			repository := NewJobRepository(database)

			testCase.setupMock(mock)

			now := time.Now()

			testJob := &jobpkg.Job{
				ID:             "test-job",
				TargetURL:      "https://example.com/webhook",
				Payload:        []byte(`{"event":"test"}`),
				IdempotencyKey: "test-key",
				Status:         jobpkg.StatusPending,
				Attempts:       0,
				MaxAttempts:    3,
				NextAttemptAt:  now,
				CreatedAt:      now,
				UpdatedAt:      now,
			}

			err = repository.Update(ctx, testJob)

			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), testCase.expectedError) {
				t.Fatalf(
					"expected error containing %q, got %q",
					testCase.expectedError,
					err.Error(),
				)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}

func TestClaimDatabaseErrors(t *testing.T) {
	ctx := context.Background()

	columns := []string{
		"id",
		"target_url",
		"payload",
		"idempotency_key",
		"status",
		"attempts",
		"max_attempts",
		"next_attempt_at",
		"locked_until",
		"last_error",
		"created_at",
		"updated_at",
	}

	testCases := []struct {
		name          string
		setupMock     func(sqlmock.Sqlmock)
		expectedError string
	}{
		{
			name: "claim query error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("UPDATE jobs").
					WillReturnError(errors.New("database query failure"))
			},
			expectedError: "failed to claim jobs",
		},
		{
			name: "scan claimed job error",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(columns).
					AddRow(
						"job-1",
						"https://example.com/webhook",
						[]byte(`{"event":"test"}`),
						"key-1",
						"processing",
						"invalid-attempts",
						3,
						time.Now().UnixMilli(),
						nil,
						"",
						time.Now().UnixMilli(),
						time.Now().UnixMilli(),
					)

				mock.ExpectQuery("UPDATE jobs").
					WillReturnRows(rows)
			},
			expectedError: "failed to scan claimed job",
		},
		{
			name: "iterate claimed jobs error",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(columns).
					AddRow(
						"job-1",
						"https://example.com/webhook",
						[]byte(`{"event":"test"}`),
						"key-1",
						"processing",
						1,
						3,
						time.Now().UnixMilli(),
						nil,
						"",
						time.Now().UnixMilli(),
						time.Now().UnixMilli(),
					).
					RowError(0, errors.New("row iteration failure"))

				mock.ExpectQuery("UPDATE jobs").
					WillReturnRows(rows)
			},
			expectedError: "failed to iterate claimed jobs",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create sqlmock database: %v", err)
			}
			defer mockDB.Close()

			database := &databasepkg.SQLite{
				WriteDB: mockDB,
			}

			repository := NewJobRepository(database)

			testCase.setupMock(mock)

			_, err = repository.Claim(ctx, 1)

			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if !strings.Contains(err.Error(), testCase.expectedError) {
				t.Fatalf(
					"expected error containing %q, got %q",
					testCase.expectedError,
					err.Error(),
				)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unfulfilled mock expectations: %v", err)
			}
		})
	}
}
