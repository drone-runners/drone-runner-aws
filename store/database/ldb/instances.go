package ldb

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"sort"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/sirupsen/logrus"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var _ store.InstanceStore = (*InstanceStore)(nil)

const keyPrefix = "inst-"

func NewInstanceStore(db *leveldb.DB) *InstanceStore {
	return &InstanceStore{db}
}

type InstanceStore struct {
	db *leveldb.DB
}

func (s InstanceStore) getKey(id string) string {
	return keyPrefix + id
}

func (s InstanceStore) Find(_ context.Context, id string) (*types.Instance, error) {
	key := s.getKey(id)
	data, err := s.db.Get([]byte(key), nil)
	if err != nil {
		return nil, err
	}

	dst := new(types.Instance)
	err = gob.NewDecoder(bytes.NewReader(data)).Decode(dst)
	return dst, err
}

func (s InstanceStore) List(_ context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
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

func (s InstanceStore) Create(ctx context.Context, instance *types.Instance) error {
	return s.Update(ctx, instance)
}

func (s InstanceStore) Delete(ctx context.Context, id string) error {
	key := s.getKey(id)
	return s.db.Delete([]byte(key), nil)
}

func (s InstanceStore) Update(_ context.Context, instance *types.Instance) error {
	key := s.getKey(instance.ID)
	var data bytes.Buffer
	enc := gob.NewEncoder(&data)
	if err := enc.Encode(instance); err != nil {
		return err
	}

	return s.db.Put([]byte(key), data.Bytes(), nil)
}

func (s InstanceStore) Purge(ctx context.Context) error {
	panic("implement me")
}

func (s InstanceStore) DeleteAndReturn(ctx context.Context, query string, args ...any) ([]*types.Instance, error) {
	panic("implement me")
}

func (s InstanceStore) satisfy(inst *types.Instance, pool string, params *types.QueryParams) bool {
	log := logrus.New()
	if pool == "" {
		return true
	}

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
		if len(params.MatchLabels) > 0 {
			var instanceLabels map[string]string
			err := json.Unmarshal(inst.Labels, &instanceLabels)
			if err != nil {
				log.Errorln("Error decoding instance labels json:", err)
				return false
			}
			for key, value := range params.MatchLabels {
				if instVal, ok := instanceLabels[key]; !ok || instVal != value {
					return false
				}
			}
		}
	}
	return true
}
