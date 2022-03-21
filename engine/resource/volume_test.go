package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVolume(t *testing.T) {
	v := "/path/to/src.txt:/path/to/dest.txt"
	src, dest, ro, err := ParseVolume(v)
	expSrc := "/path/to/src.txt"
	expDest := "/path/to/dest.txt"
	assert.Nil(t, err)
	assert.Equal(t, src, expSrc)
	assert.Equal(t, dest, expDest)
	assert.Equal(t, ro, false)
}

func TestParseVolume_WithRo(t *testing.T) {
	v := "/path/to/src.txt:/path/to/dest.txt:ro"
	src, dest, ro, err := ParseVolume(v)
	expSrc := "/path/to/src.txt"
	expDest := "/path/to/dest.txt"
	assert.Nil(t, err)
	assert.Equal(t, src, expSrc)
	assert.Equal(t, dest, expDest)
	assert.Equal(t, ro, true)
}

func TestParseVolumeError(t *testing.T) {
	v := "/path/to/src.txt,/path/to/dest.txt"
	_, _, _, err := ParseVolume(v) //nolint:dogsled
	assert.NotNil(t, err)
}
