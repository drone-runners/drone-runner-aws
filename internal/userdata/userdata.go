package userdata

import (
	"github.com/drone-runners/drone-runner-aws/core"
	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/oshelp"
)

func Generate(userdata, os, arch string, opts *core.InstanceCreateOpts) string {
	var params = cloudinit.Params{
		Architecture:   arch,
		Platform:       os,
		CACert:         string(opts.CACert),
		TLSCert:        string(opts.TLSCert),
		TLSKey:         string(opts.TLSKey),
		LiteEnginePath: opts.LiteEnginePath,
	}

	if userdata == "" {
		if os == oshelp.OSWindows {
			userdata = cloudinit.Windows(&params)
		} else {
			userdata = cloudinit.Linux(&params)
		}
	} else {
		userdata, _ = cloudinit.Custom(userdata, &params)
	}
	return userdata
}
