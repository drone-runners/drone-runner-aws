# Design Spec: Predictor-Driven Capacity Reservations

**Status:** Draft  
**Date:** 2026-04-29  
**Authors:** Saurabh Pahuja  

---

## 1. Problem Statement

There are two independent features in the runner today that solve related but disconnected problems:

### Feature 1: Predictor (Load Forecasting & Pre-Provisioning)

The predictor/scheduler forecasts incoming CI build load using an EMA (Exponential Moving Average) algorithm with weekend-awareness and 3-week historical decay. It operates on aligned time windows (default 30 minutes) and pre-provisions VMs ahead of predicted demand so that incoming build requests find warm, ready-to-use VMs instead of waiting for cold starts.

**How it works:**
- A utilization tracker records in-use instance counts every 30-60 seconds per (pool, variant, image).
- A scaler trigger fires 5 minutes before each window boundary and creates scale jobs.
- The scaler predicts demand for the upcoming window, compares against current free instances, and scales up/down accordingly.
- Scale-up creates VMs via `Driver.Create()` — standard on-demand instance creation.

**The problem:** When the scaler calls `Driver.Create()`, the cloud provider may reject the request due to insufficient capacity in the target zone. The request fails, the VM is not provisioned, and subsequent builds must wait or retry in alternate zones. During high-contention periods (e.g., morning ramp-up across many teams), this causes build delays.

### Feature 2: Capacity Reservations (Guaranteed VM Slots)

Capacity reservations are a cloud-provider feature (available on GCP and AWS) that locks VM capacity in a specific zone. Once a reservation exists, any VM creation targeting that reservation is guaranteed to succeed — the capacity is held exclusively for the reservation holder.

**How it works today:**
- Reservations are triggered by external callers via the dlite capacity API (stage setup requests).
- The runner calls `Driver.ReserveCapacity()` which creates a cloud-side reservation (GCP `Reservations.Insert` / AWS `CreateCapacityReservation`).
- A subsequent `Driver.Create()` call targets the reservation via `ReservationAffinity` (GCP) or `CapacityReservationSpecification` (AWS).
- Reservations are billed per-second — you only pay for the time the reservation exists, not a weekly/monthly commitment.
- Cleanup happens via explicit destroy, DB purger (stale state detection), and GCP-side TTL (`DeleteAfterDuration`).

**How it's enabled:** Reservations are currently only used when an external caller sends a capacity reservation request (capacity task via dlite). The predictor/scaler does NOT use reservations — it provisions VMs directly without capacity guarantees.

### The Gap

These two features operate independently. The predictor knows WHEN and HOW MANY VMs are needed but has no capacity guarantee. Reservations provide capacity guarantees but are only triggered by external requests, not by the predictor's forecasts.

**Goal:** Wire the predictor's scale-up path through capacity reservations so that pre-provisioned VMs have guaranteed capacity, reducing cloud-side provisioning failures by >70%.

---

## 2. Top Considerations

| # | Consideration | How We Address It |
|---|---------------|-------------------|
| 1 | Existing functionality intact | Feature gated by flag. Full unit test coverage before and after. No behavioral change when flag is off. |
| 2 | Controlled rollout via flags | Per-pool configuration. Can enable for one pool, monitor, then expand. |
| 3 | Cost visibility and traceability | New metrics for reservation idle cost, reservation duration, and reservation-backed vs direct-create counts. Labels link reservations to predictor source. |
| 4 | Success criteria | (a) Failed cloud requests / total requests reduces significantly. (b) Cost increase is minimal (seconds of idle reservation time per VM). |

---

## 2.1 Observability Gap (Pre-Requisite)

**Today we cannot accurately measure our primary success metric.** The runner lacks end-to-end visibility into cloud API request volume and failure rates at the predictor/scaler level.

### What We Have

| Metric | What It Tracks | Limitation |
|--------|---------------|------------|
| `harness_ci_pipeline_execution_total` (BuildCount) | Completed VM setups | Only counts successes, not total attempts |
| `harness_ci_pipeline_execution_errors_total` (FailedCount) | Failed VM setups | Tracks at setup level — mixes predictor and external-caller failures together, no breakdown by source |
| `harness_ci_capacity_reservation_total` | Capacity reservation completions | Only for external-caller reservations, not predictor |
| `harness_ci_scaler_predicted_instances` | Predicted instance count (gauge) | Output only — tells you what the predictor wants, not what it achieved |

