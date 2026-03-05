package drivers

import (
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// GetInstanceStore returns the instance store.
func (m *Manager) GetInstanceStore() store.InstanceStore {
	return m.instanceStore
}

// GetStageOwnerStore returns the stage owner store.
func (m *Manager) GetStageOwnerStore() store.StageOwnerStore {
	return m.stageOwnerStore
}

// GetCapacityReservationStore returns the capacity reservation store.
func (m *Manager) GetCapacityReservationStore() store.CapacityReservationStore {
	return m.capacityReservationStore
}

// GetTLSServerName returns the TLS server name.
func (m *Manager) GetTLSServerName() string {
	if m.runnerConfig.HA {
		return "drone-runner-ha"
	}
	return m.runnerName
}

// GetRunnerConfig returns the runner configuration.
func (m *Manager) GetRunnerConfig() types.RunnerConfig {
	return m.runnerConfig
}

// GetHealthCheckTimeout returns the appropriate health check timeout based on the OS, provider, warmed status, and hibernated status.
func (m *Manager) GetHealthCheckTimeout(os string, provider types.DriverType, warmed, hibernated bool) time.Duration {
	// Override for Windows
	if os == "windows" {
		return m.runnerConfig.HealthCheckWindowsTimeout
	}

	// Use hotpool timeout for Nomad (true hot pool with running processes)
	if provider == types.Nomad {
		return m.runnerConfig.HealthCheckHotpoolTimeout
	}

	// Use hibernated timeout for hibernated VMs (need time to resume from disk and restart processes)
	if hibernated {
		return m.runnerConfig.HealthCheckHibernatedTimeout
	}

	// Use hotpool timeout for other warmed instances
	if warmed {
		return m.runnerConfig.HealthCheckHotpoolTimeout
	}

	// Default to cold start timeout
	return m.runnerConfig.HealthCheckColdstartTimeout
}

// GetHealthCheckConnectivityDuration returns the health check connectivity duration.
func (m *Manager) GetHealthCheckConnectivityDuration() time.Duration {
	return m.runnerConfig.HealthCheckConnectivityDuration
}

// GetSetupTimeout returns the setup timeout.
func (m *Manager) GetSetupTimeout() time.Duration {
	return m.runnerConfig.SetupTimeout
}

// IsDistributed returns whether the manager is in distributed mode.
func (m *Manager) IsDistributed() bool {
	return false
}
