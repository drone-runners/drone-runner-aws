// Copyright 2021 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package database

import (
	"github.com/drone-runners/drone-runner-aws/store"

	"github.com/google/wire"
	"github.com/jmoiron/sqlx"
)

// WireSet provides a wire set for this package
var WireSet = wire.NewSet(
	ProvideDatabase,
	ProvideInstanceStore,
)

// ProvideDatabase provides a database connection.
func ProvideDatabase(driver, datasource string) (*sqlx.DB, error) {
	return Connect(
		driver,
		datasource,
	)
}

// ProvideInstanceStore provides an instance store.
func ProvideInstanceStore(db *sqlx.DB) store.InstanceStore {
	switch db.DriverName() {
	case "postgres":
		return NewInstanceStore(db)
	default:
		return NewInstanceStoreSync(
			NewInstanceStore(db),
		)
	}
}

// ProvideInstanceStore provides an instance store.
func ProvideStageOwnerStore(db *sqlx.DB) store.StageOwnerStore {
	switch db.DriverName() {
	case "postgres":
		return NewStageOwnerStore(db)
	default:
		return NewStageOwnerStoreSync(
			NewStageOwnerStore(db),
		)
	}
}