### What's Missing

| Gap | Impact |
|-----|--------|
| **No counter for `Driver.Create()` calls** | Cannot measure total cloud API requests sent |
| **No counter for `Driver.Create()` failures by type** | Cannot distinguish capacity exhaustion vs quota vs API errors |
| **No counter for scaler scale-up attempts** | Cannot measure how many VMs the predictor tried to provision |
| **No counter for scaler scale-up failures** | Cannot measure how many predictor-initiated VMs failed to materialize |

### Required Instrumentation (Part of This Work)

Before enabling reservation-backed scaling, we must add baseline metrics to measure the "before" state:

1. **`cloud_create_attempts_total`** (Counter, labels: pool, driver, zone, source[predictor|external|manual])
   - Emitted at `provisioner.go:309` and scaler outbox handler on every `Driver.Create()` call.

2. **`cloud_create_failures_total`** (Counter, labels: pool, driver, zone, source, error_type[capacity_unavailable|quota|timeout|other])
   - Emitted when `Driver.Create()` returns an error.

3. **`scaler_scale_up_attempts_total`** (Counter, labels: pool, variant_id, image_name)
   - Emitted in `scaler.go` `scaleUp()` for each VM the scaler decides to provision.
   - This tells us: "the predictor wanted N VMs this window" — the demand signal from the forecasting model.

4. **`scaler_scale_up_failures_total`** (Counter, labels: pool, variant_id, image_name, error_type)
   - Emitted when an outbox `setup_instance` job (created by the scaler) fails during processing.
   - This tells us: "of the VMs the predictor asked for, M failed to actually materialize in the cloud."

**Why these two scaler metrics matter together:**

```
scaler_failure_rate = scaler_scale_up_failures_total / scaler_scale_up_attempts_total
```

This ratio is the precise before/after comparison for reservations. Today it's unmeasurable because:
- `ScalerPredictedInstances` is a gauge (target count), not a counter of actual attempts.
- `FailedCount` mixes predictor-initiated and external-caller failures — you can't isolate the predictor's success rate.

With these metrics, we can say definitively: "Before reservations, X% of predictor-initiated VMs failed. After, Y% fail." The target is >70% reduction in that failure rate.

These metrics must be deployed and collecting data for at least **2 weeks before** enabling reservations, to establish the baseline.

---

## 3. Success Criteria

### Primary Metric
```
reservation_failure_ratio = cloud_create_failures / total_create_attempts
```
- **Before:** Baseline measured over 2 weeks pre-rollout.
- **After:** Must show >70% reduction in `cloud_create_failures` for reservation-enabled pools.

### Secondary Metrics
| Metric | Target |
|--------|--------|
| Reservation idle cost as % of total pool compute spend | < 2% |
| Reservation creation success rate | > 95% (if < 95%, zone is capacity-constrained and needs attention) |
| P99 time-to-provision for predictor VMs | No regression (should improve) |
| Build wait time due to capacity failures | Measurable reduction |

### Rollback Trigger
- If reservation idle cost exceeds 5% of pool compute spend.
- If reservation creation failures > 30% (indicating systemic zone issues).
- If any regression in existing pool behavior (non-reservation paths).

---

## 4. Design

### 4.1 High-Level Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                     CURRENT FLOW (unchanged)                     │
│                                                                 │
│  Predictor → ScaleUp → OutboxJob(setup_instance) → Create VM   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│               NEW FLOW (when flag enabled per pool)             │
│                                                                 │
│  Predictor → ScaleUp → OutboxJob(setup_instance)                │
│                              │                                  │
│                              ▼                                  │
│                   ┌─── ReserveCapacity() ───┐                   │
│                   │                         │                   │
│                success                    failure               │
│                   │                         │                   │
│                   ▼                         ▼                   │
│          Create VM with              Create VM directly         │
│          ReservationAffinity         (existing behavior)        │
│                   │                         │                   │
│                   ▼                         ▼                   │
│          Link reservation            Normal instance            │
│          to instance in DB           (no reservation)           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 Fallback Strategy

