package ssh

type Option func(*config)

func WithHostname(s string) Option {
	return func(p *config) {
		p.hostname = s
	}
}

func WithUsername(s string) Option {
	return func(p *config) {
		p.username = s
	}
}

func WithPassword(s string) Option {
	return func(p *config) {
		p.password = s
	}
}

func WithSSHKey(s string) Option {
	return func(p *config) {
		p.sshkey = s
	}
}
