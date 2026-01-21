package metric

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

type Metrics struct {
	BuildCount               *prometheus.CounterVec
	FailedCount              *prometheus.CounterVec
	ErrorCount               *prometheus.CounterVec
	RunningCount             *prometheus.GaugeVec
	RunningPerAccountCount   *prometheus.GaugeVec
	WarmPoolCount            *prometheus.GaugeVec
	PoolFallbackCount        *prometheus.CounterVec
	WaitDurationCount        *prometheus.HistogramVec
	TotalVMInitDurationCount *prometheus.HistogramVec
	CPUPercentile            *prometheus.HistogramVec
	MemoryPercentile         *prometheus.HistogramVec

	CapacityReservationCount                *prometheus.CounterVec
	CapacityReservationDurationCount        *prometheus.HistogramVec
	CapacityReservationPerPoolDurationCount *prometheus.HistogramVec
	CapacityReservationFallbackCount        *prometheus.CounterVec
	CapacityReservationFailedCount          *prometheus.CounterVec

	// Scaler metrics
	ScalerPredictedInstances *prometheus.GaugeVec

	stores []*Store
}

type label struct {
	os        string
	arch      string
	state     string
	poolID    string
	driver    string
	ownerID   string
	variantID string
}

type Store struct {
	Store       store.InstanceStore
	Query       *types.QueryParams
	Manager     drivers.IManager
	PoolConfig  *config.PoolFile
	Distributed bool
}

var (
	True             = "true"
	False            = "false"
	dbInterval       = 30 * time.Second
	warmPoolInterval = 2 * time.Minute
)

func ConvertBool(b bool) string {
	if b {
		return True
	}
	return False
}

func (m *Metrics) AddMetricStore(metricStore *Store) {
	m.stores = append(m.stores, metricStore)
}

// Error count provides metrics for total errors in the system
func ErrorCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_errors_total",
			Help: "Total number of system errors",
		},
		[]string{"owner_id", "distributed"},
	)
}

// BuildCount provides metrics for total number of pipeline executions (failed + successful)
func BuildCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_pipeline_execution_total",
			Help: "Total number of completed pipeline executions (failed + successful)",
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "zone", "owner_id", "resource_class", "address", "image_version", "image_name", "variant_id"},
	)
}

// FailedBuildCount provides metrics for total failed pipeline executions
func FailedBuildCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_pipeline_execution_errors_total",
			Help: "Total number of pipeline executions which failed due to system errors",
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "owner_id", "resource_class", "image_version", "image_name"},
	)
}

// RunningCount provides metrics for number of builds currently running
func RunningCount() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_pipeline_running_executions",
			Help: "Total number of running executions",
		},
		[]string{"pool_id", "os", "arch", "driver", "state", "distributed", "owner_id", "variant_id"}, // state can be running, in_use, or hibernating
	)
}

// RunningPerAccountCount provides metrics at account level for running executions
// This might be removed in the future as we don't want labels with high cardinality
// We are just using two labels at the moment which should be pretty small
func RunningPerAccountCount() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_pipeline_per_account_running_executions",
			Help: "Total number of running executions per account",
		},
		[]string{"owner_id", "os", "distributed"},
	)
}

// WarmPoolCount provides metrics for number of warm pool executions
func WarmPoolCount() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_pipeline_warm_pool_executions",
			Help: "Total number of warm pool executions",
		},
		[]string{"pool_id", "os", "arch", "driver", "state"},
	)
}

func (m *Metrics) UpdateRunningCount(ctx context.Context) {
	go func() {
		for {
			time.Sleep(dbInterval)
			wg := &sync.WaitGroup{}
			for _, ms := range m.stores {
				go m.updateRunningCount(ctx, ms, wg)
			}
			wg.Wait()
			m.RunningPerAccountCount.Reset()
			m.RunningCount.Reset()
		}
	}()
}

// UpdateWarmPoolCount starts a goroutine that updates the WarmPoolCount metric every minute
// by collecting pool statistics (free, busy, hibernating) for all pools using manager.List()
func (m *Metrics) UpdateWarmPoolCount(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(warmPoolInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				wg := &sync.WaitGroup{}
				for _, ms := range m.stores {
					go m.updateWarmPoolCount(ctx, ms, wg)
				}
				wg.Wait()
				// Reset the metric before setting new values
				m.WarmPoolCount.Reset()
			}
		}
	}()
}

