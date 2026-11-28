package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	databasepkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/database"
	jobpkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/job"
	repositorypkg "github.com/deepesh-kumar-pandey/webhook-engine/internal/repository"
)

const leaseDuration = 30 * time.Second

type JobRepository struct {
	db *databasepkg.SQLite
}

var _ repositorypkg.JobRepository = (*JobRepository)(nil)

func NewJobRepository(db *databasepkg.SQLite) *JobRepository {
	return &JobRepository{
		db: db,
	}
}

func (r *JobRepository) Create(ctx context.Context, job *jobpkg.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}

	_, err := r.db.WriteDB.ExecContext(
		ctx,
		`INSERT INTO jobs (
			id,
			target_url,
			payload,
			idempotency_key,
			status,
			attempts,
			max_attempts,
			next_attempt_at,
			locked_until,
			last_error,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID,
		job.TargetURL,
		job.Payload,
		job.IdempotencyKey,
		job.Status,
		job.Attempts,
		job.MaxAttempts,
		job.NextAttemptAt.UnixMilli(),
		nil,
		job.LastError,
		job.CreatedAt.UnixMilli(),
		job.UpdatedAt.UnixMilli(),
	)

	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	return nil
}

func (r *JobRepository) GetByID(ctx context.Context, id string) (*jobpkg.Job, error) {
	var job jobpkg.Job

	var nextAttemptAt int64
	var lockedUntil sql.NullInt64
	var createdAt int64
	var updatedAt int64

	err := r.db.ReadDB.QueryRowContext(
		ctx,
		`SELECT
			id,
			target_url,
			payload,
			idempotency_key,
			status,
			attempts,
			max_attempts,
			next_attempt_at,
			locked_until,
			last_error,
			created_at,
			updated_at
		FROM jobs
		WHERE id = ?`,
		id,
	).Scan(
		&job.ID,
		&job.TargetURL,
		&job.Payload,
		&job.IdempotencyKey,
		&job.Status,
		&job.Attempts,
		&job.MaxAttempts,
		&nextAttemptAt,
		&lockedUntil,
		&job.LastError,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get job by ID: %w", err)
	}

	job.NextAttemptAt = time.UnixMilli(nextAttemptAt)
	job.CreatedAt = time.UnixMilli(createdAt)
	job.UpdatedAt = time.UnixMilli(updatedAt)

	if lockedUntil.Valid {
		lockedTime := time.UnixMilli(lockedUntil.Int64)
		job.LockedUntil = &lockedTime
	}

	return &job, nil
}

func (r *JobRepository) GetByIdempotencyKey(ctx context.Context, key string) (*jobpkg.Job, error) {
	var job jobpkg.Job

	var nextAttemptAt int64
	var lockedUntil sql.NullInt64
	var createdAt int64
	var updatedAt int64

	err := r.db.ReadDB.QueryRowContext(
		ctx,
		`SELECT
			id,
			target_url,
			payload,
			idempotency_key,
			status,
			attempts,
			max_attempts,
			next_attempt_at,
			locked_until,
			last_error,
			created_at,
			updated_at
		FROM jobs
		WHERE idempotency_key = ?`,
		key,
	).Scan(
		&job.ID,
		&job.TargetURL,
		&job.Payload,
		&job.IdempotencyKey,
		&job.Status,
		&job.Attempts,
		&job.MaxAttempts,
		&nextAttemptAt,
		&lockedUntil,
		&job.LastError,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get job by idempotency key: %w", err)
	}

	job.NextAttemptAt = time.UnixMilli(nextAttemptAt)
	job.CreatedAt = time.UnixMilli(createdAt)
	job.UpdatedAt = time.UnixMilli(updatedAt)

	if lockedUntil.Valid {
		lockedTime := time.UnixMilli(lockedUntil.Int64)
		job.LockedUntil = &lockedTime
	}

	return &job, nil
}

func (r *JobRepository) Claim(ctx context.Context, limit int) ([]*jobpkg.Job, error) {
	if limit <= 0 {
		return []*jobpkg.Job{}, nil
	}

	now := time.Now()
	lockedUntilTime := now.Add(leaseDuration)

	nowTimestamp := now.UnixMilli()
	lockedUntilTimestamp := lockedUntilTime.UnixMilli()

	rows, err := r.db.WriteDB.QueryContext(
		ctx,
		`UPDATE jobs
		SET
			status = ?,
			locked_until = ?,
			updated_at = ?
		WHERE id IN (
			SELECT id
			FROM jobs
			WHERE
				(status = ? AND next_attempt_at <= ?)
				OR
				(status = ? AND locked_until <= ?)
			ORDER BY next_attempt_at, created_at
			LIMIT ?
		)
		RETURNING
			id,
			target_url,
			payload,
			idempotency_key,
			status,
			attempts,
			max_attempts,
			next_attempt_at,
			locked_until,
			last_error,
			created_at,
			updated_at`,
		jobpkg.StatusProcessing,
		lockedUntilTimestamp,
		nowTimestamp,
		jobpkg.StatusPending,
		nowTimestamp,
		jobpkg.StatusProcessing,
		nowTimestamp,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to claim jobs: %w", err)
	}
	defer rows.Close()

	claimedJobs := make([]*jobpkg.Job, 0, limit)

	for rows.Next() {
		var job jobpkg.Job

		var nextAttemptAt int64
		var lockedUntil sql.NullInt64
		var createdAt int64
		var updatedAt int64

		err := rows.Scan(
			&job.ID,
			&job.TargetURL,
			&job.Payload,
			&job.IdempotencyKey,
			&job.Status,
			&job.Attempts,
			&job.MaxAttempts,
			&nextAttemptAt,
			&lockedUntil,
			&job.LastError,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan claimed job: %w", err)
		}

		job.NextAttemptAt = time.UnixMilli(nextAttemptAt)
		job.CreatedAt = time.UnixMilli(createdAt)
		job.UpdatedAt = time.UnixMilli(updatedAt)

		if lockedUntil.Valid {
			lockedTime := time.UnixMilli(lockedUntil.Int64)
			job.LockedUntil = &lockedTime
		}

		claimedJobs = append(claimedJobs, &job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate claimed jobs: %w", err)
	}

	return claimedJobs, nil
}

func (r *JobRepository) Update(ctx context.Context, job *jobpkg.Job) error {
	if job == nil {
		return fmt.Errorf("job cannot be nil")
	}

	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}

	result, err := r.db.WriteDB.ExecContext(
		ctx,
		`UPDATE jobs
		SET
			target_url = ?,
			payload = ?,
			status = ?,
			attempts = ?,
			max_attempts = ?,
			next_attempt_at = ?,
			locked_until = ?,
			last_error = ?,
			updated_at = ?
		WHERE id = ?`,
		job.TargetURL,
		job.Payload,
		job.Status,
		job.Attempts,
		job.MaxAttempts,
		job.NextAttemptAt.UnixMilli(),
		nullableTimestamp(job.LockedUntil),
		job.LastError,
		job.UpdatedAt.UnixMilli(),
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check updated job: %w", err)
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func nullableTimestamp(timestamp *time.Time) interface{} {
	if timestamp == nil {
		return nil
	}

	return timestamp.UnixMilli()
}
