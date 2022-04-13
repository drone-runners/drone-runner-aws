package userdata

import (
	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
)

func Generate(userdata, os, arch string, opts *types.InstanceCreateOpts) string {
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
		} else if os == oshelp.OSMac {
			userdata = cloudinit.Mac(&params)
		} else {
			userdata = cloudinit.Linux(&params)
		}
	} else {
		userdata, _ = cloudinit.Custom(userdata, &params)
	}
	return userdata
}
