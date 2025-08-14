package dlc

type (
	LayerStatus struct {
		Status string  `json:"status"`
		Time   float64 `json:"time"` // Time in seconds; only set for DONE layers
	}

	Metrics struct {
		TotalLayers int                 `json:"total_layers"`
		Done        int                 `json:"done"`
		Cached      int                 `json:"cached"`
		Error       int                 `json:"error"`
		Canceled    int                 `json:"canceled"`
		Layers      map[int]LayerStatus `json:"layers"`
	}

	Metadata struct {
		TotalLayers int `json:"total_layers"`
		Cached      int `json:"cached"`
	}
)
