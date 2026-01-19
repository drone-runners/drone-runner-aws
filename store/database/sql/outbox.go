package sql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Ensure OutboxStore implements store.OutboxStore interface.
var _ store.OutboxStore = (*outboxStore)(nil)

type outboxStore struct {
	db *sqlx.DB
}

// NewOutboxStore returns a new OutboxStore.
func NewOutboxStore(db *sqlx.DB) store.OutboxStore {
	return &outboxStore{db: db}
}

// Create creates a new outbox job.
func (s *outboxStore) Create(ctx context.Context, job *types.OutboxJob) error {
	now := time.Now().Unix()
	query := squirrel.Insert("outbox_jobs").
		Columns(
			"pool_name",
			"runner_name",
			"job_type",
			"job_params",
			"status",
			"created_at",
		).
		Values(
			job.PoolName,
			job.RunnerName,
			job.JobType,
			job.JobParams,
			types.OutboxJobStatusPending,
			now,
		).
		Suffix("RETURNING id").
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	if err := query.QueryRowContext(ctx).Scan(&job.ID); err != nil {
		return err
	}
	return nil
}

// FindAndClaimPending finds and claims pending jobs.
// If runnerName is non-empty, only jobs matching that runner_name are returned.
// If runnerName is empty, only jobs with empty runner_name are returned (global jobs).
func (s *outboxStore) FindAndClaimPending(ctx context.Context, runnerName string, jobTypes []types.OutboxJobType, limit int, retryInterval time.Duration) ([]*types.OutboxJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}
	defer tx.Rollback() //nolint

	// Build subquery (CTE) to find candidate jobs
	subQuery := squirrel.Select("id AS job_id").
		From("outbox_jobs").
		Where(squirrel.And{
			squirrel.Eq{"runner_name": runnerName},
			squirrel.Eq{"status": types.OutboxJobStatusPending},
			squirrel.Eq{"job_type": jobTypes},
			squirrel.Or{
				squirrel.Eq{"processed_at": nil},
				squirrel.Expr("processed_at < extract(epoch FROM now() - make_interval(mins := ?))", int(retryInterval.Minutes())),
			},
		}).
		Limit(uint64(limit)).
		Suffix("FOR UPDATE SKIP LOCKED")

	// Convert subquery to SQL + args
	subSQL, subArgs, err := subQuery.PlaceholderFormat(squirrel.Dollar).ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build CTE subquery: %w", err)
	}

	// Calculate the next placeholder number based on how many args were generated
	// (jobTypes slice expands to multiple placeholders)
	nextPlaceholder := len(subArgs) + 1

	// Build final CTE UPDATE SQL
	//nolint: gosec
	finalSQL := fmt.Sprintf(`
WITH candidate AS (
	%s
)
UPDATE outbox_jobs
SET status = $%d,
	processed_at = extract(epoch FROM now()),
	retry_count = retry_count + 1
FROM candidate
WHERE outbox_jobs.id = candidate.job_id
RETURNING id, pool_name, runner_name, job_type, job_params, created_at, processed_at, status, error_message, retry_count
`, subSQL, nextPlaceholder)

	// Append status to args
	subArgs = append(subArgs, types.OutboxJobStatusRunning)

	// Execute and scan results
	rows, err := tx.QueryContext(ctx, finalSQL, subArgs...)
	if err != nil {
		return nil, fmt.Errorf("error executing update: %w", err)
	}
	defer rows.Close()

	var jobs []*types.OutboxJob
	for rows.Next() {
		job := new(types.OutboxJob)
		scanErr := rows.Scan(
			&job.ID,
			&job.PoolName,
			&job.RunnerName,
			&job.JobType,
			&job.JobParams,
			&job.CreatedAt,
			&job.ProcessedAt,
			&job.Status,
			&job.ErrorMessage,
			&job.RetryCount,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("error scanning job: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing transaction: %w", err)
	}

	return jobs, nil
}

// UpdateStatus updates the status of an outbox job.
func (s *outboxStore) UpdateStatus(ctx context.Context, id int64, status types.OutboxJobStatus, errorMessage string) error {
	query := squirrel.Update("outbox_jobs").
		Set("status", status).
		Set("error_message", errorMessage)

	query = query.Set("processed_at", squirrel.Expr("extract(epoch FROM now())")).
		Where(squirrel.Eq{"id": id}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	result, err := query.ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("error updating outbox job status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// Delete deletes an outbox job.
func (s *outboxStore) Delete(ctx context.Context, id int64) error {
	query := squirrel.Delete("outbox_jobs").
		Where(squirrel.Eq{"id": id}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	result, err := query.ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("error deleting outbox job: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// DeleteOlderThan deletes jobs older than the given timestamp and returns number of jobs deleted
func (s *outboxStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	query := squirrel.Delete("outbox_jobs").
		Where(squirrel.Lt{"created_at": timestamp}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	result, err := query.ExecContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deleting old jobs: %w", err)
	}

	return result.RowsAffected()
}

// FindScaleJobForWindow checks if a scale job already exists for the given pool and window.
// It looks for jobs with job_type='scale' where pool_name and job_params->>'window_start' match.
func (s *outboxStore) FindScaleJobForWindow(ctx context.Context, poolName string, windowStart int64) (*types.OutboxJob, error) {
	query := squirrel.Select(
		"id", "pool_name", "runner_name", "job_type", "job_params",
		"created_at", "processed_at", "status", "error_message", "retry_count",
	).
		From("outbox_jobs").
		Where(squirrel.And{
			squirrel.Eq{"job_type": types.OutboxJobTypeScale},
			squirrel.Eq{"pool_name": poolName},
			squirrel.Expr("(job_params->>'window_start')::bigint = ?", windowStart),
		}).
		Limit(1).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	job := new(types.OutboxJob)
	err := query.QueryRowContext(ctx).Scan(
		&job.ID,
		&job.PoolName,
		&job.RunnerName,
		&job.JobType,
		&job.JobParams,
		&job.CreatedAt,
		&job.ProcessedAt,
		&job.Status,
		&job.ErrorMessage,
		&job.RetryCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No job found for this pool and window
		}
		return nil, fmt.Errorf("error finding scale job for window: %w", err)
	}

	return job, nil
}