The reservation step is **additive** — failure always falls back to the current direct-create path:

| Scenario | Behavior |
|----------|----------|
| `ErrCapacityReservationNotSupported` (Azure) | Skip reservation, create directly |
| `ErrCapacityUnavailable` (all zones full) | Skip reservation, create directly (may also fail, but retries as today) |
| Reservation succeeds, VM create fails | Destroy reservation, retry create without reservation |
| Flag disabled for pool | Existing behavior, no reservation attempted |

### 4.3 Configuration

```go
// In types.ScalerConfig (per-pool)
type ScalerConfig struct {
    // ... existing fields ...

    // UseReservations enables capacity reservations for predictor-initiated scaling.
    // When true, scale-up attempts to reserve capacity before creating VMs.
    // Falls back to direct creation on reservation failure.
    UseReservations bool `json:"use_reservations"`

    // ReservationTTLSeconds sets the auto-delete duration on cloud-side reservations.
    // Safety net: if runner fails to clean up, cloud provider deletes after this TTL.
    // Default: WindowDuration + LeadTime + 600s (10 min buffer).
    // Only applicable to GCP (AWS reservations have no TTL — purger handles cleanup).
    ReservationTTLSeconds int64 `json:"reservation_ttl_seconds"`

    // ReservationFallbackOnFailure controls behavior when reservation fails.
    // true (default): fall back to direct create.
    // false: fail the scale-up attempt (useful for strict capacity guarantees).
    ReservationFallbackOnFailure bool `json:"reservation_fallback_on_failure"`
}
```

Pool-level configuration in the runner config:
```yaml
pools:
  - name: "linux-amd64-build"
    driver: "google"
    scaler:
      use_reservations: true
      reservation_ttl_seconds: 2400
      reservation_fallback_on_failure: true
    # ... other pool config
```

### 4.4 Multi-Cloud Behavior

| Cloud | Reservation Support | Behavior When Flag Enabled |
|-------|:---:|---|
| **GCP** | Yes | Reserve → Create with `ReservationAffinity`. TTL via `DeleteAfterDuration`. |
| **AWS** | Yes | Reserve → Create with `CapacityReservationSpecification`. No cloud-side TTL (purger-only cleanup). |
| **Azure** | No | `ErrCapacityReservationNotSupported` → immediate fallback to direct create. No cost impact. |

**AWS Caveat:** AWS reservations have `EndDateType: "unlimited"` — no auto-expiry. Orphan cleanup relies entirely on the DB purger. If purger is delayed, AWS reservations accumulate cost. Mitigation: shorter purger intervals for reservation-enabled AWS pools.

**GCP Advantage:** `DeleteAfterDuration` provides a cloud-side safety net independent of runner health.

---

## 5. Implementation Plan

### Phase 1: Reserve-and-Create in Outbox Handler (MVP)

**Goal:** Predictor-initiated VMs attempt reservation before creation, controlled by per-pool flag.

#### Files to Modify

| File | Change |
|------|--------|
| `types/types.go` | Add `UseReservations`, `ReservationTTLSeconds`, `ReservationFallbackOnFailure` to `ScalerConfig`. Add `ReservationID` to `SetupInstanceParams`. |
| `command/config/config.go` | Wire new config fields from pool YAML. |
| `app/scheduler/jobs/scaler.go` | Pass `UseReservations` flag in outbox job params when creating setup_instance jobs. |
| `app/drivers/distributed_manager.go` | New method: `SetupInstanceWithReservation()` — reserve, then create with affinity. Fallback on error. |
| `app/drivers/instance_ops.go` | Ensure predictor-sourced instance destroy also cleans associated reservation. |
| `app/scheduler/jobs/outbox.go` | In `processSetupInstanceJob()`: check flag, route to reservation-backed or direct path. |
| `metric/metric.go` | New metrics (see Section 6). |

#### Implementation Sequence

