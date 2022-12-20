package ldb

import (
	"bytes"
	"context"
	"encoding/gob"
	"sort"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var _ store.InstanceStore = (*LevelDbStore)(nil)

const keyPrefix = "inst-"

func NewLevelDbStore(db *leveldb.DB) *LevelDbStore {
	return &LevelDbStore{db}
}

type LevelDbStore struct {
	db *leveldb.DB
}

func (s LevelDbStore) getKey(id string) string {
	return keyPrefix + id
}

func (s LevelDbStore) Find(_ context.Context, id string) (*types.Instance, error) {
	key := s.getKey(id)
	data, err := s.db.Get([]byte(key), nil)
	if err != nil {
		return nil, err
	}

	dst := new(types.Instance)
	err = gob.NewDecoder(bytes.NewReader(data)).Decode(dst)
	return dst, err
}

func (s LevelDbStore) List(_ context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
	instances := make([]*types.Instance, 0)

	iter := s.db.NewIterator(util.BytesPrefix([]byte(keyPrefix)), nil)
	defer iter.Release()
	for iter.Next() {
		value := iter.Value()

		inst := new(types.Instance)
		if err := gob.NewDecoder(bytes.NewReader(value)).Decode(inst); err != nil {
			return nil, err
		}

		if s.satisfy(inst, pool, params) {
			instances = append(instances, inst)
		}
	}

	iter.Release()
	if err := iter.Error(); err != nil {
		return nil, err
	}

	sort.Slice(instances, func(i, j int) bool {
		iTime := time.Unix(instances[i].Started, 0)
		jTime := time.Unix(instances[j].Started, 0)
		return iTime.Before(jTime)
	})

	return instances, nil
}

func (s LevelDbStore) Create(ctx context.Context, instance *types.Instance) error {
	return s.Update(ctx, instance)
}

func (s LevelDbStore) Delete(ctx context.Context, id string) error {
	key := s.getKey(id)
	return s.db.Delete([]byte(key), nil)
}

func (s LevelDbStore) Update(_ context.Context, instance *types.Instance) error {
	key := s.getKey(instance.ID)
	var data bytes.Buffer
	enc := gob.NewEncoder(&data)
	if err := enc.Encode(instance); err != nil {
		return err
	}

	return s.db.Put([]byte(key), data.Bytes(), nil)
}

func (s LevelDbStore) Purge(ctx context.Context) error {
	panic("implement me")
}

func (s LevelDbStore) satisfy(inst *types.Instance, pool string, params *types.QueryParams) bool {
	if inst.Pool != pool {
		return false
	}

	if params != nil {
		if params.Stage != "" {
			if inst.Stage != params.Stage {
				return false
			}
		}
		if params.Status != "" {
			if inst.State != params.Status {
				return false
			}
		}
	}
	return true
}
