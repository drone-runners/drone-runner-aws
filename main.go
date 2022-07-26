// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package main

import (
	"math/rand"
	"time"

	"github.com/drone-runners/drone-runner-aws/command"
	_ "github.com/joho/godotenv/autoload"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	command.Command()
}
