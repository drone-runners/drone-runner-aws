package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const logFieldCapability = "capability"

// CapabilityScaler scales capability groups: one prediction per group, with instances placed
// across the group's pools. It also spills failed predictor setup jobs to the next pool.
type CapabilityScaler struct {
	manager         ScalerManager
	predictor       predictor.Predictor
	instanceStore   store.InstanceStore
	capabilityStore store.CapabilityHistoryStore
	outboxStore     store.OutboxStore
	catalog         *capability.Catalog
	config          types.ScalerConfig
	metrics         *metric.Metrics
	capacity        *capacityCache
}

// NewCapabilityScaler creates a new CapabilityScaler. pred must predict from capability history.
func NewCapabilityScaler(
	manager ScalerManager,
	pred predictor.Predictor,
	instanceStore store.InstanceStore,
	capabilityStore store.CapabilityHistoryStore,
	outboxStore store.OutboxStore,
	catalog *capability.Catalog,
	config types.ScalerConfig, //nolint:gocritic // acceptable for one-time setup
	metrics *metric.Metrics,
) *CapabilityScaler {
	return &CapabilityScaler{
		manager:         manager,
		predictor:       pred,
		instanceStore:   instanceStore,
		capabilityStore: capabilityStore,
		outboxStore:     outboxStore,
		catalog:         catalog,
		config:          config,
		metrics:         metrics,
		capacity:        newCapacityCache(capacityCacheTTL),
	}
}

// capabilitySlot is one pool of a capability group as seen by the scaler.
type capabilitySlot struct {
	member capability.Member
	// image is the image the scaler creates with: the variant's image, or empty for the pool default.
	image string
	// fqImage is image resolved the way hot-pool claims resolve it; free instances must match it.
	fqImage string
	free    int
	// used counts every instance and pending setup job of the slot's tenant and variant in the
	// pool; limit caps it.
	used int
}

// slotPlan is the scaling action for one slot.
type slotPlan struct {
	live       int
	hibernated int
	remove     int
}

// RunScaleJob runs the scale job of a capability target. Targets that are not capability scale
// job names (pool jobs left over from before an algorithm switch) are skipped.
func (s *CapabilityScaler) RunScaleJob(ctx context.Context, target string, windowStart, windowEnd int64) error {
	groupName, ok := types.ParseCapabilityScaleJobName(target)
	if !ok {
		logrus.WithField(logFieldPool, target).Infoln("scaler: not a capability scale job, skipping")
		return nil
	}
	return s.ScaleGroup(ctx, groupName, windowStart, windowEnd)
}

