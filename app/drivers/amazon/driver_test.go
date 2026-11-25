package amazon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	drtypes "github.com/drone-runners/drone-runner-aws/types"
)

// mockEC2Client is a mock implementation of the EC2 client for testing
type mockEC2Client struct {
	DescribeRegionsFunc               func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
	RunInstancesFunc                  func(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstancesFunc             func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstancesFunc            func(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	StopInstancesFunc                 func(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstancesFunc                func(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeImagesFunc                func(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	CreateSecurityGroupFunc           func(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DescribeSecurityGroupsFunc        func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	AuthorizeSecurityGroupIngressFunc func(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	CreateVolumeFunc                  func(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
	DescribeVolumesFunc               func(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	AttachVolumeFunc                  func(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error)
	DeleteVolumeFunc                  func(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
	CreateTagsFunc                    func(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	GetConsoleOutputFunc              func(ctx context.Context, params *ec2.GetConsoleOutputInput, optFns ...func(*ec2.Options)) (*ec2.GetConsoleOutputOutput, error)
	CreateCapacityReservationFunc     func(ctx context.Context, params *ec2.CreateCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CreateCapacityReservationOutput, error)
	CancelCapacityReservationFunc     func(ctx context.Context, params *ec2.CancelCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CancelCapacityReservationOutput, error)
}

func (m *mockEC2Client) DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	if m.DescribeRegionsFunc != nil {
		return m.DescribeRegionsFunc(ctx, params, optFns...)
	}
	return &ec2.DescribeRegionsOutput{}, nil
}

func (m *mockEC2Client) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	if m.RunInstancesFunc != nil {
		return m.RunInstancesFunc(ctx, params, optFns...)
	}
	return &ec2.RunInstancesOutput{}, nil
}

func (m *mockEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if m.DescribeInstancesFunc != nil {
		return m.DescribeInstancesFunc(ctx, params, optFns...)
	}
	return &ec2.DescribeInstancesOutput{}, nil
}

func (m *mockEC2Client) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	if m.TerminateInstancesFunc != nil {
		return m.TerminateInstancesFunc(ctx, params, optFns...)
	}
	return &ec2.TerminateInstancesOutput{}, nil
}

func (m *mockEC2Client) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	if m.StopInstancesFunc != nil {
		return m.StopInstancesFunc(ctx, params, optFns...)
	}
	return &ec2.StopInstancesOutput{}, nil
}

func (m *mockEC2Client) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	if m.StartInstancesFunc != nil {
		return m.StartInstancesFunc(ctx, params, optFns...)
	}
	return &ec2.StartInstancesOutput{}, nil
}

func (m *mockEC2Client) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if m.DescribeImagesFunc != nil {
		return m.DescribeImagesFunc(ctx, params, optFns...)
	}
	return &ec2.DescribeImagesOutput{}, nil
}

func (m *mockEC2Client) CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	if m.CreateSecurityGroupFunc != nil {
		return m.CreateSecurityGroupFunc(ctx, params, optFns...)
	}
	return &ec2.CreateSecurityGroupOutput{}, nil
}

func (m *mockEC2Client) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	if m.DescribeSecurityGroupsFunc != nil {
		return m.DescribeSecurityGroupsFunc(ctx, params, optFns...)
	}
	return &ec2.DescribeSecurityGroupsOutput{}, nil
}

func (m *mockEC2Client) AuthorizeSecurityGroupIngress(
	ctx context.Context,
	params *ec2.AuthorizeSecurityGroupIngressInput,
	optFns ...func(*ec2.Options),
) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	if m.AuthorizeSecurityGroupIngressFunc != nil {
		return m.AuthorizeSecurityGroupIngressFunc(ctx, params, optFns...)
	}
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, nil
}

func (m *mockEC2Client) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	if m.CreateVolumeFunc != nil {
		return m.CreateVolumeFunc(ctx, params, optFns...)
	}
	return &ec2.CreateVolumeOutput{}, nil
}

func (m *mockEC2Client) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if m.DescribeVolumesFunc != nil {
		return m.DescribeVolumesFunc(ctx, params, optFns...)
	}
	return &ec2.DescribeVolumesOutput{}, nil
}

func (m *mockEC2Client) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	if m.AttachVolumeFunc != nil {
		return m.AttachVolumeFunc(ctx, params, optFns...)
	}
	return &ec2.AttachVolumeOutput{}, nil
}

func (m *mockEC2Client) DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	if m.DeleteVolumeFunc != nil {
		return m.DeleteVolumeFunc(ctx, params, optFns...)
	}
	return &ec2.DeleteVolumeOutput{}, nil
}

