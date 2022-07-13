package dlite

import (
	"github.com/wings-software/dlite/task"
)

func routeMap(c *dliteCommand) map[string]task.Handler {
	return map[string]task.Handler{
		"DLITE_CI_VM_INITIALIZE_TASK": &VMInitTask{c},
		"DLITE_CI_VM_EXECUTE_TASK":    &VMExecuteTask{c},
		"DLITE_CI_VM_CLEANUP_TASK":    &VMCleanupTask{c},
	}
}