// ScaleGroup scales a capability group for the given window: one prediction for the whole
// group, with new instances placed in pool-file order (each pool's min size first, then up to
// its limit) and excess predictor instances removed from the end of that order.
func (s *CapabilityScaler) ScaleGroup(ctx context.Context, groupName string, windowStart, windowEnd int64) error {
	logr := logrus.WithField(logFieldCapability, groupName)
	group, ok := s.catalog.GroupByName(groupName)
	if !ok {
		logr.Errorln("scaler: capability group not found")
		return nil
	}
	members := s.activeMembers(&group)
	if len(members) == 0 {
		logr.Infoln("scaler: every pool of the capability group is disabled, skipping")
		return nil
	}
	key := group.Key

	slots, err := s.capabilitySlots(ctx, &key, members)
	if err != nil {
		return err
	}

	prediction, err := s.predictor.Predict(ctx, &predictor.PredictionInput{
		Capability:     &key,
		TenantID:       key.TenantID,
		VariantID:      key.VariantID,
		StartTimestamp: windowStart,
		EndTimestamp:   windowEnd,
	})
	if err != nil {
		return fmt.Errorf("scaler: failed to get capability prediction: %w", err)
	}

	minTotal, freeTotal := 0, 0
	for i := range slots {
		minTotal += slots[i].member.MinSize
		freeTotal += slots[i].free
	}
	target := max(prediction.PredictedInstances, minTotal)

	scaledByRecentUsage := false
	if prediction.PredictedInstances == 0 && s.config.RecentUsageMinInstances > 0 && target < s.config.RecentUsageMinInstances {
		since := time.Now().AddDate(0, 0, -s.config.RecentUsageLookbackDays).Unix()
		recent, checkErr := s.capabilityStore.HasRecentUsage(ctx, &key, since)
		if checkErr != nil {
			logr.WithError(checkErr).Warnln("scaler: failed to check capability recent usage, skipping recent usage minimum")
		} else if recent {
			target = s.config.RecentUsageMinInstances
			scaledByRecentUsage = true
		}
	}

	delta := target - freeTotal
	buffer := 0
	if delta > 0 && s.config.ScalePercent > 100 {
		buffer = int(math.Ceil(float64(delta) * (s.config.ScalePercent - 100.0) / 100.0)) //nolint:mnd
	}

	plans, unplaced := planCapabilityScale(slots, delta, buffer, scaledByRecentUsage)

	logr.WithFields(logrus.Fields{
		"predicted":              prediction.PredictedInstances,
		"min_size":               minTotal,
		"target":                 target,
		"current_free":           freeTotal,
		"delta":                  delta,
		"buffer_delta":           buffer,
		"unplaced":               unplaced,
		"pools":                  len(slots),
		"scaled_by_recent_usage": scaledByRecentUsage,
	}).Infoln("scaler: calculated capability scaling deltas")
	if unplaced > 0 {
		logr.WithField("unplaced", unplaced).Warnln("scaler: every pool of the capability group is at its limit")
	}

	s.recordMetrics(&key, slots, plans, target+buffer)

	if s.config.DryRun {
		logr.WithField("dry_run", true).Infoln("scaler: dry run mode enabled, skipping capability scale operation")
		return nil
	}

	for i := range slots {
		slot, plan := &slots[i], plans[i]
		if plan.live > 0 {
			s.scaleUp(ctx, &key, slot, plan.live, false)
		}
		if plan.hibernated > 0 {
			s.scaleUp(ctx, &key, slot, plan.hibernated, true)
		}
		if plan.remove > 0 {
			destroyFreePredictorInstances(ctx, s.instanceStore, s.manager, slot.member.Pool, key.TenantID, key.VariantID, slot.fqImage, plan.remove)
		}
	}
	logr.Infoln("scaler: capability scaling operation completed")
	return nil
}

// planCapabilityScale splits a group-level delta across slots in order. A positive delta (plus
// buffer) is placed first where a pool is below its min size, then wherever a pool is under its
// limit. A negative delta is removed from the last slot backwards, never taking a pool below its
// min size. It returns the per-slot plan and how many new instances found no room.
func planCapabilityScale(slots []capabilitySlot, delta, buffer int, liveAsHibernated bool) (plans []slotPlan, unplaced int) {
	plans = make([]slotPlan, len(slots))
	planned := func(i int) int { return slots[i].free + plans[i].live + plans[i].hibernated }
	plannedUsed := func(i int) int { return slots[i].used + plans[i].live + plans[i].hibernated }

	place := func(n int, hibernated bool) int {
		add := func(i, k int) {
			if hibernated {
				plans[i].hibernated += k
			} else {
				plans[i].live += k
			}
			n -= k
		}
		roomFor := func(i int) int {
			if slots[i].member.Limit <= 0 {
				return math.MaxInt
			}
			return max(0, slots[i].member.Limit-plannedUsed(i))
		}
		for i := range slots {
			if n == 0 {
				break
			}
			if deficit := slots[i].member.MinSize - planned(i); deficit > 0 {
				add(i, min(deficit, n, roomFor(i)))
			}
		}
		for i := range slots {
			if n == 0 {
				break
			}
			add(i, min(n, roomFor(i)))
		}
		return n
	}

	switch {
	case delta > 0:
		unplaced = place(delta, liveAsHibernated)
		unplaced += place(buffer, true)
	case delta < 0:
		excess := -delta
		for i := len(slots) - 1; i >= 0 && excess > 0; i-- {
			if spare := slots[i].free - slots[i].member.MinSize; spare > 0 {
				plans[i].remove = min(spare, excess)
				excess -= plans[i].remove
			}
		}
	}
	return plans, unplaced
}