func (m *mockEC2Client) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	if m.CreateTagsFunc != nil {
		return m.CreateTagsFunc(ctx, params, optFns...)
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (m *mockEC2Client) GetConsoleOutput(ctx context.Context, params *ec2.GetConsoleOutputInput, optFns ...func(*ec2.Options)) (*ec2.GetConsoleOutputOutput, error) {
	if m.GetConsoleOutputFunc != nil {
		return m.GetConsoleOutputFunc(ctx, params, optFns...)
	}
	return &ec2.GetConsoleOutputOutput{}, nil
}

func (m *mockEC2Client) CreateCapacityReservation(ctx context.Context, params *ec2.CreateCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CreateCapacityReservationOutput, error) {
	if m.CreateCapacityReservationFunc != nil {
		return m.CreateCapacityReservationFunc(ctx, params, optFns...)
	}
	return &ec2.CreateCapacityReservationOutput{}, nil
}

func (m *mockEC2Client) CancelCapacityReservation(ctx context.Context, params *ec2.CancelCapacityReservationInput, optFns ...func(*ec2.Options)) (*ec2.CancelCapacityReservationOutput, error) {
	if m.CancelCapacityReservationFunc != nil {
		return m.CancelCapacityReservationFunc(ctx, params, optFns...)
	}
	return &ec2.CancelCapacityReservationOutput{}, nil
}

// TestPing tests the Ping method with AWS SDK v2
func TestPing(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockEC2Client
		wantErr bool
	}{
		{
			name: "successful ping",
			mock: &mockEC2Client{
				DescribeRegionsFunc: func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
					return &ec2.DescribeRegionsOutput{
						Regions: []types.Region{
							{RegionName: aws.String("us-east-1")},
						},
					}, nil
				},
			},
			wantErr: false,
		},
		{
			name: "failed ping",
			mock: &mockEC2Client{
				DescribeRegionsFunc: func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
					return nil, errors.New("connection failed")
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				service: tt.mock,
			}
			err := p.Ping(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Ping() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestGetState tests the getState method with SDK v2 types
func TestGetState(t *testing.T) {
	tests := []struct {
		name     string
		instance *types.Instance
		want     string
	}{
		{
			name: "nil state",
			instance: &types.Instance{
				State: nil,
			},
			want: "",
		},
		{
			name: "running state",
			instance: &types.Instance{
				State: &types.InstanceState{
					Name: types.InstanceStateNameRunning,
				},
			},
			want: "running",
		},
		{
			name: "stopped state",
			instance: &types.Instance{
				State: &types.InstanceState{
					Name: types.InstanceStateNameStopped,
				},
			},
			want: "stopped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{}
			got := p.getState(tt.instance)
			if got != tt.want {
				t.Errorf("getState() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetIP tests the getIP method
func TestGetIP(t *testing.T) {
	tests := []struct {
		name          string
		allocPublicIP bool
		instance      *types.Instance
		want          string
	}{
		{
			name:          "get public IP",
			allocPublicIP: true,
			instance: &types.Instance{
				PublicIpAddress: aws.String("1.2.3.4"),
			},
			want: "1.2.3.4",
		},
		{
			name:          "get private IP",
			allocPublicIP: false,
			instance: &types.Instance{
				PrivateIpAddress: aws.String("10.0.0.1"),
			},
			want: "10.0.0.1",
		},
		{
			name:          "no public IP",
			allocPublicIP: true,
			instance: &types.Instance{
				PublicIpAddress: nil,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				allocPublicIP: tt.allocPublicIP,
			}
			got := p.getIP(tt.instance)
			if got != tt.want {
				t.Errorf("getIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsHibernateRetryable tests the isHibernateRetryable function with smithy.APIError
func TestIsHibernateRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "UnsupportedOperation error",
			err: &smithy.GenericAPIError{
				Code:    "UnsupportedOperation",
				Message: "Instance is not ready to hibernate yet",
			},
			want: true,
		},
		{
			name: "other API error",
			err: &smithy.GenericAPIError{
				Code:    "InvalidInstanceID.NotFound",
				Message: "Instance not found",
			},
			want: false,
		},
		{
			name: "non-API error",
			err:  errors.New("some other error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHibernateRetryable(tt.err)
			if got != tt.want {
				t.Errorf("isHibernateRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHibernate tests the Hibernate method with SDK v2
func TestHibernate(t *testing.T) {
	tests := []struct {
		name      string
		hibernate bool
		mock      *mockEC2Client
		wantErr   bool
	}{
		{
			name:      "successful hibernate",
			hibernate: true,
			mock: &mockEC2Client{
				StopInstancesFunc: func(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
					assert.Equal(t, true, *params.Hibernate)
					return &ec2.StopInstancesOutput{}, nil
				},
			},
			wantErr: false,
		},
		{
			name:      "UnsupportedOperation error - retryable",
			hibernate: true,
			mock: &mockEC2Client{
				StopInstancesFunc: func(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
					return nil, &smithy.GenericAPIError{
						Code:    "UnsupportedOperation",
						Message: "Instance is not ready to hibernate yet",
					}
				},
			},
			wantErr: true, // Should return error but it's retryable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				service:   tt.mock,
				hibernate: tt.hibernate,
			}
			err := p.Hibernate(context.Background(), "i-1234567890abcdef0", "test-pool", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("Hibernate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestGetInstance tests the getInstance method
func TestGetInstance(t *testing.T) {
	instanceID := "i-1234567890abcdef0"

	tests := []struct {
		name    string
		mock    *mockEC2Client
		wantErr bool
	}{
		{
			name: "successful get instance",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{
							{
								Instances: []types.Instance{
									{
										InstanceId: aws.String(instanceID),
										State: &types.InstanceState{
											Name: types.InstanceStateNameRunning,
										},
									},
								},
							},
						},
					}, nil
				},
			},
			wantErr: false,
		},
		{
			name: "empty reservations",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{},
					}, nil
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				service: tt.mock,
			}
			instance, err := p.getInstance(context.Background(), instanceID)
			if (err != nil) != tt.wantErr {
				t.Errorf("getInstance() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && instance == nil {
				t.Error("getInstance() returned nil instance")
			}
		})
	}
}

// TestGetNextDeviceName tests device name generation
func TestGetNextDeviceName(t *testing.T) {
	p := &amazonConfig{}

	tests := []struct {
		index int
		want  string
	}{
		{0, "/dev/sde"},
		{1, "/dev/sdf"},
		{2, "/dev/sdg"},
		{25, "/dev/sde"}, // wraps around
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := p.getNextDeviceName(tt.index)
			if got != tt.want {
				t.Errorf("getNextDeviceName(%d) = %v, want %v", tt.index, got, tt.want)
			}
		})
	}
}

// TestGetLaunchTime tests the getLaunchTime method
func TestGetLaunchTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		instance *types.Instance
		wantNil  bool
	}{
		{
			name: "with launch time",
			instance: &types.Instance{
				LaunchTime: &now,
			},
			wantNil: false,
		},
		{
			name: "nil launch time",
			instance: &types.Instance{
				LaunchTime: nil,
			},
			wantNil: false, // Should return time.Now()
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{}
			got := p.getLaunchTime(tt.instance)
			if tt.instance.LaunchTime != nil {
				if !got.Equal(now) {
					t.Errorf("getLaunchTime() = %v, want %v", got, now)
				}
			} else {
				// Should be close to now
				if time.Since(got) > time.Second {
					t.Error("getLaunchTime() should return current time when LaunchTime is nil")
				}
			}
		})
	}
}

// TestDriverName tests the DriverName method
func TestDriverName(t *testing.T) {
	p := &amazonConfig{}
	got := p.DriverName()
	want := string(drtypes.Amazon)
	if got != want {
		t.Errorf("DriverName() = %v, want %v", got, want)
	}
}

// TestCanHibernate tests the CanHibernate method
func TestCanHibernate(t *testing.T) {
	tests := []struct {
		name      string
		hibernate bool
		want      bool
	}{
		{"hibernate enabled", true, true},
		{"hibernate disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{hibernate: tt.hibernate}
			if got := p.CanHibernate(); got != tt.want {
				t.Errorf("CanHibernate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetDynamicConfig_RoundRobinEvenDistribution tests that round-robin distributes
// evenly across zones when called sequentially.
func TestGetDynamicConfig_RoundRobinEvenDistribution(t *testing.T) {
	zones := []cf.ZoneInfo{
		{AvailabilityZone: "us-east-1a", SubnetID: "subnet-aaa"},
		{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
		{AvailabilityZone: "us-east-1c", SubnetID: "subnet-ccc"},
	}

	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      zones,
		zoneIndex:        0,
	}

	counts := map[string]int{}
	totalCalls := 30

	for i := 0; i < totalCalls; i++ {
		cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{
			PoolName: "test-pool",
		})
		assert.NoError(t, err)
		counts[cfg.availabilityZone]++
	}

	// Each zone should get exactly totalCalls/numZones = 10
	for _, zone := range zones {
		assert.Equal(t, totalCalls/len(zones), counts[zone.AvailabilityZone],
			"zone %s should get %d calls", zone.AvailabilityZone, totalCalls/len(zones))
	}
}

// TestGetDynamicConfig_RoundRobinConcurrent tests even distribution under concurrent
// goroutine access — the exact bug scenario that atomic.AddUint64 fixes.
func TestGetDynamicConfig_RoundRobinConcurrent(t *testing.T) {
	zones := []cf.ZoneInfo{
		{AvailabilityZone: "us-east-1a", SubnetID: "subnet-aaa"},
		{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
		{AvailabilityZone: "us-east-1c", SubnetID: "subnet-ccc"},
	}

	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      zones,
		zoneIndex:        0,
	}

	var mu sync.Mutex
	counts := map[string]int{}
	numGoroutines := 300

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{
				PoolName: "test-pool",
			})
			assert.NoError(t, err)
			mu.Lock()
			counts[cfg.availabilityZone]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Each zone should get exactly numGoroutines/numZones = 100
	for _, zone := range zones {
		assert.Equal(t, numGoroutines/len(zones), counts[zone.AvailabilityZone],
			"zone %s should get %d calls under concurrency", zone.AvailabilityZone, numGoroutines/len(zones))
	}
}

// TestGetDynamicConfig_SingleZone tests round-robin with only one zone.
func TestGetDynamicConfig_SingleZone(t *testing.T) {
	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails: []cf.ZoneInfo{
			{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
		},
		zoneIndex: 0,
	}

	for i := 0; i < 5; i++ {
		cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{PoolName: "test-pool"})
		assert.NoError(t, err)
		assert.Equal(t, "us-east-1b", cfg.availabilityZone)
		assert.Equal(t, "subnet-bbb", cfg.subnet)
	}
}

// TestGetDynamicConfig_EmptyZoneDetails tests fallback to pool defaults when no
// zoneDetails are configured.
func TestGetDynamicConfig_EmptyZoneDetails(t *testing.T) {
	p := &amazonConfig{
		availabilityZone: "us-west-2a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      nil,
	}

	cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{PoolName: "test-pool"})
	assert.NoError(t, err)
	assert.Equal(t, "us-west-2a", cfg.availabilityZone, "should use pool default zone")
	assert.Equal(t, "subnet-default", cfg.subnet, "should use pool default subnet")
}

// TestGetDynamicConfig_ZonePriority tests that request zones take priority over
// capacity reservation zone, which takes priority over round-robin.
func TestGetDynamicConfig_ZonePriority(t *testing.T) {
	zones := []cf.ZoneInfo{
		{AvailabilityZone: "us-east-1a", SubnetID: "subnet-aaa"},
		{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
		{AvailabilityZone: "us-east-1c", SubnetID: "subnet-ccc"},
	}

	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      zones,
		zoneIndex:        0,
	}

	// Request zone takes top priority
	cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{
		PoolName: "test-pool",
		Zones:    []string{"us-east-1c"},
		CapacityReservation: &drtypes.CapacityReservation{
			Zone: drtypes.StringPtr("us-east-1b"),
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, "us-east-1c", cfg.availabilityZone, "request zone should take priority")
	assert.Equal(t, "subnet-ccc", cfg.subnet, "should match subnet for requested zone")

	// Capacity reservation zone is second priority
	cfg, err = p.getDynamicConfig(&drtypes.InstanceCreateOpts{
		PoolName: "test-pool",
		CapacityReservation: &drtypes.CapacityReservation{
			Zone: drtypes.StringPtr("us-east-1b"),
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, "us-east-1b", cfg.availabilityZone, "capacity reservation zone should be used when no request zone")
	assert.Equal(t, "subnet-bbb", cfg.subnet, "should match subnet for capacity reservation zone")
}

// TestGetDynamicConfig_SubnetMatchForRequestedZone tests that when a zone is requested,
// the correct subnet is looked up from zoneDetails.
func TestGetDynamicConfig_SubnetMatchForRequestedZone(t *testing.T) {
	zones := []cf.ZoneInfo{
		{AvailabilityZone: "us-east-1a", SubnetID: "subnet-aaa"},
		{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
	}

	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      zones,
	}

	// Requesting a zone that exists in zoneDetails should find the matching subnet
	cfg, err := p.getDynamicConfig(&drtypes.InstanceCreateOpts{
		PoolName: "test-pool",
		Zones:    []string{"us-east-1b"},
	})
	assert.NoError(t, err)
	assert.Equal(t, "us-east-1b", cfg.availabilityZone)
	assert.Equal(t, "subnet-bbb", cfg.subnet, "should match subnet from zoneDetails")

	// Requesting a zone NOT in zoneDetails should fall back to pool default subnet
	cfg, err = p.getDynamicConfig(&drtypes.InstanceCreateOpts{
		PoolName: "test-pool",
		Zones:    []string{"us-east-1z"},
	})
	assert.NoError(t, err)
	assert.Equal(t, "us-east-1z", cfg.availabilityZone)
	assert.Equal(t, "subnet-default", cfg.subnet, "should fall back to pool default subnet for unknown zone")
}

// TestGetDynamicConfig_RoundRobinSetsSubnet verifies that round-robin sets both
// the zone and its corresponding subnet from zoneDetails.
func TestGetDynamicConfig_RoundRobinSetsSubnet(t *testing.T) {
	zones := []cf.ZoneInfo{
		{AvailabilityZone: "us-east-1a", SubnetID: "subnet-aaa"},
		{AvailabilityZone: "us-east-1b", SubnetID: "subnet-bbb"},
	}

	p := &amazonConfig{
		availabilityZone: "us-east-1a",
		subnet:           "subnet-default",
		size:             "m5.xlarge",
		volumeSize:       100,
		volumeType:       "gp3",
		zoneDetails:      zones,
		zoneIndex:        0,
	}

	cfg1, _ := p.getDynamicConfig(&drtypes.InstanceCreateOpts{PoolName: "test-pool"})
	cfg2, _ := p.getDynamicConfig(&drtypes.InstanceCreateOpts{PoolName: "test-pool"})

	assert.Equal(t, "us-east-1a", cfg1.availabilityZone)
	assert.Equal(t, "subnet-aaa", cfg1.subnet)
	assert.Equal(t, "us-east-1b", cfg2.availabilityZone)
	assert.Equal(t, "subnet-bbb", cfg2.subnet)
}

// TestStart tests the Start method for hibernate resume behavior
func TestStart(t *testing.T) {
	instanceID := "i-1234567890abcdef0"

	tests := []struct {
		name    string
		mock    *mockEC2Client
		wantErr bool
		wantIP  string
		errMsg  string
	}{
		{
			name: "instance already running",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{
							{
								Instances: []types.Instance{
									{
										InstanceId:       aws.String(instanceID),
										PrivateIpAddress: aws.String("10.0.0.1"),
										State: &types.InstanceState{
											Name: types.InstanceStateNameRunning,
										},
									},
								},
							},
						},
					}, nil
				},
			},
			wantErr: false,
			wantIP:  "10.0.0.1",
		},
		{
			name: "instance terminated - cannot start",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{
							{
								Instances: []types.Instance{
									{
										InstanceId: aws.String(instanceID),
										State: &types.InstanceState{
											Name: types.InstanceStateNameTerminated,
										},
									},
								},
							},
						},
					}, nil
				},
			},
			wantErr: true,
			errMsg:  "terminal state",
		},
		{
			name: "instance shutting-down - cannot start",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{
							{
								Instances: []types.Instance{
									{
										InstanceId: aws.String(instanceID),
										State: &types.InstanceState{
											Name: types.InstanceStateNameShuttingDown,
										},
									},
								},
							},
						},
					}, nil
				},
			},
			wantErr: true,
			errMsg:  "terminal state",
		},
		{
			name: "StartInstances API fails",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return &ec2.DescribeInstancesOutput{
						Reservations: []types.Reservation{
							{
								Instances: []types.Instance{
									{
										InstanceId: aws.String(instanceID),
										State: &types.InstanceState{
											Name: types.InstanceStateNameStopped,
										},
									},
								},
							},
						},
					}, nil
				},
				StartInstancesFunc: func(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
					return nil, errors.New("insufficient capacity")
				},
			},
			wantErr: true,
			errMsg:  "insufficient capacity",
		},
		{
			name: "getInstance fails",
			mock: &mockEC2Client{
				DescribeInstancesFunc: func(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
					return nil, errors.New("describe failed")
				},
			},
			wantErr: true,
			errMsg:  "failed to get instance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &amazonConfig{
				service: tt.mock,
			}
			instance := &drtypes.Instance{
				ID: instanceID,
			}
			ip, err := p.Start(context.Background(), instance, "test-pool")
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantIP, ip)
			}
		})
	}
}
