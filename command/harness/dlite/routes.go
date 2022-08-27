package dlite

import (
	"github.com/wings-software/dlite/task"
)

var (
	initTask    = "DLITE_CI_VM_INITIALIZE_TASK"
	executeTask = "DLITE_CI_VM_EXECUTE_TASK"
	cleanupTask = "DLITE_CI_VM_CLEANUP_TASK"
)

func routeMap(c *dliteCommand) map[string]task.Handler {
	return map[string]task.Handler{
		initTask:    &VMInitTask{c},
		executeTask: &VMExecuteTask{c},
		cleanupTask: &VMCleanupTask{c},
	}
}
