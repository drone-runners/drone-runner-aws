package metric

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	BuildCount        *prometheus.CounterVec
	FailedCount       *prometheus.CounterVec
	RunningCount      *prometheus.GaugeVec
	PoolFallbackCount *prometheus.CounterVec
	WaitDurationCount *prometheus.HistogramVec
}

type label struct {
	os     string
	arch   string
	state  string
	poolID string
	driver string
}

var (
	True       = "true"
	False      = "false"
	dbInterval = 30 * time.Second
)

func ConvertBool(b bool) string {
	if b {
		return True
	}
	return False
}

// BuildCount provides metrics for total number of pipeline executions (failed + successful)
func BuildCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_pipeline_execution_total",
			Help: "Total number of completed pipeline executions (failed + successful)",
		},
		[]string{"pool_id", "os", "arch", "driver"},
	)
}

// FailedBuildCount provides metrics for total failed pipeline executions
func FailedBuildCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_pipeline_execution_errors_total",
			Help: "Total number of pipeline executions which failed due to system errors",
		},
		[]string{"pool_id", "os", "arch", "driver"},
	)
}

// RunningCount provides metrics for number of builds currently running
func RunningCount(instanceStore store.InstanceStore) *prometheus.GaugeVec {
	metric := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_pipeline_running_executions",
			Help: "Total number of running executions",
		},
		[]string{"pool_id", "os", "arch", "driver", "state"}, // state can be running, in_use, or hibernating
	)
	go updateRunningCount(context.Background(), instanceStore, metric)
	return metric
}

func updateRunningCount(ctx context.Context, instanceStore store.InstanceStore, dbMetric *prometheus.GaugeVec) {
	for {
		time.Sleep(dbInterval)
		m := make(map[label]int)
		// collect stats here
		instances, err := instanceStore.List(ctx, "", &types.QueryParams{})
		if err != nil {
			// TODO: log error
			continue
		}
		for _, i := range instances {
			l := label{os: i.OS, arch: i.Arch, state: string(i.State), poolID: i.Pool, driver: string(i.Provider)}
			m[l]++
		}
		dbMetric.Reset()
		for k, v := range m {
			dbMetric.WithLabelValues(k.poolID, k.os, k.arch, k.driver, k.state).Set(float64(v))
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
		[]string{"pool_id", "os", "arch", "driver", "success"}, // success is true/false depending on whether fallback happened successfully
	)
}

// WaitDurationCount provides metrics for amount of time needed to wait to setup a machine
func WaitDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "harness_ci_runner_wait_duration_seconds",
			Help:    "Waiting time needed to successfully allocate a machine",
			Buckets: []float64{5, 15, 30, 60, 300, 600},
		},
		[]string{"pool_id", "os", "arch", "driver", "is_fallback"},
	)
}

func RegisterMetrics(instanceStore store.InstanceStore) *Metrics {
	buildCount := BuildCount()
	failedBuildCount := FailedBuildCount()
	runningCount := RunningCount(instanceStore)
	poolFallbackCount := PoolFallbackCount()
	waitDurationCount := WaitDurationCount()
	prometheus.MustRegister(buildCount, failedBuildCount, runningCount, poolFallbackCount, waitDurationCount)
	return &Metrics{
		BuildCount:        buildCount,
		FailedCount:       failedBuildCount,
		RunningCount:      runningCount,
		PoolFallbackCount: poolFallbackCount,
		WaitDurationCount: waitDurationCount,
	}
}
