package certs

import (
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/patrickmn/go-cache"
)

var (
	expirationTime = 2 * time.Hour
)

type CertsCache struct {
	c      *cache.Cache
	expiry time.Duration
}

// NewCertsCache creates a certs cache to use in the lite engine
// every expirationTime interval
func NewCertsCache() *CertsCache {
	// purge certs from the cache at expirationTime/3 intervals
	c := cache.New(expirationTime, expirationTime/3)
	return &CertsCache{
		expiry: expirationTime,
		c:      c,
	}
}

// Get returns the certs for a given key. The certs are generated if
// they don't exist for that key. They are always refreshed in the cache after
// expirationTime.
func (c *CertsCache) Get(key string) (*types.InstanceCreateOpts, error) {
	cert, found := c.c.Get(key)
	if found {
		return cert.(*types.InstanceCreateOpts), nil
	}
	opts, err := Generate(key)
	if err != nil {
		return opts, err
	}
	// refresh token before the expiration time to give some buffer
	c.c.Set(key, opts, c.expiry)
	return opts, err
}
