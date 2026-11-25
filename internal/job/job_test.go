package job

import (
	"testing"
	"time"
)

func TestIsValidStatus(t *testing.T) {
	testCases := []struct {
		name   string
		status Status
		want   bool
	}{
		{"pending status", StatusPending, true},
		{"processing status", StatusProcessing, true},
		{"succeeded status", StatusSucceeded, true},
		{"dead letter status", StatusDeadLetter, true},
		{"invalid status", Status("unknown"), false},
		{"empty status", Status(""), false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := IsValidStatus(testCase.status)

			if got != testCase.want {
				t.Errorf("IsValidStatus(%q) = %v, want %v",
					testCase.status, got, testCase.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name    string
		job     *Job
		wantErr bool
	}{
		{
			name: "valid job",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com/webhook",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				Attempts:       0,
				MaxAttempts:    5,
			},
			wantErr: false,
		},
		{
			name:    "nil job",
			job:     nil,
			wantErr: true,
		},
		{
			name: "empty job ID",
			job: &Job{
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "empty idempotency key",
			job: &Job{
				ID:          "job-1",
				TargetURL:   "https://example.com",
				Status:      StatusPending,
				MaxAttempts: 5,
			},
			wantErr: true,
		},
		{
			name: "empty target URL",
			job: &Job{
				ID:             "job-1",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "malformed URL",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "://invalid-url",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "unsupported URL scheme",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "ftp://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "URL without host",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https:///webhook",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "invalid status",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         Status("unknown"),
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "negative attempts",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				Attempts:       -1,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "zero max attempts",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				Attempts:       0,
				MaxAttempts:    0,
			},
			wantErr: true,
		},
		{
			name: "attempts exceed max attempts",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				Attempts:       6,
				MaxAttempts:    5,
			},
			wantErr: true,
		},
		{
			name: "attempts equal max attempts",
			job: &Job{
				ID:             "job-1",
				TargetURL:      "https://example.com",
				IdempotencyKey: "key-1",
				Status:         StatusPending,
				Attempts:       5,
				MaxAttempts:    5,
			},
			wantErr: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.job.Validate()

			if (err != nil) != testCase.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v",
					err, testCase.wantErr)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	testCases := []struct {
		name   string
		status Status
		want   bool
	}{
		{"pending job", StatusPending, false},
		{"processing job", StatusProcessing, false},
		{"succeeded job", StatusSucceeded, true},
		{"dead letter job", StatusDeadLetter, true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			job := &Job{
				Status: testCase.status,
			}

			got := job.IsTerminal()

			if got != testCase.want {
				t.Errorf("IsTerminal() = %v, want %v",
					got, testCase.want)
			}
		})
	}
}

func TestIsReady(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name          string
		status        Status
		nextAttemptAt time.Time
		want          bool
	}{
		{
			name:          "pending job ready in past",
			status:        StatusPending,
			nextAttemptAt: now.Add(-time.Minute),
			want:          true,
		},
		{
			name:          "pending job ready exactly now",
			status:        StatusPending,
			nextAttemptAt: now,
			want:          true,
		},
		{
			name:          "pending job scheduled in future",
			status:        StatusPending,
			nextAttemptAt: now.Add(time.Minute),
			want:          false,
		},
		{
			name:          "processing job",
			status:        StatusProcessing,
			nextAttemptAt: now.Add(-time.Minute),
			want:          false,
		},
		{
			name:          "succeeded job",
			status:        StatusSucceeded,
			nextAttemptAt: now.Add(-time.Minute),
			want:          false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			job := &Job{
				Status:        testCase.status,
				NextAttemptAt: testCase.nextAttemptAt,
			}

			got := job.IsReady(now)

			if got != testCase.want {
				t.Errorf("IsReady() = %v, want %v",
					got, testCase.want)
			}
		})
	}
}
