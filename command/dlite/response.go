package dlite

type VmTaskExecutionResponse struct {
	ErrorMessage           string                 `json:"error_message"`
	IPAddress              string                 `json:"ip_address"`
	OutputVars             map[string]string      `json:"output_vars"`
	ServiceStatuses        []VmServiceStatus      `json:"service_statuses"`
	CommandExecutionStatus CommandExecutionStatus `json:"command_execution_status"`
}

type VmServiceStatus struct {
	ID           string `json:"identifier"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	LogKey       string `json:"log_key"`
	Status       Status `json:"status"`
	ErrorMessage string `json:"error_message"`
}

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

func failedResponse(msg string) VmTaskExecutionResponse {
	return VmTaskExecutionResponse{CommandExecutionStatus: Failure, ErrorMessage: msg}
}
