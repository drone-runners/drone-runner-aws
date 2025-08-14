// Copyright 2019 Drone.IO Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package environ

import (
	"context"

	"github.com/drone/drone-go/drone"
)

// V1 is version 1 of the env API
const V1 = "application/vnd.drone.env.v1+json"

// V2 is version 2 of the env API
const V2 = "application/vnd.drone.env.v2+json"

type (
	// Request defines a environment request.
	Request struct {
		Repo  drone.Repo  `json:"repo,omitempty"`
		Build drone.Build `json:"build,omitempty"`
	}

	// Variable defines an environment variable.
	Variable struct {
		Name string `json:"name,omitempty"`
		Data string `json:"data,omitempty"`
		Mask bool   `json:"mask,omitempty"`
	}

	// Plugin responds to a registry request.
	Plugin interface {
		List(context.Context, *Request) ([]*Variable, error)
	}
)
