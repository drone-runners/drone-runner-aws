package harness

import (
	"testing"

	"github.com/harness/lite-engine/api"
	"github.com/harness/lite-engine/engine/spec"
)

func TestNewStreamClientPreservesOptionalLogScope(t *testing.T) {
	tests := []struct {
		name        string
		orgID       string
		projectID   string
		wantOrg     string
		wantProject string
	}{
		{
			name:        "complete scope",
			orgID:       "org",
			projectID:   "project",
			wantOrg:     "org",
			wantProject: "project",
		},
		{name: "org only", orgID: "org", wantOrg: "org"},
		{name: "project only", projectID: "project", wantProject: "project"},
		{name: "legacy unscoped"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newStreamClient(
				&api.LogConfig{
					URL:       "https://logs",
					AccountID: "account",
					OrgID:     test.orgID,
					ProjectID: test.projectID,
					Token:     "token",
				},
				spec.MtlsConfig{},
			)

			if client.OrgID != test.wantOrg || client.ProjectID != test.wantProject {
				t.Fatalf(
					"scope = (%q, %q), want (%q, %q)",
					client.OrgID,
					client.ProjectID,
					test.wantOrg,
					test.wantProject,
				)
			}
		})
	}
}
