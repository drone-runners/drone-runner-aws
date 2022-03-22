package database

import (
	"database/sql"
	"time"

	"github.com/drone-runners/drone-runner-aws/store/database/migrate"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"           // required for postgres
	_ "github.com/mattn/go-sqlite3" // required for sqlite3
)

var builder = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

// Connect to a database and verify with a ping.
func Connect(driver, datasource string) (*sqlx.DB, error) {
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
			return
		}
		time.Sleep(time.Second)
	}
	return
}

// helper function to setup the databsae by performing automated
// database migration steps.
func setupDatabase(db *sqlx.DB) error {
	return migrate.Migrate(db)
}
