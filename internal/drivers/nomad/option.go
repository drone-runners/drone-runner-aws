package nomad

type Option func(*config)

func WithAddress(s string) Option {
	return func(p *config) {
		p.address = s
	}
}

func WithCaCertPath(s string) Option {
	return func(p *config) {
		p.caCertPath = s
	}
}

func WithClientCertPath(s string) Option {
	return func(p *config) {
		p.clientCertPath = s
	}
}

func WithClientKeyPath(s string) Option {
	return func(p *config) {
		p.clientKeyPath = s
	}
}

func WithInsecure(b bool) Option {
	return func(p *config) {
		p.insecure = b
	}
}

func WithImage(s string) Option {
	return func(p *config) {
		p.vmImage = s
		if p.vmImage == "" {
			p.vmImage = "weaveworks/ignite-ubuntu:latest"
		}
	}
}

func WithMemory(s string) Option {
	return func(p *config) {
		p.vmMemory = s
		if p.vmMemory == "" {
			p.vmMemory = "6GB"
		}
	}
}

func WithCpus(s string) Option {
	return func(p *config) {
		p.vmCpus = s
		if p.vmCpus == "" {
			p.vmCpus = "2"
		}
	}
}
