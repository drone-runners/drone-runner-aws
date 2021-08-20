// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package sshkey

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"golang.org/x/crypto/ssh"
)

// GeneratePair generates an RSA Key pair.
func GeneratePair() (public, private string, err error) {
	key, err := Generate()
	if err != nil {
		return
	}
	private = MarshalPrivateKey(key)
	public, err = MarshalPublicKey(&key.PublicKey)
	return
}

// Generate generates an RSA Private Key.
func Generate() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048) //nolint:gomnd
}

// MarshalPublicKey marshalls an RSA Public Key to an SSH .authorized_keys format
func MarshalPublicKey(pubkey *rsa.PublicKey) (string, error) {
	pk, err := ssh.NewPublicKey(pubkey)
	return string(ssh.MarshalAuthorizedKey(pk)), err
}

// MarshalPrivateKey marshalls an RSA Private Key to
// a PEM encoded file.
func MarshalPrivateKey(privkey *rsa.PrivateKey) string {
	privateKeyMarshaled := x509.MarshalPKCS1PrivateKey(privkey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: privateKeyMarshaled})
	return string(privateKeyPEM)
}
