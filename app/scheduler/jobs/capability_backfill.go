package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/store"
)

// BackfillCapabilityHistory copies pool utilization history into the capability history table
// for every pool in the catalog. Only one process runs it at a time, and re-running it is a no-op
// for pools that were already copied. It returns the number of rows inserted.
func BackfillCapabilityHistory(ctx context.Context, s store.CapabilityHistoryStore, catalog *capability.Catalog) (int64, error) {
	release, ok, err := s.TryLockBackfill(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		logrus.Infoln("capability backfill: another runner holds the lock, skipping")
		return 0, nil
	}
	defer release()

	notAfter := time.Now().Unix()
	var total int64
	for _, p := range catalog.Pools() {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		started := time.Now()
		rows, err := s.BackfillPool(ctx, p.Name, p.Tier, p.OS, p.Arch, notAfter)
		if err != nil {
			return total, err
		}
		total += rows
		logrus.WithFields(logrus.Fields{
			logFieldPool: p.Name,
			"tier":       p.Tier,
			"os":         p.OS,
			"arch":       p.Arch,
			"rows":       rows,
			"duration":   time.Since(started).String(),
		}).Infoln("capability backfill: pool done")
	}
	logrus.WithField("rows", total).Infoln("capability backfill: complete")
	return total, nil
}
