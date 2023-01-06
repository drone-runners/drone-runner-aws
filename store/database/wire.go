// Copyright 2021 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package database

import (
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/store/singleinstance"

	"github.com/google/wire"
	"github.com/jmoiron/sqlx"
)

// WireSet provides a wire set for this package
var WireSet = wire.NewSet(
	ProvideDatabase,
	ProvideInstanceStore,
)

const SingleInstance = "singleinstance"

// ProvideDatabase provides a database connection.
func ProvideDatabase(driver, datasource string) (*sqlx.DB, error) {
	switch driver {
	case SingleInstance:
		// use a single instance db, as we only need one machine
		empty := sqlx.NewDb(nil, SingleInstance)

		return empty, nil
	default:
		return Connect(
			driver,
			datasource,
		)
	}
}

// ProvideInstanceStore provides an instance store.
func ProvideInstanceStore(db *sqlx.DB) store.InstanceStore {
	switch db.DriverName() {
	case "postgres":
		return sql.NewInstanceStore(db)
	case SingleInstance:
		// this is a store with a single instance, used by exec and setup commands
		return singleinstance.NewSingleInstanceStore(db)
	default:
		return sql.NewInstanceStoreSync(
			sql.NewInstanceStore(db),
		)
	}
}

// ProvideInstanceStore provides an instance store.
func ProvideStageOwnerStore(db *sqlx.DB) store.StageOwnerStore {
	switch db.DriverName() {
	case "postgres":
		return sql.NewStageOwnerStore(db)
	default:
		return sql.NewStageOwnerStoreSync(
			sql.NewStageOwnerStore(db),
		)
	}
}
