package dlite

import (
	"github.com/harness/lite-engine/api"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/types"
)

type VMTaskExecutionResponse struct {
	ErrorMessage           string                    `json:"error_message"`
	IPAddress              string                    `json:"ip_address"`
	OutputVars             map[string]string         `json:"output_vars"`
	ServiceStatuses        []VMServiceStatus         `json:"service_statuses"`
	CommandExecutionStatus CommandExecutionStatus    `json:"command_execution_status"`
	DelegateMetaInfo       DelegateMetaInfo          `json:"delegate_meta_info"`
	Artifact               []byte                    `json:"artifact,omitempty"`
	PoolDriverUsed         string                    `json:"pool_driver_used"`
	Outputs                []*api.OutputV2           `json:"outputs"`
	OptimizationState      string                    `json:"optimization_state"`
	GitspacesPortMappings  map[int]int               `json:"gitspaces_port_mappings"`
	InstanceInfo           common.InstanceInfo       `json:"instance_info"`
	CapacityReservation    types.CapacityReservation `json:"capacity_reservation"`
	OSStats                *OSStats                  `json:"os_stats,omitempty"`
}

// OSStats contains OS-level resource usage statistics collected during stage execution.
type OSStats struct {
	TotalMemMB     float64 `json:"total_mem_mb"`
	CPUCores       int     `json:"cpu_cores"`
	AvgMemUsagePct float64 `json:"avg_mem_usage_pct"`
	AvgCPUUsagePct float64 `json:"avg_cpu_usage_pct"`
	MaxMemUsagePct float64 `json:"max_mem_usage_pct"`
	MaxCPUUsagePct float64 `json:"max_cpu_usage_pct"`
	P95MemUsagePct float64 `json:"p95_mem_usage_pct"`
	P95CpuUsagePct float64 `json:"p95_cpu_usage_pct"`
	PeakMemMB      float64 `json:"peak_mem_mb"`
}

type DelegateMetaInfo struct {
	ID       string `json:"id"`
	HostName string `json:"host_name"`
}

type VMServiceStatus struct {
	ID           string `json:"identifier"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	LogKey       string `json:"log_key"`
	Status       Status `json:"status"`
	ErrorMessage string `json:"error_message"`
}

var (
	httpOK     = 200
	httpFailed = 500
)

type Status string

const (
	Running Status = "RUNNING"
	Error   Status = "ERROR"
)

type CommandExecutionStatus string

const (
	Success      CommandExecutionStatus = "SUCCESS"
	Failure      CommandExecutionStatus = "FAILURE"
	RunningState CommandExecutionStatus = "RUNNING"
	Queued       CommandExecutionStatus = "QUEUED"
	Skipped      CommandExecutionStatus = "SKIPPED"
)

func failedResponse(msg string) VMTaskExecutionResponse {
	return VMTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: msg}
}
