package ldb

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/syndtr/goleveldb/leveldb"
)

var _ store.StageOwnerStore = (*LdbStageOwnerStore)(nil)

const ssKeyPrefix = "stage-owner-"

func NewLdbStageOwnerStore(db *leveldb.DB) *LdbStageOwnerStore {
	return &LdbStageOwnerStore{db}
}

type LdbStageOwnerStore struct {
	db *leveldb.DB
}

func (s LdbStageOwnerStore) getKey(id string) string {
	return ssKeyPrefix + id
}

func (s LdbStageOwnerStore) Find(_ context.Context, id, poolName string) (*types.StageOwner, error) {
	key := s.getKey(id)
	data, err := s.db.Get([]byte(key), nil)
	if err != nil {
		return nil, err
	}

	dst := new(types.StageOwner)
	if err = gob.NewDecoder(bytes.NewReader(data)).Decode(dst); err != nil {
		return nil, err
	}

	if dst.PoolName == poolName {
		return dst, nil
	}
	return nil, fmt.Errorf("Found stage id %s bound to different pool: %s from input: %s", id, dst.PoolName, poolName)
}

func (s LdbStageOwnerStore) Create(_ context.Context, stageOwner *types.StageOwner) error {
	key := s.getKey(stageOwner.StageID)
	var data bytes.Buffer
	enc := gob.NewEncoder(&data)
	if err := enc.Encode(stageOwner); err != nil {
		return err
	}

	return s.db.Put([]byte(key), data.Bytes(), nil)
}

func (s LdbStageOwnerStore) Delete(ctx context.Context, id string) error {
	key := s.getKey(id)
	return s.db.Delete([]byte(key), nil)
}

func (s LdbStageOwnerStore) Purge(ctx context.Context) error {
	panic("implement me")
}
