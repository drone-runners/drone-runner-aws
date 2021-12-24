package cloudinit_test

import (
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
)

const (
	publicKey      = "dmawlfelcuak"
	liteEnginePath = "/lite/engine/goes/here"
	caCertFile     = "qwerty123"
	certFile       = "abcdef456"
	keyFile        = "xyzuvw789"
)

func TestLinux(t *testing.T) {
	params := &cloudinit.Params{
		PublicKey:      publicKey + "\n",
		LiteEnginePath: liteEnginePath,
		CaCertFile:     caCertFile + "\n",
		CertFile:       certFile + "\n",
		KeyFile:        keyFile + "\n",
	}

	s := cloudinit.Linux(params)
	if !strings.Contains(s, publicKey) || !strings.Contains(s, `"`+liteEnginePath+`/lite-engine"`) {
		t.Error("linux init script does not contain public key or LE path")
	}
}

func TestWindows(t *testing.T) {
	params := &cloudinit.Params{
		PublicKey:      publicKey + "\n",
		LiteEnginePath: liteEnginePath,
		CaCertFile:     caCertFile + "\n",
		CertFile:       certFile + "\n",
		KeyFile:        keyFile + "\n",
	}

	s := cloudinit.Windows(params)
	if !strings.Contains(s, `"`+publicKey+`"`) || !strings.Contains(s, `"`+liteEnginePath+`/lite-engine.exe"`) {
		t.Error("windows init script does not contain public key or LE path")
	}
}