1. Add config fields and wire them through (no behavior change).
2. Write unit tests for existing `scaleUp` and `processSetupInstanceJob` behavior (baseline).
3. Implement `SetupInstanceWithReservation()` in distributed_manager.
4. Add flag check in outbox processor to route to new path.
5. Add metrics instrumentation.
6. Write unit tests for new path (reservation success, failure+fallback, unsupported driver).
7. Integration test with flag on/off.

### Phase 2: Reservation Lifecycle Tracking

**Goal:** Full visibility into reservation cost and utilization.

- Track reservation `created_at` → `consumed_at` (when VM starts using it) → `destroyed_at`.
- Calculate idle duration = `consumed_at - created_at`.
- Expose as metric: `predictor_reservation_idle_seconds{pool, zone}`.
- Dashboard showing cost of idle reservations per pool.

### Phase 3: Zone Intelligence (Future)

**Goal:** Feed reservation success/failure data back into zone selection.

- Track reservation failure rates per zone.
- Deprioritize zones with high failure rates in predictor's provisioning.
- Alert when a zone crosses failure threshold.

---

## 6. Metrics & Observability

### New Metrics

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `predictor_reservation_attempts_total` | Counter | pool, zone, driver | Total reservation attempts from predictor |
| `predictor_reservation_success_total` | Counter | pool, zone, driver | Successful reservations |
| `predictor_reservation_failure_total` | Counter | pool, zone, driver, error_type | Failed reservations (capacity_unavailable, not_supported, other) |
| `predictor_reservation_idle_seconds` | Histogram | pool, zone, driver | Time between reservation creation and VM consuming it |
| `predictor_vm_create_with_reservation_total` | Counter | pool, driver, zone | VMs created with reservation affinity (happy path) |
| `predictor_vm_create_direct_total` | Counter | pool, driver, zone | VMs created directly — flag off or driver unsupported, no reservation attempted |

#### Fallback Metrics

When a reservation fails, we fall back to direct `Driver.Create()`. The fallback itself can succeed or fail — these are operationally different:
- **Fallback succeeds**: Zone had capacity, reservation API had a false negative (reservation infra issue, not a capacity problem).
- **Fallback fails**: Genuine capacity exhaustion — reservation correctly indicated no capacity, direct create also fails.

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `predictor_fallback_attempts_total` | Counter | pool, driver, reservation_error_type | Times we fell back to direct create after reservation failure |
| `predictor_fallback_success_total` | Counter | pool, driver, zone | Fallback direct-create succeeded (reservation was a false negative) |
| `predictor_fallback_failure_total` | Counter | pool, driver, zone, error_type | Fallback direct-create also failed (true capacity exhaustion) |

**Why the split matters:** A high `fallback_success` rate relative to `predictor_reservation_failure` means reservations are over-reporting capacity issues — the zones actually have capacity. This could indicate reservation API quota limits or zone-level reservation caps, not actual VM capacity constraints. Conversely, if `fallback_failure` is high, it confirms reservations are correctly detecting genuine capacity exhaustion and the fallback is futile — useful signal for zone deprioritization (Phase 3).
| `predictor_vm_unconsumed_total` | Gauge | pool, variant_id, image_name | VMs created by predictor that are currently idle (created/hibernating, never moved to InUse). High values indicate over-prediction — predictor provisioned VMs that no build ever needed. |

**Why `predictor_vm_unconsumed_total` matters:**

This metric directly measures predictor accuracy from the "waste" side. A predictor-created VM that is never consumed before it's cleaned up (TTL or scale-down) represents:
- Wasted cloud spend (the VM ran for nothing).
- With reservations enabled: wasted reservation cost + VM cost.

Tracking this alongside `scaler_scale_up_attempts_total` gives prediction efficiency:
```
predictor_waste_rate = predictor_vm_unconsumed_total / scaler_scale_up_attempts_total
```

If this ratio is high, the predictor is over-provisioning — reservations amplify the cost of that over-provisioning (you pay for both the reservation idle time AND the unused VM). This metric acts as a cost guardrail: if waste rate climbs after enabling reservations, the predictor's forecasting window or EMA weights may need tuning before reservations add value.

### Existing Metrics: Fixes Required

#### Cardinality Fix: Remove `address` Label from `BuildCount`

