package amazon

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	drtypes "github.com/drone-runners/drone-runner-aws/types"
)

// TestNewAMICache tests AMI cache initialization
func TestNewAMICache(t *testing.T) {
	cache := NewAMICache()
	if cache == nil {
		t.Fatal("NewAMICache() returned nil")
	}
	if cache.cache == nil {
		t.Error("NewAMICache() cache map is nil")
	}
}

// TestAMICacheGetSet tests cache Get and Set operations
func TestAMICacheGetSet(t *testing.T) {
	cache := NewAMICache()
	region := "us-east-1"
	imageName := "ubuntu-22.04"
	amiID := "ami-1234567890abcdef0"

	// Test Get on empty cache
	_, found := cache.Get(region, imageName)
	if found {
		t.Error("Get() should return false for non-existent entry")
	}

	// Test Set
	cache.Set(region, imageName, amiID)

	// Test Get on populated cache
	got, found := cache.Get(region, imageName)
	if !found {
		t.Error("Get() should return true for existing entry")
	}
	if got != amiID {
		t.Errorf("Get() = %v, want %v", got, amiID)
	}
}

// TestAMICacheExpiration tests cache entry expiration
func TestAMICacheExpiration(t *testing.T) {
	cache := NewAMICache()
	region := "us-east-1"
	imageName := "ubuntu-22.04"
	amiID := "ami-1234567890abcdef0"

	// Set with custom expiration for testing
	cache.cache[region+":"+imageName] = &amiCacheEntry{
		amiID:    amiID,
		cachedAt: time.Now().Add(-2 * time.Hour), // Expired
		ttl:      1 * time.Hour,
	}

	// Should not return expired entry
	_, found := cache.Get(region, imageName)
	if found {
		t.Error("Get() should not return expired entry")
	}
}

// TestIsAMIID tests AMI ID validation
func TestIsAMIID(t *testing.T) {
	tests := []struct {
		name    string
		imageID string
		want    bool
	}{
		{"valid AMI ID", "ami-1234567890abcdef0", true},
		{"valid short AMI ID", "ami-12345678", true},
		{"invalid - no prefix", "1234567890abcdef0", false},
		{"invalid - too short", "ami-123", false},
		{"invalid - empty", "", false},
		{"invalid - wrong prefix", "snap-12345678", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAMIID(tt.imageID)
			if got != tt.want {
				t.Errorf("isAMIID(%v) = %v, want %v", tt.imageID, got, tt.want)
			}
		})
	}
}

// TestResolveImageNameToAMI tests AMI name resolution
func TestResolveImageNameToAMI(t *testing.T) {
	imageName := "ubuntu-22.04-*"
	amiID := "ami-1234567890abcdef0"
	creationDate := "2024-01-01T00:00:00.000Z"

	tests := []struct {
		name    string
		mock    *mockEC2Client
		wantErr bool
		wantAMI string
	}{
		{
			name: "successful resolution",
			mock: &mockEC2Client{
				DescribeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
					// Verify filters are set correctly
					if len(params.Filters) != 2 {
						t.Error("Expected 2 filters (name and state)")
					}
					return &ec2.DescribeImagesOutput{
						Images: []types.Image{
							{
								ImageId:      aws.String(amiID),
								Name:         aws.String(imageName),
								CreationDate: aws.String(creationDate),
							},
						},
					}, nil
				},
			},
			wantErr: false,
			wantAMI: amiID,
		},
		{
			name: "no AMI found",
			mock: &mockEC2Client{
				DescribeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
					return &ec2.DescribeImagesOutput{
						Images: []types.Image{},
					}, nil
				},
			},
			wantErr: true,
		},
		{
			name: "multiple AMIs - returns most recent",
			mock: &mockEC2Client{
				DescribeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
					return &ec2.DescribeImagesOutput{
						Images: []types.Image{
							{
								ImageId:      aws.String("ami-old"),
								Name:         aws.String(imageName),
								CreationDate: aws.String("2023-01-01T00:00:00.000Z"),
							},
							{
								ImageId:      aws.String(amiID),
								Name:         aws.String(imageName),
								CreationDate: aws.String(creationDate),
							},
						},
					}, nil
				},
			},
			wantErr: false,
			wantAMI: amiID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				service:  tt.mock,
				region:   "us-east-1",
				amiCache: NewAMICache(),
			}

			got, err := p.resolveImageNameToAMI(context.Background(), imageName)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveImageNameToAMI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantAMI {
				t.Errorf("resolveImageNameToAMI() = %v, want %v", got, tt.wantAMI)
			}

			// Test that result is cached
			if !tt.wantErr {
				cached, found := p.amiCache.Get(p.region, imageName)
				if !found {
					t.Error("Result should be cached")
				}
				if cached != tt.wantAMI {
					t.Errorf("Cached value = %v, want %v", cached, tt.wantAMI)
				}
			}
		})
	}
}

// TestGetFullyQualifiedImage tests image resolution with caching
func TestGetFullyQualifiedImage(t *testing.T) {
	tests := []struct {
		name      string
		imageName string
		mock      *mockEC2Client
		wantErr   bool
		isAMI     bool
	}{
		{
			name:      "direct AMI ID",
			imageName: "ami-1234567890abcdef0",
			mock:      &mockEC2Client{},
			wantErr:   false,
			isAMI:     true,
		},
		{
			name:      "resolve image name",
			imageName: "ubuntu-22.04",
			mock: &mockEC2Client{
				DescribeImagesFunc: func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
					return &ec2.DescribeImagesOutput{
						Images: []types.Image{
							{
								ImageId:      aws.String("ami-resolved"),
								Name:         aws.String("ubuntu-22.04"),
								CreationDate: aws.String("2024-01-01T00:00:00.000Z"),
							},
						},
					}, nil
				},
			},
			wantErr: false,
			isAMI:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				image:    tt.imageName,
				service:  tt.mock,
				region:   "us-east-1",
				amiCache: NewAMICache(),
			}

			config := &drtypes.VMImageConfig{
				ImageName: tt.imageName,
			}

			got, err := p.GetFullyQualifiedImage(context.Background(), config)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetFullyQualifiedImage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if tt.isAMI && got != tt.imageName {
					t.Errorf("GetFullyQualifiedImage() = %v, want %v", got, tt.imageName)
				}
				if !tt.isAMI && got == "" {
					t.Error("GetFullyQualifiedImage() returned empty string")
				}
			}
		})
	}
}
