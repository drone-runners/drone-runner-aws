package store

import (
	"database/sql"
	"errors"

	"github.com/syndtr/goleveldb/leveldb"
)

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, leveldb.ErrNotFound)
}
