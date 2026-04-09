
// retryFirewallStore wraps a FirewallStore with retry logic.
type retryFirewallStore struct {
	inner store.FirewallStore
}

// NewRetryFirewallStore wraps the given FirewallStore with transient-error retry logic.
func NewRetryFirewallStore(inner store.FirewallStore) store.FirewallStore {
	return &retryFirewallStore{inner: inner}
}

func (s *retryFirewallStore) CreateBatch(ctx context.Context, rules []*types.FirewallRule) error {
	return RetryVoid(func() error {
		return s.inner.CreateBatch(ctx, rules)
	})
}

func (s *retryFirewallStore) ListByStageID(ctx context.Context, stageID string) ([]*types.FirewallRule, error) {
	return Retry(func() ([]*types.FirewallRule, error) {
		return s.inner.ListByStageID(ctx, stageID)
	})
}

func (s *retryFirewallStore) DeleteByStageID(ctx context.Context, stageID string) error {
	return RetryVoid(func() error {
		return s.inner.DeleteByStageID(ctx, stageID)
	})
}
