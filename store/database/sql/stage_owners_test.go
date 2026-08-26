package sql

import (
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestIsUniqueConstraintViolation(t *testing.T) {
	require.True(t, isUniqueConstraintViolation(&pq.Error{Code: "23505"}))
	require.True(t, isUniqueConstraintViolation(sqlite3.Error{
		Code:         sqlite3.ErrConstraint,
		ExtendedCode: sqlite3.ErrConstraintPrimaryKey,
	}))
	require.True(t, isUniqueConstraintViolation(sqlite3.Error{
		Code:         sqlite3.ErrConstraint,
		ExtendedCode: sqlite3.ErrConstraintUnique,
	}))
	require.False(t, isUniqueConstraintViolation(&pq.Error{Code: "08006"}))
	require.False(t, isUniqueConstraintViolation(errors.New("database unavailable")))
}
