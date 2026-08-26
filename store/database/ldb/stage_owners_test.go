package ldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestStageOwnerCreateClassifiesDuplicateAsConflict(t *testing.T) {
	db, err := leveldb.OpenFile(t.TempDir(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	owners := NewStageOwnerStore(db)
	owner := &types.StageOwner{StageID: "stage", PoolName: "pool", InstanceID: "instance"}
	require.NoError(t, owners.Create(context.Background(), owner))
	err = owners.Create(context.Background(), owner)
	require.Error(t, err)
	require.True(t, store.IsConflict(err))
}
