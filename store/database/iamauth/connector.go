package iamauth

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// Connector is a database/sql driver.Connector that generates a fresh
// RDS IAM auth token on every new physical connection. This ensures the
// token (which expires every 15 minutes) is always valid regardless of
// how long the connection pool has been running.
//
// The AWS credentials provider is loaded once at construction time. The SDK
// handles refreshing the underlying IRSA/STS credentials automatically.
type Connector struct {
	region   string
	host     string
	port     string
	user     string
	dbname   string
	sslmode  string
	awsCreds aws.CredentialsProvider
}

// New creates a new IAM-auth Connector. It loads AWS config once at
// construction so the credentials provider can be reused across connections.
func New(ctx context.Context, region, host, port, user, dbname, sslmode string) (*Connector, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("iamauth: failed to load AWS config: %w", err)
	}
	return &Connector{
		region:   region,
		host:     host,
		port:     port,
		user:     user,
		dbname:   dbname,
		sslmode:  sslmode,
		awsCreds: cfg.Credentials,
	}, nil
}

// Connect implements driver.Connector. Called by database/sql every time it
// needs to open a new physical connection to the database.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	token, err := c.generateToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("iamauth: failed to generate RDS auth token: %w", err)
	}
	logrus.Infoln("iamauth: generated RDS IAM auth token successfully")

	// Build a postgres:// URL so net/url properly percent-encodes the password.
	// The IAM token contains =, &, % characters that break keyword=value DSN parsing.
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.user, token),
		Host:   fmt.Sprintf("%s:%s", c.host, c.port),
		Path:   "/" + c.dbname,
	}
	q := url.Values{}
	if c.sslmode != "" {
		q.Set("sslmode", c.sslmode)
	}
	u.RawQuery = q.Encode()

	return pq.Open(u.String())
}

// Driver implements driver.Connector.
func (c *Connector) Driver() driver.Driver {
	return &pq.Driver{}
}

// generateToken calls the AWS SDK to produce a short-lived RDS IAM auth token.
// The token is a pre-signed URL valid for 15 minutes, so it must be regenerated
// on every new connection. The credentials provider it uses is long-lived and
// handles STS/IRSA refresh automatically.
func (c *Connector) generateToken(ctx context.Context) (string, error) {
	endpoint := fmt.Sprintf("%s:%s", c.host, c.port)
	token, err := auth.BuildAuthToken(ctx, endpoint, c.region, c.user, c.awsCreds)
	if err != nil {
		return "", fmt.Errorf("iamauth: failed to build auth token for %s: %w", endpoint, err)
	}
	return token, nil
}
