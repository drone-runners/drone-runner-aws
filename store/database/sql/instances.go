package sql

import (
	"context"
	"fmt"
	"strings"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
)

var _ store.InstanceStore = (*InstanceStore)(nil)
var builder = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

func NewInstanceStore(db *sqlx.DB) *InstanceStore {
	return &InstanceStore{db}
}

type InstanceStore struct {
	db *sqlx.DB
}

func (s InstanceStore) Find(_ context.Context, id string) (*types.Instance, error) {
	dst := new(types.Instance)
	err := s.db.Get(dst, instanceFindByID, id)
	return dst, err
}

func (s InstanceStore) List(_ context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
	dst := []*types.Instance{}
	var args []interface{}

	stmt := builder.Select(instanceColumns).From("instances")

	if pool != "" {
		stmt = stmt.Where(squirrel.Eq{"instance_pool": pool})
		args = append(args, pool)
	}

	if params != nil {
		if params.Stage != "" {
			stmt = stmt.Where(squirrel.Eq{"instance_stage": params.Stage})
			args = append(args, params.Stage)
		}
		if params.Status != "" {
			stmt = stmt.Where(squirrel.Eq{"instance_state": params.Status})
			args = append(args, params.Status)
		}
		if params.RunnerName != "" {
			stmt = stmt.Where(squirrel.Eq{"runner_name": params.RunnerName})
			args = append(args, params.RunnerName)
		}
		for key, value := range params.MatchLabels {
			condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
			stmt = stmt.Where(condition)
			args = append(args, key, value)
		}
	}
	stmt = stmt.OrderBy("instance_started " + "ASC")
	sql, _, _ := stmt.ToSql()
	var err = s.db.Select(&dst, sql, args...)
	return dst, err
}

func (s InstanceStore) Create(_ context.Context, instance *types.Instance) error {
	query, arg, err := s.db.BindNamed(instanceInsert, instance)
	if err != nil {
		return err
	}
	return s.db.QueryRow(query, arg...).Scan(&instance.ID)
}