func (m *Metrics) updateRunningCount(ctx context.Context, metricStore *Store, wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()
	d := make(map[label]int)
	// collect stats here
	instances, err := metricStore.Store.List(ctx, "", metricStore.Query)
	if err != nil {
		// TODO: log error
		return
	}
	for _, i := range instances {
		l := label{os: i.OS, arch: i.Arch, state: string(i.State), poolID: i.Pool, driver: string(i.Provider), ownerID: i.OwnerID, variantID: i.VariantID}
		if i.OwnerID != "" {
			m.RunningPerAccountCount.WithLabelValues(i.OwnerID, i.OS, strconv.FormatBool(metricStore.Distributed)).Inc()
		}
		d[l]++
	}
	for k, v := range d {
		m.RunningCount.WithLabelValues(k.poolID, k.os, k.arch, k.driver, k.state, strconv.FormatBool(metricStore.Distributed), k.ownerID, k.variantID).Set(float64(v))
	}
}

// updateWarmPoolCount collects pool statistics using instanceStore.List() for all pools
// and updates the WarmPoolCount metric with free, busy, and hibernating counts
func (m *Metrics) updateWarmPoolCount(ctx context.Context, metricStore *Store, wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()

	// Skip if Manager or PoolConfig is not available
	if metricStore.Manager == nil || metricStore.PoolConfig == nil {
		return
	}

	// Iterate through all pools in the configuration
	for i := range metricStore.PoolConfig.Instances {
		poolInstance := metricStore.PoolConfig.Instances[i]
		poolName := poolInstance.Name

		// Check if pool exists in the manager
		if !metricStore.Manager.Exists(poolName) {
			continue
		}

		// Get all instances for this pool using instanceStore.List()
		busy, free, hibernating, err := metricStore.Manager.List(ctx, poolName, metricStore.Query)
		if err != nil {
			// Log error but continue with other pools
			continue
		}

		// Get platform and driver information
		platform, _, driver := metricStore.Manager.Inspect(poolName)

		// Update metrics for busy instances
		if len(busy) > 0 {
			m.WarmPoolCount.WithLabelValues(
				poolName,
				platform.OS,
				platform.Arch,
				driver,
				"busy",
			).Set(float64(len(busy)))
		}

		// Count hibernated VMs from free list
		var hibernatedCount int
		for _, vm := range free {
			if vm.IsHibernated {
				hibernatedCount++
			}
		}

		// Update metrics for free instances (free - hibernated)
		if len(free)-hibernatedCount > 0 {
			m.WarmPoolCount.WithLabelValues(
				poolName,
				platform.OS,
				platform.Arch,
				driver,
				"free",
			).Set(float64(len(free) - hibernatedCount))
		}

		// Update metrics for hibernated instances
		if hibernatedCount > 0 {
			m.WarmPoolCount.WithLabelValues(
				poolName,
				platform.OS,
				platform.Arch,
				driver,
				"hibernated",
			).Set(float64(hibernatedCount))
		}

		// Update metrics for hibernating instances
		if len(hibernating) > 0 {
			m.WarmPoolCount.WithLabelValues(
				poolName,
				platform.OS,
				platform.Arch,
				driver,
				"hibernating",
			).Set(float64(len(hibernating)))
		}
	}
}

// PoolFallbackCount provides metrics for number of fallbacks while finding a valid pool
func PoolFallbackCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_pipeline_pool_fallbacks",
			Help: "Total number of fallbacks triggered on the pool",
		},
		[]string{"pool_id", "os", "arch", "driver", "success", "distributed", "owner_id", "resource_class", "image_version", "image_name", "variant_id"},
		// success is true/false depending on whether fallback happened successfully
	)
}

// CPUPercentile provides information about the max CPU usage in the pipeline run
func CPUPercentile() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_pipeline_max_cpu_usage_percent",
			Help:    "Max CPU usage in the pipeline",
			Buckets: []float64{30, 50, 70, 90},
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "variant_id"},
	)
}

// MemoryPercentile provides information about the max RAM usage in the pipeline run
func MemoryPercentile() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_pipeline_max_mem_usage_percent",
			Help:    "Max memory usage in the pipeline",
			Buckets: []float64{30, 50, 70, 90},
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "variant_id"},
	)
}

// WaitDurationCount provides metrics for amount of time needed to wait to setup a machine in a particular pool
func WaitDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_runner_wait_duration_seconds",
			Help:    "Waiting time needed to successfully allocate a machine in a pool",
			Buckets: []float64{5, 15, 30, 60, 300, 600},
		},
		[]string{"pool_id", "os", "arch", "driver", "is_fallback", "distributed", "owner_id", "image_version", "image_name", "warmed", "hibernated"},
	)
}

