// Copyright 2021 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package database

import (
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database/ldb"
	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/store/singleinstance"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/google/wire"
	"github.com/jmoiron/sqlx"
)

// WireSet provides a wire set for this package
var WireSet = wire.NewSet(
	ProvideSQLDatabase,
	ProvideSQLInstanceStore,
)

const SingleInstance = "singleinstance"

// ProvideSQLDatabase provides a database connection.
func ProvideSQLDatabase(driver, datasource string) (*sqlx.DB, error) {
	switch driver {
	case SingleInstance:
		// use a single instance db, as we only need one machine
		empty := sqlx.NewDb(nil, SingleInstance)

		return empty, nil
	default:
		return ConnectSQL(
			driver,
			datasource,
		)
	}
}

// ProvideSQLInstanceStore provides an instance store.
func ProvideSQLInstanceStore(db *sqlx.DB) store.InstanceStore {
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

// ProvideSQLStageOwnerStore provides an stage owner store.
func ProvideSQLStageOwnerStore(db *sqlx.DB) store.StageOwnerStore {
	switch db.DriverName() {
	case "postgres":
		return sql.NewStageOwnerStore(db)
	default:
		return sql.NewStageOwnerStoreSync(
			sql.NewStageOwnerStore(db),
		)
	}
}

// ProvideSQLStageOwnerStore provides an stage owner store.
func ProvideSQLCapacityReservationStore(db *sqlx.DB) store.CapacityReservationStore {
	switch db.DriverName() {
	case "postgres":
		return sql.NewCapacityReservationStore(db)
	default:
		return sql.NewCapacityReservationStoreSync(
			sql.NewCapacityReservationStore(db),
		)
	}
}

func ProvideStore(driver, datasource string) (store.InstanceStore, store.StageOwnerStore, store.CapacityReservationStore, error) {
	if driver == "leveldb" {
		db, err := leveldb.OpenFile(datasource, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		return ldb.NewInstanceStore(db), ldb.NewStageOwnerStore(db), nil, nil
	}

	db, err := ProvideSQLDatabase(driver, datasource)
	if err != nil {
		return nil, nil, nil, err
	}
	return ProvideSQLInstanceStore(db), ProvideSQLStageOwnerStore(db), ProvideSQLCapacityReservationStore(db), nil
}
