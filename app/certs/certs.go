package certs

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/harness/lite-engine/cli/certs"
)

func Generate(runnerName, tlsServerName string) (*types.InstanceCreateOpts, error) {
	ca, err := certs.GenerateCA()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ca certificate: %w", err)
	}
	tlsCert, err := certs.GenerateCert(tlsServerName, ca)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tls certificate: %w", err)
	}
	return &types.InstanceCreateOpts{
		CACert:     ca.Cert,
		CAKey:      ca.Key,
		TLSCert:    tlsCert.Cert,
		TLSKey:     tlsCert.Key,
		RunnerName: runnerName,
	}, nil
}
