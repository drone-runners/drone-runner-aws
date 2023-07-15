package harness

import "github.com/sirupsen/logrus"

type Context struct {
	AccountID   string `json:"account_id,omitempty"`
	OrgID       string `json:"org_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	PipelineID  string `json:"pipeline_id,omitempty"`
	RunSequence int    `json:"run_sequence,omitempty"`
}

func AddContext(logr *logrus.Entry, context Context) *logrus.Entry {
	return logr.WithField("account_id", context.AccountID).
		WithField("org_id", context.OrgID).
		WithField("project_id", context.ProjectID).
		WithField("pipeline_id", context.PipelineID).
		WithField("build_id", context.RunSequence)
}
