package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
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
		slot, err := s.capabilitySlot(ctx, key, &members[i])
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func (s *CapabilityScaler) capabilitySlot(ctx context.Context, key *types.CapabilityKey, m *capability.Member) (capabilitySlot, error) {
	slot := capabilitySlot{member: *m}
	if m.Variant != nil {
		slot.image = m.Variant.ImageName
	}
	slot.fqImage = slot.image
	fq, err := s.manager.FullyQualifiedImage(ctx, m.Pool, slot.image)
	if err != nil {
		logrus.WithError(err).WithField(logFieldPool, m.Pool).Warnln("scaler: failed to resolve pool image, matching configured image name")
	} else {
		slot.fqImage = fq
	}

	instances, err := s.instanceStore.List(ctx, m.Pool, nil)
	if err != nil {
		return slot, fmt.Errorf("scaler: failed to list instances for pool %s: %w", m.Pool, err)
	}
	for _, inst := range instances {
		tenantID := inst.TenantID
		if tenantID == "" {
			tenantID = types.DefaultTenantID
		}
		variantID := inst.VariantID
		if variantID == "" {
			variantID = types.DefaultVariantID
		}
		if tenantID != key.TenantID || variantID != key.VariantID {
			continue
		}
		if inst.State == types.StateTerminating {
			continue
		}
		slot.used++
		if slot.fqImage != "" && inst.Image != slot.fqImage {
			continue
		}
		if inst.State == types.StateCreated || inst.State == types.StateHibernating || inst.State == types.StateProvisioning {
			slot.free++
		}
	}
	// Setup jobs still in the outbox have no instance yet but count against the limit.
	pending, err := s.outboxStore.CountSetupJobs(ctx, m.Pool, key.TenantID, key.VariantID)
	if err != nil {
		return slot, fmt.Errorf("scaler: failed to count setup jobs for pool %s: %w", m.Pool, err)
	}
	slot.used += pending
	return slot, nil
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
		slot, err := s.capabilitySlot(ctx, &key, &m)
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
