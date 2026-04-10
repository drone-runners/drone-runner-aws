package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"github.com/drone-runners/drone-runner-aws/store/database/iamauth"
	"github.com/drone-runners/drone-runner-aws/store/database/migrate"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"           // required for postgres
	_ "github.com/mattn/go-sqlite3" // required for sqlite3
)

var _ = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

// ConnectSQL to a database and verify with a ping.
func ConnectSQL(driver, datasource string) (*sqlx.DB, error) {
	db, err := sql.Open(driver, datasource)
	if err != nil {
		return nil, err
	}
	dbx := sqlx.NewDb(db, driver)
	if err := pingDatabase(dbx); err != nil {
		return nil, err
	}
	if err := setupDatabase(dbx); err != nil {
		return nil, err
	}
	return dbx, nil
}

// ConnectSQLWithIAM opens a Postgres connection using RDS IAM authentication.
// A fresh IAM auth token is generated on every new physical connection the pool opens,
// so token expiry (15 min) is handled transparently without restarting the process.
// The datasource must not contain a password field.
func ConnectSQLWithIAM(ctx context.Context, datasource, region string) (*sqlx.DB, error) {
	host, port, user, dbname, sslmode, err := parseDSN(datasource)
	if err != nil {
		return nil, fmt.Errorf("iamauth: failed to parse datasource: %w", err)
	}

	connector, err := iamauth.New(ctx, region, host, port, user, dbname, sslmode)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	dbx := sqlx.NewDb(db, "postgres")

	if err := pingDatabase(dbx); err != nil {
		return nil, err
	}
	if err := setupDatabase(dbx); err != nil {
		return nil, err
	}
	return dbx, nil
}

//nolint:gocritic
func parseDSN(dsn string) (host, port, user, dbname, sslmode string, err error) {
	extract := func(key string) string {
		re := regexp.MustCompile(`(?:^|\s)` + key + `=(\S+)`)
		if m := re.FindStringSubmatch(dsn); len(m) > 1 {
			return m[1]
		}
		return ""
	}

	host = extract("host")
	port = extract("port")
	user = extract("user")
	dbname = extract("dbname")
	sslmode = extract("sslmode")

	if port == "" {
		port = "5432"
	}
	if host == "" {
		err = fmt.Errorf("missing 'host' in datasource")
	} else if user == "" {
		err = fmt.Errorf("missing 'user' in datasource")
	}
	return
}

// Must is a helper function that wraps a call to Connect
// and panics if the error is non-nil.
func Must(db *sqlx.DB, err error) *sqlx.DB {
	if err != nil {
		panic(err)
	}
	return db
}

// helper function to ping the database with backoff to ensure
// a connection can be established before we proceed with the
// database setup and migration.
func pingDatabase(db *sqlx.DB) (err error) {
	for i := 0; i < 30; i++ {
		err = db.Ping()
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return err
}

// helper function to setup the databsae by performing automated
// database migration steps.
func setupDatabase(db *sqlx.DB) error {
	return migrate.Migrate(db)
}
