# Design Spec: Predictor-Driven Capacity Reservations

**Status:** Draft  
**Date:** 2026-04-29  
**Authors:** Saurabh Pahuja  

---

## 1. Problem Statement

The predictor/scheduler pre-provisions VMs based on predicted load. However, VM creation can fail when the cloud provider lacks capacity in the target zone. Today, failures are handled by retrying in alternate zones — introducing latency and sometimes build delays during high-contention periods.

GCP and AWS both support capacity reservations (short-lived, per-second billing) that guarantee instance creation will succeed. These reservations are already implemented in the runner but are only triggered by external callers (dlite capacity tasks), not by the predictor.

**Goal:** Wire the predictor's scale-up path through capacity reservations so that pre-provisioned VMs have guaranteed capacity, reducing cloud-side failures to near zero.

---

## 2. Top Considerations

| # | Consideration | How We Address It |
|---|---------------|-------------------|
| 1 | Existing functionality intact | Feature gated by flag. Full unit test coverage before and after. No behavioral change when flag is off. |
| 2 | Controlled rollout via flags | Per-pool configuration. Can enable for one pool, monitor, then expand. |
| 3 | Cost visibility and traceability | New metrics for reservation idle cost, reservation duration, and reservation-backed vs direct-create counts. Labels link reservations to predictor source. |
| 4 | Success criteria | (a) Failed cloud requests / total requests reduces significantly. (b) Cost increase is minimal (seconds of idle reservation time per VM). |

---

## 3. Success Criteria

### Primary Metric
```
reservation_failure_ratio = cloud_create_failures / total_create_attempts
```
- **Before:** Baseline measured over 2 weeks pre-rollout.
- **After:** Must show >50% reduction in `cloud_create_failures` for reservation-enabled pools.

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
| `predictor_reservation_fallback_total` | Counter | pool, driver | Times we fell back to direct create |
| `predictor_reservation_idle_seconds` | Histogram | pool, zone, driver | Time between reservation creation and VM consuming it |
| `predictor_vm_create_with_reservation_total` | Counter | pool, driver | VMs created with reservation affinity |
| `predictor_vm_create_without_reservation_total` | Counter | pool, driver | VMs created without reservation (fallback or flag off) |

### Existing Metrics (unchanged)

- `CapacityReservationPerPoolDurationCount` — continues to track external-caller reservations.
- `CapacityReservationFailedCount` — continues to track external-caller failures.

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