// TotalVMInitDurationCount provides metrics for amount of time needed to wait to setup a machine
func TotalVMInitDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_runner_total_vm_init_duration_seconds",
			Help:    "Total time needed to successfully allocate a machine",
			Buckets: []float64{5, 15, 30, 60, 300, 600},
		},
		[]string{"pool_id", "os", "arch", "driver", "is_fallback", "distributed", "owner_id", "image_version", "image_name", "warmed", "hibernated", "variant_id"},
	)
}

// CapacityReservationDurationCount provides metrics for total time needed to complete a capacity reservation
func CapacityReservationDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_capacity_reservation_duration_seconds",
			Help:    "Total time needed to successfully reserve capacity",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300},
		},
		[]string{"pool_id", "os", "arch", "driver", "is_fallback", "distributed", "owner_id"},
	)
}

// CapacityReservationPerPoolDurationCount provides metrics for time needed to reserve capacity per pool
func CapacityReservationPerPoolDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_capacity_reservation_per_pool_duration_seconds",
			Help:    "Time needed to reserve capacity in a specific pool",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300},
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "owner_id"},
	)
}

// CapacityReservationFallbackCount provides metrics for number of fallbacks during capacity reservation
func CapacityReservationFallbackCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_capacity_reservation_fallbacks_total",
			Help: "Total number of fallbacks triggered during capacity reservation",
		},
		[]string{"pool_id", "os", "arch", "driver", "success", "distributed", "owner_id"},
	)
}

// CapacityReservationFailedCount provides metrics for failed capacity reservations per pool
func CapacityReservationFailedCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_capacity_reservation_errors_total",
			Help: "Total number of capacity reservation failures per pool",
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "owner_id"},
	)
}

// CapacityReservationCount provides metrics for total number of capacity reservations (failed + successful)
func CapacityReservationCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_capacity_reservation_total",
			Help: "Total number of completed capacity reservations (failed + successful)",
		},
		[]string{"pool_id", "os", "arch", "driver", "distributed", "owner_id"},
	)
}

// ScalerPredictedInstances provides the predicted number of instances for a pool/variant
func ScalerPredictedInstances() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_scaler_predicted_instances",
			Help: "Predicted number of instances needed for the upcoming window",
		},
		[]string{"pool_id", "variant_id"},
	)
}

func RegisterMetrics() *Metrics {
	buildCount := BuildCount()
	failedBuildCount := FailedBuildCount()
	runningCount := RunningCount()
	runningPerAccountCount := RunningPerAccountCount()
	poolFallbackCount := PoolFallbackCount()
	waitDurationCount := WaitDurationCount()
	totalVMInitDurationCount := TotalVMInitDurationCount()
	warmPoolCount := WarmPoolCount()
	cpuPercentile := CPUPercentile()
	memoryPercentile := MemoryPercentile()
	errorCount := ErrorCount()
	capacityReservationCount := CapacityReservationCount()
	capacityReservationDurationCount := CapacityReservationDurationCount()
	capacityReservationPerPoolDurationCount := CapacityReservationPerPoolDurationCount()
	capacityReservationFallbackCount := CapacityReservationFallbackCount()
	capacityReservationFailedCount := CapacityReservationFailedCount()

	// Scaler metrics
	scalerPredictedInstances := ScalerPredictedInstances()

	prometheus.MustRegister(
		buildCount, failedBuildCount, runningCount, runningPerAccountCount,
		poolFallbackCount, waitDurationCount, totalVMInitDurationCount,
		cpuPercentile, memoryPercentile, errorCount, warmPoolCount,
		capacityReservationCount, capacityReservationDurationCount,
		capacityReservationPerPoolDurationCount, capacityReservationFallbackCount,
		capacityReservationFailedCount,
		scalerPredictedInstances,
	)

	return &Metrics{
		BuildCount:                              buildCount,
		FailedCount:                             failedBuildCount,
		RunningCount:                            runningCount,
		RunningPerAccountCount:                  runningPerAccountCount,
		PoolFallbackCount:                       poolFallbackCount,
		WaitDurationCount:                       waitDurationCount,
		TotalVMInitDurationCount:                totalVMInitDurationCount,
		MemoryPercentile:                        memoryPercentile,
		CPUPercentile:                           cpuPercentile,
		ErrorCount:                              errorCount,
		WarmPoolCount:                           warmPoolCount,
		CapacityReservationCount:                capacityReservationCount,
		CapacityReservationDurationCount:        capacityReservationDurationCount,
		CapacityReservationPerPoolDurationCount: capacityReservationPerPoolDurationCount,
		CapacityReservationFallbackCount:        capacityReservationFallbackCount,
		CapacityReservationFailedCount:          capacityReservationFailedCount,
		ScalerPredictedInstances:                scalerPredictedInstances,
	}
}
