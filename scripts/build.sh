#!/bin/sh

# disable go modules
export GOPATH=""

# CGO is required for SQLite
export CGO_ENABLED=1
export GOTOOLCHAIN=go1.26.10

set -e
set -x

# linux
GOOS=linux GOARCH=amd64 go build -ldflags "-extldflags \"-static\"" -o release/linux/amd64/drone-runner-aws
# darwin
#GOARCH=amd64 go build -o release/darwin/amd64/drone-runner-aws
