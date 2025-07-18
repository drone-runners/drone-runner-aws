package harness

import (
	"strconv"

	"github.com/sirupsen/logrus"
)

type Context struct {
	AccountID     string `json:"account_id,omitempty"`
	OrgID         string `json:"org_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	PipelineID    string `json:"pipeline_id,omitempty"`
	RunSequence   int    `json:"run_sequence,omitempty"`
	IsFreeAccount bool   `json:"is_free_account,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	IsRunner      bool   `json:"is_runner,omitempty"`
}

const (
	trueString = "true"
)

func AddContext(logr *logrus.Entry, context *Context, tags map[string]string) *logrus.Entry {
	return logr.WithField("account_id", GetAccountID(context, tags)).
		WithField("org_id", getOrgID(context, tags)).
		WithField("project_id", getProjectID(context, tags)).
		WithField("pipeline_id", getPipelineID(context, tags)).
		WithField("build_id", getRunSequence(context, tags)).
		WithField("task_id", getTaskID(context, tags)).
		WithField("is_runner", getIsRunner(context, tags))
}

// These functions can be removed in the next release once we start populating context
func GetAccountID(context *Context, tags map[string]string) string {
	if context.AccountID != "" {
		return context.AccountID
	}
	return tags["accountID"]
}

func getIsFreeAccount(context *Context, tags map[string]string) bool {
	// if freeCI is a key in tags, use it
	if tags["freeCI"] == trueString {
		return true
	}
	return context.IsFreeAccount
}

func getIsRunner(context *Context, tags map[string]string) string {
	if tags["isRunner"] == trueString {
		return trueString
	}
	return strconv.FormatBool(context.IsRunner)
}

func getTaskID(context *Context, tags map[string]string) string {
	if context.TaskID != "" {
		return context.TaskID
	}
	return tags["taskId"]
}

func getOrgID(context *Context, tags map[string]string) string {
	if context.OrgID != "" {
		return context.OrgID
	}
	return tags["orgID"]
}

func getPipelineID(context *Context, tags map[string]string) string {
	if context.PipelineID != "" {
		return context.PipelineID
	}
	return tags["pipelineID"]
}

func getProjectID(context *Context, tags map[string]string) string {
	if context.ProjectID != "" {
		return context.ProjectID
	}
	return tags["projectID"]
}

func getRunSequence(context *Context, tags map[string]string) int {
	if context.RunSequence != 0 {
		return context.RunSequence
	}
	b, _ := strconv.Atoi(tags["buildNumber"])
	return b
}
