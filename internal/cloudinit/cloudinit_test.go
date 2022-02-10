package cloudinit_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
)

const (
	liteEnginePath = "/lite/engine/goes/here"
	caCertFile     = "qwerty123"
	certFile       = "abcdef456"
	keyFile        = "xyzuvw789"
	platform       = "spectrum"
	arch           = "z80"
)

func TestLinux(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CaCertFile:     caCertFile + "\n",
		CertFile:       certFile + "\n",
		KeyFile:        keyFile + "\n",
		Platform:       platform,
		Architecture:   arch,
	}

	s := cloudinit.Linux(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s"`, params.LiteEnginePath, params.Platform, params.Architecture)
	if !strings.Contains(s, lePath) {
		t.Error("linux init script does not contain LE path")
	}
}

func TestWindows(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CaCertFile:     caCertFile + "\n",
		CertFile:       certFile + "\n",
		KeyFile:        keyFile + "\n",
	}

	s := cloudinit.Windows(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s.exe"`, params.LiteEnginePath, params.Platform, params.Architecture)
	if !strings.Contains(s, lePath) {
		t.Error("windows init script does not contain LE path")
	}
}
