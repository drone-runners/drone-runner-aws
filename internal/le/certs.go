package le

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/harness/lite-engine/cli/certs"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const certPermissions = os.FileMode(0600)

func GenerateLECerts(serverName, relPath string) error {
	// lets see if the certificates exist
	_, existsErr := os.Stat(relPath)
	if existsErr == nil {
		logrus.Infof("certificates folder already exists %s", relPath)
		return nil
	}

	ca, err := certs.GenerateCA()
	if err != nil {
		return errors.Wrap(err, "failed to generate ca certificate")
	}

	tlsCert, err := certs.GenerateCert(serverName, ca)
	if err != nil {
		return errors.Wrap(err, "failed to generate certificate")
	}

	err = os.MkdirAll(relPath, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to create directory at path: %s", relPath))
	}

	caCertFilePath := filepath.Join(relPath, "ca-cert.pem")
	caKeyFilePath := filepath.Join(relPath, "ca-key.pem")
	if err := os.WriteFile(caCertFilePath, ca.Cert, certPermissions); err != nil {
		return errors.Wrap(err, "failed to write CA cert file")
	}
	if err := os.WriteFile(caKeyFilePath, ca.Key, certPermissions); err != nil {
		return errors.Wrap(err, "failed to write CA key file")
	}

	certFilePath := filepath.Join(relPath, "server-cert.pem")
	keyFilePath := filepath.Join(relPath, "server-key.pem")
	if err := os.WriteFile(certFilePath, tlsCert.Cert, certPermissions); err != nil {
		return errors.Wrap(err, "failed to write server cert file")
	}
	if err := os.WriteFile(keyFilePath, tlsCert.Key, certPermissions); err != nil {
		return errors.Wrap(err, "failed to write server key file")
	}
	return nil
}
