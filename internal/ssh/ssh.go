// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package ssh

import (
	"context"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/drone/runner-go/logger"
)

const timeOut = 10
const networkTimeout = time.Minute * 10

// DialRetry configures and dials the ssh server and retries until a connection is established or a timeout is reached.
func DialRetry(ctx context.Context, ip, username, privatekey string) (*ssh.Client, error) {
	client, err := Dial(ip, username, privatekey)
	if err == nil {
		return client, nil
	}

	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		logger.FromContext(ctx).
			WithField("ip", ip).
			WithField("attempt", i).
			Trace("DialRetry: dialing the vm")
		client, err = Dial(ip, username, privatekey)
		if err == nil {
			return client, nil
		}
		logger.FromContext(ctx).
			WithError(err).
			WithField("ip", ip).
			WithField("attempt", i).
			Trace("DialRetry: failed to re-dial vm")

		if client != nil {
			client.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second * timeOut):
		}
	}
}

// RetryApplication retries a command until is returns without an error or a timeout is reached.
func RetryApplication(ctx context.Context, client *ssh.Client, command string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		logger.FromContext(ctx).
			WithField("attempt", i).
			Trace("RetryApplication: running the command")
		session, newSessionErr := client.NewSession()
		if newSessionErr != nil {
			logger.FromContext(ctx).
				WithError(newSessionErr).
				Debug("RetryApplication: failed to create session")
			return newSessionErr
		}
		runErr := session.Run(command)
		_ = session.Close()
		if runErr != nil {
			logger.FromContext(ctx).
				WithError(runErr).
				WithField("command", command).
				Trace("RetryApplication: failed running command")
		} else {
			return nil
		}

		logger.FromContext(ctx).
			WithError(runErr).
			WithField("attempt", i).
			Trace("RetryApplication: failed running command")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second * timeOut):
		}
	}
}

// Dial configures and dials the ssh server.
func Dial(server, username, privatekey string) (*ssh.Client, error) {
	if !strings.HasSuffix(server, ":22") {
		server += ":22"
	}
	config := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec //machines are ephemeral.
	}
	pem := []byte(privatekey)
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, err
	}
	config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	return ssh.Dial("tcp", server, config)
}
