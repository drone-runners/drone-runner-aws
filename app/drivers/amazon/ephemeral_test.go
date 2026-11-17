package amazon

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"

	drtypes "github.com/drone-runners/drone-runner-aws/types"
)

func TestSecurityGroupsFor(t *testing.T) {
	ephemeral := &drtypes.InstanceCreateOpts{EphemeralDelegate: true}
	liteEngine := &drtypes.InstanceCreateOpts{}

	tests := []struct {
		name        string
		groups      []string
		autoGroupID string
		opts        *drtypes.InstanceCreateOpts
		want        []string
	}{
		{
			name:   "nil opts keeps configured groups",
			groups: []string{"sg-user"},
			opts:   nil,
			want:   []string{"sg-user"},
		},
		{
			name:        "lite-engine create keeps the auto group",
			groups:      []string{"sg-auto"},
			autoGroupID: "sg-auto",
			opts:        liteEngine,
			want:        []string{"sg-auto"},
		},
		{
			name:   "ephemeral create keeps pool groups when none were auto-created",
			groups: []string{"sg-user"},
			opts:   ephemeral,
			want:   []string{"sg-user"},
		},
		{
			name:        "ephemeral create drops only the auto group",
			groups:      []string{"sg-auto", "sg-user"},
			autoGroupID: "sg-auto",
			opts:        ephemeral,
			want:        []string{"sg-user"},
		},
		{
			name:        "ephemeral create with only the auto group omits groups",
			groups:      []string{"sg-auto"},
			autoGroupID: "sg-auto",
			opts:        ephemeral,
			want:        nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := &amazonConfig{groups: append([]string(nil), tc.groups...), autoGroupID: tc.autoGroupID}
			got := p.securityGroupsFor(tc.opts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("securityGroupsFor() = %#v, want %#v", got, tc.want)
			}
			if !reflect.DeepEqual(p.groups, tc.groups) {
				t.Fatalf("groups mutated: %#v", p.groups)
			}
		})
	}
}

func TestTerminateAfterFailedCreate(t *testing.T) {
	var got []string
	p := &amazonConfig{service: &mockEC2Client{
		TerminateInstancesFunc: func(_ context.Context, params *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
			got = append([]string(nil), params.InstanceIds...)
			return &ec2.TerminateInstancesOutput{}, nil
		},
	}}
	log := logrus.New()
	log.SetOutput(io.Discard)
	p.terminateAfterFailedCreate("i-123", logger.Logrus(logrus.NewEntry(log)))
	if !reflect.DeepEqual(got, []string{"i-123"}) {
		t.Fatalf("terminated %v, want [i-123]", got)
	}
}