// activeMembers returns the group's members whose pools are not disabled for scaling.
func (s *CapabilityScaler) activeMembers(group *capability.Group) []capability.Member {
	members := make([]capability.Member, 0, len(group.Members))
	for i := range group.Members {
		if !slices.Contains(s.config.DisabledPools, group.Members[i].Pool) {
			members = append(members, group.Members[i])
		}
	}
	return members
}

// capabilitySlots resolves each member's image and counts its free and used instances.
func (s *CapabilityScaler) capabilitySlots(ctx context.Context, key *types.CapabilityKey, members []capability.Member) ([]capabilitySlot, error) {
	slots := make([]capabilitySlot, 0, len(members))
	for i := range members {
		slot, err := s.capabilitySlot(ctx, key, &members[i], s.sharedPoolCapacity)
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

// capabilitySlot resolves the member's image and counts its free and used instances, reading the
// pool's counts with load.
func (s *CapabilityScaler) capabilitySlot(
	ctx context.Context, key *types.CapabilityKey, m *capability.Member,
	load func(ctx context.Context, pool string) (*poolCapacity, error),
) (capabilitySlot, error) {
	slot := capabilitySlot{member: *m}
	if m.Variant != nil {
		slot.image = m.Variant.ImageName
	}
	slot.fqImage = slot.image
	fq, err := s.manager.FullyQualifiedImage(ctx, m.Pool, key.TenantID, slot.image)
	if err != nil {
		logrus.WithError(err).WithField(logFieldPool, m.Pool).Warnln("scaler: failed to resolve pool image, matching configured image name")
	} else {
		slot.fqImage = fq
	}

	capacity, err := load(ctx, m.Pool)
	if err != nil {
		return slot, err
	}
	slot.free, slot.used = capacity.count(key, slot.fqImage)
	return slot, nil
}

// poolCapacity is a pool's instance counts and the setup jobs still waiting in the outbox.
type poolCapacity struct {
	instances []types.PoolInstanceCount
	pending   []types.SetupJobCount
}

// count returns the free and used instances of the tenant and variant of key. Terminating
// instances are neither. Free ones are those that match fqImage (any image if it is empty) and
// are, or will soon be, ready for a build. Setup jobs still in the outbox have no instance yet
// but count against the limit.
func (c *poolCapacity) count(key *types.CapabilityKey, fqImage string) (free, used int) {
	for i := range c.instances {
		n := &c.instances[i]
		if tenantOrDefault(n.TenantID) != key.TenantID || variantOrDefault(n.VariantID) != key.VariantID {
			continue
		}
		if n.State == types.StateTerminating {
			continue
		}
		used += n.Count
		if fqImage != "" && n.ImageName != fqImage {
			continue
		}
		if n.State == types.StateCreated || n.State == types.StateHibernating || n.State == types.StateProvisioning {
			free += n.Count
		}
	}
	for i := range c.pending {
		if n := &c.pending[i]; n.TenantID == key.TenantID && n.VariantID == key.VariantID {
			used += n.Count
		}
	}
	return free, used
}

// tenantOrDefault resolves a stored tenant, which is empty for rows written before tenants existed.
func tenantOrDefault(id string) string {
	if id == "" {
		return types.DefaultTenantID
	}
	return id
}

// variantOrDefault resolves a stored variant, which is empty for a pool's base configuration.
func variantOrDefault(id string) string {
	if id == "" {
		return types.DefaultVariantID
	}
	return id
}

// loadPoolCapacity reads a pool's counts from the stores.
func (s *CapabilityScaler) loadPoolCapacity(ctx context.Context, pool string) (*poolCapacity, error) {
	instances, err := s.instanceStore.CountPoolInstances(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("scaler: failed to count instances for pool %s: %w", pool, err)
	}
	pending, err := s.outboxStore.CountSetupJobs(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("scaler: failed to count setup jobs for pool %s: %w", pool, err)
	}
	return &poolCapacity{instances: instances, pending: pending}, nil
}

// sharedPoolCapacity is loadPoolCapacity shared by the scale jobs of a window. Every pool serves
// many capability groups, and a window's jobs run at about the same time, so without sharing the
// same pool is read once per group. Sharing cannot make one group see stale counts of another:
// a group is one tenant and variant of a platform, and a group's counts only cover its own tenant
// and variant, so nothing a job changes is read by a job of another group.
func (s *CapabilityScaler) sharedPoolCapacity(ctx context.Context, pool string) (*poolCapacity, error) {
	return s.capacity.get(ctx, pool, func(ctx context.Context) (*poolCapacity, error) {
		return s.loadPoolCapacity(ctx, pool)
	})
}

// capacityCacheTTL is how long a pool's counts are shared. A window's scale jobs are claimed
// within one outbox poll of each other and the next window is minutes away, so the jobs of a
// window share their pools' counts and the next window always reads fresh ones.
const capacityCacheTTL = 20 * time.Second

// capacityCache shares a pool's counts between concurrent and near-simultaneous callers: one
// caller loads while the others wait for its result. Failed loads are not reused.
type capacityCache struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]*capacityEntry
}

