package dlite

import (
	"github.com/wings-software/dlite/task"
)

func routeMap(c *dliteCommand) map[string]task.Handler {
	return map[string]task.Handler{
		"DLITE_CI_VM_INITIALIZE_TASK": &VmInitTask{c},
		"DLITE_CI_VM_EXECUTE_TASK":    &VmExecuteTask{c},
		"DLITE_CI_VM_CLEANUP_TASK":    &VmCleanupTask{c},
	}
}
