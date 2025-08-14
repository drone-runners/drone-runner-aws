package gradle

type Task struct {
	Name   string `json:"name"`
	TimeMs int64  `json:"time_ms"`
	State  string `json:"state"`
}

type Project struct {
	Name   string `json:"name"`
	TimeMs int64  `json:"time_ms"`
	Tasks  []Task `json:"tasks"`
}

type Profile struct {
	Projects            []Project `json:"projects"`
	Cmd                 string    `json:"command"`
	BuildTimeMs         int64     `json:"build_time_ms"`
	TaskExecutionTimeMs int64     `json:"task_execution_time_ms"`
}

type Metrics struct {
	Profiles []Profile `json:"profiles"`
}