type capacityEntry struct {
	done     chan struct{} // closed once capacity and err are set
	capacity *poolCapacity
	err      error
	loadedAt time.Time
}

func newCapacityCache(ttl time.Duration) *capacityCache {
	return &capacityCache{ttl: ttl, now: time.Now, entries: map[string]*capacityEntry{}}
}

func (c *capacityCache) get(ctx context.Context, pool string, load func(context.Context) (*poolCapacity, error)) (*poolCapacity, error) {
	c.mu.Lock()
	e, ok := c.entries[pool]
	if ok {
		select {
		case <-e.done:
			// Loaded: reuse it while fresh. Otherwise (stale or failed) load again.
			if e.err == nil && c.now().Sub(e.loadedAt) < c.ttl {
				c.mu.Unlock()
				return e.capacity, nil
			}
			ok = false
		default:
			// Another caller is loading: wait for it below.
		}
	}
	if ok {
		c.mu.Unlock()
		select {
		case <-e.done:
			return e.capacity, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e = &capacityEntry{done: make(chan struct{})}
	c.entries[pool] = e
	c.mu.Unlock()

	e.capacity, e.err = load(ctx)
	e.loadedAt = c.now()
	close(e.done)
	return e.capacity, e.err
}

func (s *CapabilityScaler) recordMetrics(key *types.CapabilityKey, slots []capabilitySlot, plans []slotPlan, groupTarget int) {
	if s.metrics == nil {
		return
	}
	labels := []string{key.Tier, key.OS, key.Arch, key.VariantID, key.TenantID}
	if s.metrics.ScalerCapabilityPredictedInstances != nil {
		s.metrics.ScalerCapabilityPredictedInstances.WithLabelValues(labels...).Set(float64(groupTarget))
	}
	for i := range slots {
		poolLabels := append(slices.Clone(labels), slots[i].member.Pool)
		if s.metrics.ScalerCapabilityFreeInstances != nil {
			s.metrics.ScalerCapabilityFreeInstances.WithLabelValues(poolLabels...).Set(float64(slots[i].free))
		}
		if s.metrics.ScalerCapabilityTargetInstances != nil {
			target := slots[i].free + plans[i].live + plans[i].hibernated - plans[i].remove
			s.metrics.ScalerCapabilityTargetInstances.WithLabelValues(poolLabels...).Set(float64(target))
		}
	}
}

// capabilitySetupParams builds the setup params for a predictor instance in the slot's pool.
func capabilitySetupParams(key *types.CapabilityKey, m *capability.Member, image string, hibernate bool, tried []string) *types.SetupInstanceParams {
	params := &types.SetupInstanceParams{}
	if m.Variant != nil {
		*params = m.Variant.SetupInstanceParams
		params.MachineTypeFallbacks = slices.Clone(m.Variant.MachineTypeFallbacks)
		params.Zones = slices.Clone(m.Variant.Zones)
	}
	k := *key
	params.VariantID = key.VariantID
	params.TenantID = key.TenantID
	params.ImageName = image
	params.Source = types.InstanceSourcePredictor
	params.Hibernate = params.Hibernate || hibernate
	params.Capability = &k
	params.TriedPools = append(slices.Clone(tried), m.Pool)
	return params
}

func (s *CapabilityScaler) scaleUp(ctx context.Context, key *types.CapabilityKey, slot *capabilitySlot, count int, hibernate bool) {
	logr := logrus.WithFields(logrus.Fields{
		logFieldCapability: key.String(),
		logFieldPool:       slot.member.Pool,
		logFieldCount:      count,
		"hibernate":        hibernate,
	})
	logr.Infoln("scaler: capability scaling up")
	for i := 0; i < count; i++ {
		params := capabilitySetupParams(key, &slot.member, slot.image, hibernate, nil)
		if err := s.createSetupJob(ctx, slot.member.Pool, params); err != nil {
			logr.WithError(err).Errorln("scaler: failed to create setup instance job")
		}
	}
}

func (s *CapabilityScaler) createSetupJob(ctx context.Context, poolName string, params *types.SetupInstanceParams) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to marshal setup params: %w", err)
	}
	raw := json.RawMessage(paramsJSON)
	// Leave RunnerName empty so any runner can process this job.
	return s.outboxStore.Create(ctx, &types.OutboxJob{
		PoolName:  poolName,
		JobType:   types.OutboxJobTypeSetupInstance,
		JobParams: &raw,
		Status:    types.OutboxJobStatusPending,
	})
}

