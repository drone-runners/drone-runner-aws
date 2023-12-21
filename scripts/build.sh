#!/bin/sh

# disable go modules
export GOPATH=""

# disable cgo
export CGO_ENABLED=1

set -e
set -x

# linux
go build -ldflags "-extldflags \"-static\"" -o release/linux/amd64/drone-runner-aws-linux-amd64
# darwin
#GOARCH=amd64 go build -o release/linux/amd64/drone-runner-aws-linux-amd64

