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
	ProvideSqlDatabase,
	ProvideSqlInstanceStore,
)

const SingleInstance = "singleinstance"

// ProvideSqlDatabase provides a database connection.
func ProvideSqlDatabase(driver, datasource string) (*sqlx.DB, error) {
	switch driver {
	case SingleInstance:
		// use a single instance db, as we only need one machine
		empty := sqlx.NewDb(nil, SingleInstance)

		return empty, nil
	default:
		return ConnectSql(
			driver,
			datasource,
		)
	}
}

// ProvideSqlInstanceStore provides an instance store.
func ProvideSqlInstanceStore(db *sqlx.DB) store.InstanceStore {
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
func ProvideSqlStageOwnerStore(db *sqlx.DB) store.StageOwnerStore {
	switch db.DriverName() {
	case "postgres":
		return sql.NewStageOwnerStore(db)
	default:
		return sql.NewStageOwnerStoreSync(
			sql.NewStageOwnerStore(db),
		)
	}
}

func ProvideStore(driver, datasource string) (store.InstanceStore, store.StageOwnerStore, error) {
	if driver == "leveldb" {
		db, err := leveldb.OpenFile(datasource, nil)
		if err != nil {
			return nil, nil, err
		}
		return ldb.NewInstanceStore(db), ldb.NewStageOwnerStore(db), nil
	}

	db, err := ProvideSqlDatabase(driver, datasource)
	if err != nil {
		return nil, nil, err
	}
	return ProvideSqlInstanceStore(db), ProvideSqlStageOwnerStore(db), nil
}
