package buildcache

type (
	Metadata struct {
		TotalTasks  int `json:"total_tasks"`
		CachedTasks int `json:"cached_tasks"`
	}
)
