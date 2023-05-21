package dlite

type VMTaskExecutionResponse struct {
	ErrorMessage           string                 `json:"error_message"`
	IPAddress              string                 `json:"ip_address"`
	OutputVars             map[string]string      `json:"output_vars"`
	ServiceStatuses        []VMServiceStatus      `json:"service_statuses"`
	CommandExecutionStatus CommandExecutionStatus `json:"command_execution_status"`
	DelegateMetaInfo       DelegateMetaInfo       `json:"delegate_meta_info"`
	Artifact               []byte                 `json:"artifact,omitempty"`
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
