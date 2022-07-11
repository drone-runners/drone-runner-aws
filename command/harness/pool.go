package harness

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/poolfile"
	"github.com/sirupsen/logrus"
)

func SetupPool(ctx context.Context, env *config.EnvConfig, poolManager *drivers.Manager, poolFile string) error {
	configPool, confErr := poolfile.ConfigPoolFile(poolFile, env)
	if confErr != nil {
		logrus.WithError(confErr).Fatalln("Unable to load pool file, or use an in memory pool")
	}

	pools, err := poolfile.ProcessPool(configPool, env.Runner.Name)
	if err != nil {
		logrus.WithError(err).Errorln("dlite: unable to process pool file")
		return err
	}

	err = poolManager.Add(pools...)
	if err != nil {
		logrus.WithError(err).Errorln("dlite: unable to add pools")
		return err
	}

	err = poolManager.PingDriver(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: unable to ping driver")
		return err
	}

	// setup lifetimes of instances
	busyMaxAge := time.Hour * time.Duration(env.Settings.BusyMaxAge) // includes time required to setup an instance
	freeMaxAge := time.Hour * time.Duration(env.Settings.FreeMaxAge)
	err = poolManager.StartInstancePurger(ctx, busyMaxAge, freeMaxAge)
	if err != nil {
		logrus.WithError(err).
			Errorln("dlite: failed to start instance purger")
		return err
	}

	// lets remove any old instances.
	if !env.Settings.ReusePool {
		cleanErr := poolManager.CleanPools(ctx, true, true)
		if cleanErr != nil {
			logrus.WithError(cleanErr).
				Errorln("dlite: unable to clean pools")
		} else {
			logrus.Infoln("dlite: pools cleaned")
		}
	}
	// seed pools
	buildPoolErr := poolManager.BuildPools(ctx)
	if buildPoolErr != nil {
		logrus.WithError(buildPoolErr).
			Errorln("dlite: unable to build pool")
		return buildPoolErr
	}
	logrus.Infoln("dlite: pool created")
	return nil
}
