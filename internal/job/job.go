package job

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusDeadLetter Status = "dead_letter"
)

type Job struct {
	ID             string
	TargetURL      string
	Payload        []byte
	IdempotencyKey string
	Status         Status
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LockedUntil    *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func IsValidStatus(status Status) bool {
	switch status {
	case StatusPending, StatusProcessing, StatusSucceeded, StatusDeadLetter:
		return true
	default:
		return false
	}
}

func (j *Job) Validate() error {
	if j == nil {
		return errors.New("job cannot be nil")
	}

	if strings.TrimSpace(j.ID) == "" {
		return errors.New("job ID cannot be empty")
	}

	if strings.TrimSpace(j.IdempotencyKey) == "" {
		return errors.New("idempotency key cannot be empty")
	}

	if strings.TrimSpace(j.TargetURL) == "" {
		return errors.New("target URL cannot be empty")
	}

	parsedURL, err := url.ParseRequestURI(j.TargetURL)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}

	if parsedURL.Host == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return errors.New("target URL must use HTTP or HTTPS")
	}

	if !IsValidStatus(j.Status) {
		return fmt.Errorf("invalid status: %s", j.Status)
	}

	if j.Attempts < 0 {
		return errors.New("attempts cannot be negative")
	}

	if j.MaxAttempts <= 0 {
		return errors.New("max attempts must be greater than zero")
	}

	if j.Attempts > j.MaxAttempts {
		return errors.New("attempts cannot exceed max attempts")
	}

	return nil
}

func (j *Job) IsTerminal() bool {
	return j.Status == StatusSucceeded || j.Status == StatusDeadLetter
}

func (j *Job) IsReady(now time.Time) bool {
	return j.Status == StatusPending &&
		!j.NextAttemptAt.After(now)
}
