package noop

type Option func(*config)

// WithRootDirectory returns an OS specific temp directory
func WithRootDirectory() Option {
	return func(p *config) {
		p.rootDir = "/tmp/noop"
	}
}

func WithHibernate(hibernate bool) Option {
	return func(p *config) {
		p.hibernate = hibernate
	}
}
