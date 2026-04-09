package sql

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
)

var _ store.FirewallStore = (*FirewallStore)(nil)

func NewFirewallStore(db *sqlx.DB) *FirewallStore {
	return &FirewallStore{db}
}

type FirewallStore struct {
	db *sqlx.DB
}

func (s FirewallStore) CreateBatch(_ context.Context, rules []*types.FirewallRule) error {
	if len(rules) == 0 {
		return nil
	}

	q := builder.Insert("firewall_rules").
		Columns("stage_id", "instance_id", "resource_id", "cloud_provider", "created_at")

	for _, r := range rules {
		q = q.Values(r.StageID, r.InstanceID, r.ResourceID, r.CloudProvider, r.CreatedAt)
	}

	query, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("firewall_rules: failed to build insert query: %w", err)
	}

	_, err = s.db.Exec(query, args...)
	return err
}

func (s FirewallStore) ListByStageID(_ context.Context, stageID string) ([]*types.FirewallRule, error) {
	var dst []*types.FirewallRule

	query, args, err := builder.Select(firewallRuleColumns).
		From("firewall_rules").
		Where(squirrel.Eq{"stage_id": stageID}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("firewall_rules: failed to build select query: %w", err)
	}

	err = s.db.Select(&dst, query, args...)
	return dst, err
}

func (s FirewallStore) DeleteByStageID(ctx context.Context, stageID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint

	query, args, err := builder.Delete("firewall_rules").
		Where(squirrel.Eq{"stage_id": stageID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("firewall_rules: failed to build delete query: %w", err)
	}

	if _, err := tx.Exec(query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

const firewallRuleColumns = `id, stage_id, instance_id, resource_id, cloud_provider, created_at`
