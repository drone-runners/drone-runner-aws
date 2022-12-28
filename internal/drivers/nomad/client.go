package nomad

import "github.com/hashicorp/nomad/api"

func NewClient(address string, insecure bool, caCertPath, clientCertPath, clientKeyPath string) (*api.Client, error) {
	tlsConfig := &api.TLSConfig{
		CACert:     caCertPath,
		ClientKey:  clientKeyPath,
		Insecure:   insecure,
		ClientCert: clientCertPath,
	}
	config := &api.Config{
		Address:   address,
		TLSConfig: tlsConfig,
	}
	return api.NewClient(config)
}