// HandleSetupFailure spills a failed capability setup job to the next pool of its group, in
// pool-file order, skipping pools already tried, disabled pools and pools at their limit. It
// returns true only when a job was created in another pool. Otherwise the failed job keeps the
// outbox's normal retries in its own pool, and each retry that fails tries to spill again.
func (s *CapabilityScaler) HandleSetupFailure(ctx context.Context, failedPool string, params *types.SetupInstanceParams, cause error) bool {
	if params == nil || params.Capability == nil {
		return false
	}
	key := *params.Capability
	logr := logrus.WithError(cause).WithFields(logrus.Fields{
		logFieldCapability: key.String(),
		"from_pool":        failedPool,
	})

	group, ok := s.catalog.Group(&key)
	if !ok {
		logr.Warnln("scaler: capability group no longer exists, retrying setup job in its pool")
		s.recordSpill(failedPool, "", metric.SpillOutcomeExhausted)
		return false
	}

	tried := params.TriedPools
	if !slices.Contains(tried, failedPool) {
		tried = append(slices.Clone(tried), failedPool)
	}
	for _, m := range s.activeMembers(&group) {
		if slices.Contains(tried, m.Pool) {
			continue
		}
		// A spill follows a failed setup, so it needs counts that include the jobs just created.
		slot, err := s.capabilitySlot(ctx, &key, &m, s.loadPoolCapacity)
		if err != nil {
			logr.WithError(err).WithField("to_pool", m.Pool).Warnln("scaler: failed to inspect spill pool, skipping it")
			continue
		}
		if m.Limit > 0 && slot.used >= m.Limit {
			continue
		}
		next := capabilitySetupParams(&key, &m, slot.image, params.Hibernate, tried)
		if err := s.createSetupJob(ctx, m.Pool, next); err != nil {
			logr.WithError(err).WithField("to_pool", m.Pool).Errorln("scaler: failed to create spill setup job")
			continue
		}
		logr.WithField("to_pool", m.Pool).Infoln("scaler: spilled failed setup job to next pool of capability group")
		s.recordSpill(failedPool, m.Pool, metric.SpillOutcomeSpilled)
		return true
	}

	logr.WithField("tried_pools", tried).Warnln("scaler: no pool of the capability group to spill to, retrying setup job in its pool")
	s.recordSpill(failedPool, "", metric.SpillOutcomeExhausted)
	return false
}

func (s *CapabilityScaler) recordSpill(from, to, outcome string) {
	if s.metrics != nil && s.metrics.ScalerCreateSpillCount != nil {
		s.metrics.ScalerCreateSpillCount.WithLabelValues(from, to, outcome).Inc()
	}
}