func (s InstanceStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if _, err := tx.Exec(instanceDelete, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s InstanceStore) DeleteAndReturn(ctx context.Context, query string, args ...any) ([]*types.Instance, error) {
	dst := []*types.Instance{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var deletedRow types.Instance
		err := rows.Scan(&deletedRow.ID, &deletedRow.Name, &deletedRow.NodeID, &deletedRow.RunnerName)
		if err != nil {
			tx.Rollback() //nolint
			return nil, err
		}
		dst = append(dst, &deletedRow)
	}
	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return dst, nil
}

func (s InstanceStore) Update(_ context.Context, instance *types.Instance) error {
	query, arg, err := s.db.BindNamed(instanceUpdate, instance)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(query, arg...)
	return err
}

func (s InstanceStore) FindAndClaim(
	ctx context.Context,
	params *types.QueryParams,
	newState types.InstanceState,
	allowedStates []types.InstanceState,
	updateStartTime bool,
) (*types.Instance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint

	// --- Build subquery (CTE) ---
	subQuery := builder.Select("instance_id AS inst_id").
		From("instances").
		Where(squirrel.Eq{"instance_pool": params.PoolName})

	if params.RunnerName != "" {
		subQuery = subQuery.Where(squirrel.Eq{"runner_name": params.RunnerName})
	}

	if params.InstanceID != "" {
		subQuery = subQuery.Where(squirrel.Eq{"instance_id": params.InstanceID})
	}

	if params.ImageName != "" {
		subQuery = subQuery.Where(squirrel.Eq{"instance_image": params.ImageName})
	}

	if params.MachineType != "" {
		subQuery = subQuery.Where(squirrel.Eq{"instance_size": params.MachineType})
	}

	if params.NestedVirtualization {
		subQuery = subQuery.Where(squirrel.Eq{"enable_nested_virtualization": true})
	}

	if len(allowedStates) > 0 {
		stateVals := make([]interface{}, len(allowedStates))
		for i, state := range allowedStates {
			stateVals[i] = state
		}
		subQuery = subQuery.Where(squirrel.Eq{"instance_state": stateVals})
	}

	subQuery = subQuery.OrderBy("instance_started ASC").Limit(1).Suffix("FOR UPDATE SKIP LOCKED")

	// --- Convert subquery to SQL + args ---
	subSQL, subArgs, err := subQuery.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build CTE subquery: %w", err)
	}

	// --- Shift placeholders in subquery to start after $1 (newState) ---
	for i := len(subArgs); i > 0; i-- {
		oldPlaceholder := fmt.Sprintf("$%d", i)
		newPlaceholder := fmt.Sprintf("$%d", i+1)
		subSQL = strings.ReplaceAll(subSQL, oldPlaceholder, newPlaceholder)
	}

	// --- Clean RETURNING columns ---
	cleanColumns := strings.ReplaceAll(instanceColumns, "\n", "")
	cleanColumns = strings.TrimSpace(cleanColumns)

	// --- Build final CTE UPDATE SQL ---
	//nolint: gosec,mnd
	finalSQL := fmt.Sprintf(`
WITH candidate AS (
    %s
)
UPDATE instances
SET instance_state = $1,
    instance_updated = extract(epoch FROM now()),
    instance_started = CASE WHEN $%d THEN extract(epoch FROM now()) ELSE instance_started END
FROM candidate
WHERE instances.instance_id = candidate.inst_id
RETURNING %s
`, subSQL, len(subArgs)+2, cleanColumns)

	// --- Combine args: newState first, then subquery args, then updateStartTime ---
	args := append([]interface{}{newState}, append(subArgs, updateStartTime)...)

	// --- Execute ---
	dst := new(types.Instance)
	err = tx.QueryRowContext(ctx, finalSQL, args...).Scan(
		&dst.Name, &dst.ID, &dst.NodeID, &dst.Address, &dst.Provider,
		&dst.State, &dst.Pool, &dst.Image, &dst.Region, &dst.Zone,
		&dst.Size, &dst.OS, &dst.Arch, &dst.Variant, &dst.Version,
		&dst.OSName, &dst.Stage, &dst.CAKey, &dst.CACert, &dst.TLSKey,
		&dst.TLSCert, &dst.Started, &dst.Updated, &dst.IsHibernated,
		&dst.Port, &dst.OwnerID, &dst.StorageIdentifier, &dst.Labels,
		&dst.EnableNestedVirtualization, &dst.RunnerName, &dst.VariantID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return dst, nil
}

func (s InstanceStore) Purge(ctx context.Context) error {
	panic("implement me")
}

const instanceColumns = `
 instance_name
,instance_id
,instance_node_id
,instance_address
,instance_provider
,instance_state
,instance_pool
,instance_image
,instance_region
,instance_zone
,instance_size
,instance_os
,instance_arch
,instance_variant
,instance_version
,instance_os_name
,instance_stage
,instance_ca_key
,instance_ca_cert
,instance_tls_key
,instance_tls_cert
,instance_started
,instance_updated
,is_hibernated
,instance_port
,instance_owner_id
,instance_storage_identifier
,instance_labels
,enable_nested_virtualization
,runner_name
,variant_id
`

const instanceFindByID = `SELECT ` + instanceColumns + `
FROM instances
WHERE instance_id = $1
`

const instanceInsert = `
INSERT INTO instances (
 instance_id
,instance_node_id
,instance_name
,instance_address
,instance_provider
,instance_state
,instance_pool
,instance_image
,instance_region
,instance_zone
,instance_size
,instance_os
,instance_arch
,instance_variant
,instance_version
,instance_os_name
,instance_stage
,instance_ca_key
,instance_ca_cert
,instance_tls_key
,instance_tls_cert
,instance_started
,instance_updated
,is_hibernated
,instance_port
,instance_owner_id
,runner_name
,instance_storage_identifier
,instance_labels
,enable_nested_virtualization
,variant_id
) values (
 :instance_id
,:instance_node_id
,:instance_name
,:instance_address
,:instance_provider
,:instance_state
,:instance_pool
,:instance_image
,:instance_region
,:instance_zone
,:instance_size
,:instance_os
,:instance_arch
,:instance_variant
,:instance_version
,:instance_os_name
,:instance_stage
,:instance_ca_key
,:instance_ca_cert
,:instance_tls_key
,:instance_tls_cert
,:instance_started
,:instance_updated
,:is_hibernated
,:instance_port
,:instance_owner_id
,:runner_name
,:instance_storage_identifier
,:instance_labels
,:enable_nested_virtualization
,:variant_id
) RETURNING instance_id
`

const instanceDelete = `
DELETE FROM instances
WHERE instance_id = $1
`

const instanceUpdate = `
UPDATE instances
SET
  instance_state    = :instance_state
 ,instance_stage	= :instance_stage
 ,instance_updated  = :instance_updated
 ,is_hibernated 	= :is_hibernated
 ,instance_address  = :instance_address
 ,instance_owner_id = :instance_owner_id
 ,instance_started  = :instance_started
WHERE instance_id   = :instance_id
`
