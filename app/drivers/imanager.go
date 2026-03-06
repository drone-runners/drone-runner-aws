package drivers

// IManager provides a unified interface for all VM management operations.
// It composes several focused interfaces following the Interface Segregation Principle.
type IManager interface {
	InstanceProvisioner
	InstanceQuerier
	PoolManager
	InstanceLifecycle
	HealthChecker
	StoreProvider
	ConfigProvider
	PurgerStarter
}
