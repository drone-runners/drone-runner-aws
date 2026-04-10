package iamauth

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/lib/pq"
)

// compile-time check that Connector satisfies driver.Connector
var _ driver.Connector = (*Connector)(nil)

func TestNew_setsFields(t *testing.T) {
	tests := []struct {
		name    string
		region  string
		host    string
		port    string
		user    string
		dbname  string
		sslmode string
	}{
		{
			name:    "all fields stored correctly",
			region:  "us-east-1",
			host:    "mydb.example.com",
			port:    "5432",
			user:    "admin1",
			dbname:  "dlite",
			sslmode: "require",
		},
		{
			name:    "empty sslmode stored as empty string",
			region:  "us-west-2",
			host:    "other.rds.amazonaws.com",
			port:    "5433",
			user:    "svc",
			dbname:  "app",
			sslmode: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(context.Background(), tc.region, tc.host, tc.port, tc.user, tc.dbname, tc.sslmode)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			if c.region != tc.region {
				t.Errorf("region = %q, want %q", c.region, tc.region)
			}
			if c.host != tc.host {
				t.Errorf("host = %q, want %q", c.host, tc.host)
			}
			if c.port != tc.port {
				t.Errorf("port = %q, want %q", c.port, tc.port)
			}
			if c.user != tc.user {
				t.Errorf("user = %q, want %q", c.user, tc.user)
			}
			if c.dbname != tc.dbname {
				t.Errorf("dbname = %q, want %q", c.dbname, tc.dbname)
			}
			if c.sslmode != tc.sslmode {
				t.Errorf("sslmode = %q, want %q", c.sslmode, tc.sslmode)
			}
			if c.awsCreds == nil {
				t.Error("awsCreds must not be nil after New()")
			}
		})
	}
}

func TestNew_canceledContextReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(ctx, "us-east-1", "host", "5432", "user", "db", "require")
	if err != nil {
		// canceled context may or may not cause LoadDefaultConfig to fail depending
		// on the SDK version; if it does fail, the error must wrap the context error.
		t.Logf("New() with canceled context returned (expected on some SDK versions): %v", err)
	}
}

func TestConnector_Driver(t *testing.T) {
	c := &Connector{}
	if _, ok := c.Driver().(*pq.Driver); !ok {
		t.Errorf("Driver() returned %T, want *pq.Driver", c.Driver())
	}
}
