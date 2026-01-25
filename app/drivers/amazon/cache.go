package amazon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const (
	// AMI cache TTL - cache AMI IDs for 1 hour to balance performance and freshness
	amiCacheTTL = 1 * time.Hour
)

// amiCacheEntry represents a cached AMI ID with expiration time
type amiCacheEntry struct {
	amiID    string
	cachedAt time.Time
	ttl      time.Duration
}

// isExpired checks if the cache entry has expired
func (e *amiCacheEntry) isExpired() bool {
	return time.Since(e.cachedAt) > e.ttl
}

// AMICache provides thread-safe caching for AMI ID resolution
type AMICache struct {
	cache map[string]*amiCacheEntry
	mutex sync.RWMutex
}

// NewAMICache creates a new AMI cache instance
func NewAMICache() *AMICache {
	return &AMICache{
		cache: make(map[string]*amiCacheEntry),
	}
}

// Get retrieves an AMI ID from cache if it exists and is not expired
func (c *AMICache) Get(region, imageName string) (string, bool) {
	cacheKey := fmt.Sprintf("%s:%s", region, imageName)

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if entry, exists := c.cache[cacheKey]; exists && !entry.isExpired() {
		return entry.amiID, true
	}

	return "", false
}

// Set stores an AMI ID in cache with TTL
func (c *AMICache) Set(region, imageName, amiID string) {
	cacheKey := fmt.Sprintf("%s:%s", region, imageName)

	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cache[cacheKey] = &amiCacheEntry{
		amiID:    amiID,
		cachedAt: time.Now(),
		ttl:      amiCacheTTL,
	}
}

// isAMIID checks if the given string is in AMI ID format (ami-xxxxxxxxx)
func isAMIID(imageID string) bool {
	return strings.HasPrefix(imageID, "ami-") && len(imageID) >= 12
}

// resolveImageNameToAMI resolves an image name to its corresponding AMI ID with caching
func (p *amazonConfig) resolveImageNameToAMI(ctx context.Context, imageName string) (string, error) {
	// Check cache first
	if amiID, found := p.amiCache.Get(p.region, imageName); found {
		return amiID, nil
	}

	// Cache miss, fetch from AWS API
	client := p.service

	// Search for images by name
	input := &ec2.DescribeImagesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{imageName},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
		// Only include images owned by Amazon or the current account
		Owners: []string{"amazon", "self"},
	}

	result, err := client.DescribeImages(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe images: %w", err)
	}

	if len(result.Images) == 0 {
		return "", fmt.Errorf("no AMI found with name '%s'", imageName)
	}

	// If multiple images found, return the most recent one
	var mostRecentImage *types.Image
	for i := range result.Images {
		image := &result.Images[i]
		if mostRecentImage == nil {
			mostRecentImage = image
			continue
		}

		// Compare creation dates
		if image.CreationDate != nil && mostRecentImage.CreationDate != nil {
			if *image.CreationDate > *mostRecentImage.CreationDate {
				mostRecentImage = image
			}
		}
	}

	if mostRecentImage == nil || mostRecentImage.ImageId == nil {
		return "", fmt.Errorf("no valid AMI found with name '%s'", imageName)
	}

	amiID := *mostRecentImage.ImageId

	// Cache the result
	p.amiCache.Set(p.region, imageName, amiID)

	return amiID, nil
}
