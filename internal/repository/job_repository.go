package repository

import (
	"context"

	"github.com/deepesh-kumar-pandey/webhook-engine/internal/job"
)

type JobRepository interface {
	Create(ctx context.Context, job *job.Job) error

	GetByID(ctx context.Context, id string) (*job.Job, error)

	GetByIdempotencyKey(ctx context.Context, key string) (*job.Job, error)

	Claim(ctx context.Context, limit int) ([]*job.Job, error)

	Update(ctx context.Context, job *job.Job) error
}
