// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package platform

import (
	"reflect"
	"testing"

	"github.com/kr/pretty"
)

func TestConvertTags(t *testing.T) {
	a := map[string]string{"foo": "bar", "baz": "qux"}
	b := map[string]string{}

	tags := convertTags(a)

	if got, want := len(tags), 2; got != want {
		t.Errorf("Want %d tags, got %d", want, got)
	}

	for _, tag := range tags {
		b[*tag.Key] = *tag.Value
	}

	if !reflect.DeepEqual(a, b) {
		t.Errorf("unexpected tag conversion")
		pretty.Ldiff(t, a, b)
	}
}