**Metric:** `harness_ci_pipeline_execution_total`  
**File:** `metric/builds.go:101`

Current labels:
```
pool_id, os, arch, driver, distributed, zone, owner_id, resource_class, address, image_version, image_name, variant_id
```

**Problem:** `address` is the VM's IP address — every new VM creates a new timeseries. At 1000 VMs/day this produces 1000+ new timeseries daily, leading to unbounded Prometheus memory growth.

**Fix:** Remove `address` label. Instance IP is available in logs and the instance DB table for drill-down when needed.

Updated labels:
```
pool_id, os, arch, driver, distributed, zone, owner_id, resource_class, image_version, image_name, variant_id
```

#### Cardinality Fix: Validate `resource_class` on `PoolFallbackCount`

**Metric:** `harness_ci_pipeline_pool_fallbacks`  
**File:** `metric/builds.go:329`

**Problem:** `resource_class` is user-supplied. If users define custom classes per build, cardinality grows unbounded.

**Fix:** Add validation — if `resource_class` exceeds a known set (audit existing values), normalize to `"custom"` in the label to cap cardinality.

#### Add `source` Label to Capacity Reservation Metrics

Once the predictor creates reservations, we need to distinguish predictor-initiated vs external-caller (dlite) reservations in existing metrics. Add a `source` label (`predictor` | `external`) to:

| Metric | File | Current Labels | Change |
|--------|------|---------------|--------|
| `harness_ci_capacity_reservation_total` | `metric/builds.go:434` | pool_id, os, arch, driver, distributed, owner_id | Add `source` |
| `harness_ci_capacity_reservation_errors_total` | `metric/builds.go:424` | pool_id, os, arch, driver, distributed, owner_id | Add `source` |
| `harness_ci_capacity_reservation_fallbacks_total` | `metric/builds.go:413` | pool_id, os, arch, driver, success, distributed, owner_id | Add `source` |
| `harness_ci_capacity_reservation_duration_seconds` | `metric/builds.go:390` | pool_id, os, arch, driver, is_fallback, distributed, owner_id | Add `source` |
| `harness_ci_capacity_reservation_per_pool_duration_seconds` | `metric/builds.go:402` | pool_id, os, arch, driver, distributed, owner_id | Add `source` |

**Why:** Without `source`, once the predictor starts creating reservations, all capacity metrics become a mixed signal — you can't tell if a reservation failure was from a user's build request or from the predictor's pre-provisioning. Dashboards and alerts need to separate these to avoid false positives.

**Backward compatibility:** Existing dlite callers pass `source=external`. New predictor path passes `source=predictor`. No existing queries break — they just gain a new label dimension. Queries without a `source` filter continue to aggregate both.

### Traceability

- Predictor-created reservations tagged with `source=predictor` in GCP labels / AWS tags.
- Reservation ID stored in instance record for audit trail.
- Log lines include `reservation_id`, `source=predictor`, `pool`, `zone` for correlation.

---

## 7. Cost Analysis

### Idle Reservation Cost Model

```
idle_cost_per_window = num_vms × idle_duration × hourly_rate / 3600

Where:
  idle_duration = time from reservation creation to VM consuming it
                ≈ outbox_poll_interval + vm_boot_time
                ≈ 30s + 60s = ~90 seconds typical
```

### Example (GCP, n1-standard-4 @ $0.19/hr)

| Scenario | VMs/window | Idle time | Cost/window | Cost/day (48 windows) |
|----------|:---:|:---:|:---:|:---:|
| Conservative | 20 | 90s | $0.095 | $4.56 |
| Moderate | 50 | 90s | $0.237 | $11.40 |
| Peak | 100 | 90s | $0.475 | $22.80 |

**Comparison:** A single failed build causing 5 min developer wait for 10 developers = 50 min of engineer time. Even at $1/min loaded cost, one incident costs more than a full day of reservation overhead.

### Cost Guardrails

1. **TTL safety net**: Reservations auto-deleted by GCP after configurable TTL (prevents runaway cost from orphans).
2. **Purger**: Cleans stale reservations within minutes (all clouds).
3. **Metrics alert**: If `predictor_reservation_idle_seconds` P95 > 5 min, investigate.
4. **Kill switch**: Set `use_reservations: false` per pool for immediate rollback.

