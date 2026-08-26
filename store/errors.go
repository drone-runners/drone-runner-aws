package store

import (
	"database/sql"
	"errors"

	"github.com/syndtr/goleveldb/leveldb"
)

var ErrConflict = errors.New("store conflict")

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, leveldb.ErrNotFound)
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}
