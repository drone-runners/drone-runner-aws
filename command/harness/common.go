package harness

type InstanceInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IPAddress         string `json:"ip_address"`
	Port              int64  `json:"port"`
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Provider          string `json:"provider"`
	PoolName          string `json:"pool_name"`
	Zone              string `json:"zone"`
	StorageIdentifier string `json:"storage_identifier"`
}
