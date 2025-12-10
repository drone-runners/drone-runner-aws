package ldb

import (
	"bytes"
	"context"
	"encoding/gob"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.StageOwnerStore = (*StageOwnerStore)(nil)

const ssKeyPrefix = "stage-owner-"

func NewStageOwnerStore(db *leveldb.DB) *StageOwnerStore {
	return &StageOwnerStore{db}
}

type StageOwnerStore struct {
	db *leveldb.DB
}

func (s StageOwnerStore) getKey(id string) string {
	return ssKeyPrefix + id
}

func (s StageOwnerStore) Find(_ context.Context, id string) (*types.StageOwner, error) {
	key := s.getKey(id)
	data, err := s.db.Get([]byte(key), nil)
	if err != nil {
		return nil, err
	}

	dst := new(types.StageOwner)
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(dst); err != nil {
		return nil, err
	}
	return dst, nil
}

func (s StageOwnerStore) Create(_ context.Context, stageOwner *types.StageOwner) error {
	key := s.getKey(stageOwner.StageID)
	var data bytes.Buffer
	enc := gob.NewEncoder(&data)
	if err := enc.Encode(stageOwner); err != nil {
		return err
	}

	return s.db.Put([]byte(key), data.Bytes(), nil)
}

func (s StageOwnerStore) Delete(ctx context.Context, id string) error {
	key := s.getKey(id)
	return s.db.Delete([]byte(key), nil)
}

func (s StageOwnerStore) Purge(ctx context.Context) error {
	panic("implement me")
}
