package le

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/harness/lite-engine/cli/certs"
)

const certPermissions = os.FileMode(0600)

func GenerateLECerts(serverName, relPath string) error {
	if _, err := os.Stat(relPath); err != nil && os.IsNotExist(err) {
		err = os.MkdirAll(relPath, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create directory at path %s: %w", relPath, err)
		}
		logrus.Infof("created certificates folder %s", relPath)
	} else if err != nil {
		return fmt.Errorf("failed to check existence of directory at path %s: %w", relPath, err)
	} else {
		logrus.Infof("certificates folder already exists %s", relPath)
	}

	caCertFilePath := filepath.Join(relPath, "ca-cert.pem")
	caKeyFilePath := filepath.Join(relPath, "ca-key.pem")
	tlsCertFilePath := filepath.Join(relPath, "server-cert.pem")
	tlsKeyFilePath := filepath.Join(relPath, "server-key.pem")

	if needToCreateCerts, err := needToCreateAny(caCertFilePath, caKeyFilePath, tlsCertFilePath, tlsKeyFilePath); err != nil {
		return err
	} else if !needToCreateCerts {
		logrus.Infof("using certificates from folder %s", relPath)
		return nil
	}

	logrus.Infof("creating new certificates in folder %s", relPath)

	ca, err := certs.GenerateCA()
	if err != nil {
		return fmt.Errorf("failed to generate ca certificate: %w", err)
	}

	tlsCert, err := certs.GenerateCert(serverName, ca)
	if err != nil {
		return fmt.Errorf("failed to generate tls certificate: %w", err)
	}

	if err := os.WriteFile(caCertFilePath, ca.Cert, certPermissions); err != nil {
		return fmt.Errorf("failed to write CA cert file: %w", err)
	}
	if err := os.WriteFile(caKeyFilePath, ca.Key, certPermissions); err != nil {
		return fmt.Errorf("failed to write CA key file: %w", err)
	}
	if err := os.WriteFile(tlsCertFilePath, tlsCert.Cert, certPermissions); err != nil {
		return fmt.Errorf("failed to write server cert file: %w", err)
	}
	if err := os.WriteFile(tlsKeyFilePath, tlsCert.Key, certPermissions); err != nil {
		return fmt.Errorf("failed to write server key file: %w", err)
	}

	return nil
}

func ReadLECerts(certFolder string) (caCertFile, certFile, keyFile string, err error) {
	contents, err := os.ReadFile(fmt.Sprintf("%s/ca-cert.pem", certFolder))
	if err != nil {
		return "", "", "", err
	}
	caCertFile = string(contents)
	contents, err = os.ReadFile(fmt.Sprintf("%s/server-cert.pem", certFolder))
	if err != nil {
		return caCertFile, "", "", err
	}
	certFile = string(contents)
	contents, err = os.ReadFile(fmt.Sprintf("%s/server-key.pem", certFolder))
	if err != nil {
		return caCertFile, certFile, "", err
	}
	keyFile = string(contents)
	return caCertFile, certFile, keyFile, nil
}

func needToCreate(p string) (bool, error) {
	stat, err := os.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to check existence of file at path %s: %w", p, err)
	}

	return err != nil || // file not exists
		stat.Size() == 0, nil // or file is empty
}

func needToCreateAny(ps ...string) (bool, error) {
	for _, p := range ps {
		if n, err := needToCreate(p); err != nil {
			return false, err
		} else if n {
			return true, nil
		}
	}
	return false, nil
}