---

## 8. Testing Strategy

### Pre-Implementation (Baseline)

1. **Unit tests for existing behavior** — snapshot current scaleUp, processSetupInstanceJob, and ScalePool behavior.
2. **Integration test**: Run scaler with flag OFF, verify no reservation calls made.

### Post-Implementation

#### Unit Tests

| Test Case | Validates |
|-----------|-----------|
| Flag OFF → no reservation call | Existing behavior preserved |
| Flag ON, reservation succeeds → VM created with affinity | Happy path |
| Flag ON, `ErrCapacityUnavailable` → fallback to direct create | Graceful degradation |
| Flag ON, `ErrCapacityReservationNotSupported` → fallback to direct create | Azure compatibility |
| Flag ON, reservation succeeds but VM create fails → reservation destroyed | Cleanup |
| Flag ON, `ReservationFallbackOnFailure=false`, reservation fails → job fails | Strict mode |
| Reservation linked to instance → destroy cleans both | Lifecycle |
| Metrics emitted correctly for each path | Observability |

#### Integration Tests

| Test Case | Validates |
|-----------|-----------|
| End-to-end: predictor → reserve → create → destroy | Full lifecycle |
| Pod dies after reservation, before VM → purger cleans up | Failure recovery |
| Multi-pool: one with flag ON, one OFF → independent behavior | Isolation |
| AWS pool with flag ON → uses EC2 Capacity Reservations | Multi-cloud |
| Azure pool with flag ON → graceful fallback, no error to user | Multi-cloud |

---

## 9. Rollout Plan

| Stage | Scope | Duration | Exit Criteria |
|-------|-------|----------|---------------|
| 1. Deploy with flag OFF | All environments | 1 week | No regressions in existing metrics |
| 2. Enable on 1 non-critical GCP pool | Single pool, single account | 1 week | Reservation success > 95%, idle cost < 2% |
| 3. Enable on primary GCP build pools | All GCP build pools | 2 weeks | Failed cloud requests reduced > 50% |
| 4. Enable on AWS pools | AWS pools | 1 week | Same criteria as GCP |
| 5. Evaluate Azure implementation | Design only | - | Decide if Azure Capacity Reservations worth implementing |

---

## 10. Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|:---:|---|
| Reservation creation fails frequently (zone exhausted) | No improvement, fallback to current behavior | Medium | Metric alerting, zone deprioritization (Phase 3) |
| Orphaned reservations accumulate cost (pod crash) | Unexpected cloud bill | Low | TTL (GCP), purger (all), metrics alert |
| AWS reservations leak (no TTL, purger delayed) | Cost accumulation | Low | Shorter purger interval for AWS, alert on reservation age |
| Reservation + VM create adds latency to scale-up | Slower pre-provisioning | Low | Reservation create is fast (~2-5s). Offset by guaranteed success. |
| Flag misconfiguration enables on wrong pool | Unintended cost | Low | Config validation, per-pool isolation, default OFF |
| Azure driver called with flag on | Error propagated to user | None | Graceful fallback on `ErrCapacityReservationNotSupported` |

---

## 11. Open Questions

1. **Should we batch reservations?** Currently `ReserveCapacity()` reserves 1 VM. If predictor needs 50, that's 50 API calls. GCP supports `Count > 1` in a single reservation — should we use bulk reservations?

2. **Zone preference in reservations:** Should the predictor prefer the zone where past reservations succeeded, or continue round-robin?

3. **Azure implementation priority:** Azure does support [On-demand Capacity Reservations](https://learn.microsoft.com/en-us/azure/virtual-machines/capacity-reservation-overview). Is it worth implementing in the Azure driver as part of this work?

4. **Reservation sharing across predictor and external callers:** Should predictor-created reservations be consumable by external capacity requests (dlite), or kept isolated?

---

## 12. Dependencies

- No external service dependencies beyond existing cloud APIs.
- No schema migration required (reuses existing `capacity_reservation` table).
- No new cloud IAM permissions required (reservation APIs already authorized).
