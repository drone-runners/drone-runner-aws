package harness

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/poolfile"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/sirupsen/logrus"
)

// SetupPool sets up a pool of instances given a config pool.
func SetupPool(
	ctx context.Context,
	configPool *config.PoolFile,
	runnerName string,
	passwords types.Passwords,
	poolManager drivers.IManager,
	busyMaxAge int64,
	freeMaxAge int64,
	purgerTime int64,
	reusePool bool,
) (*config.PoolFile, error) {
	pools, err := poolfile.ProcessPool(configPool, runnerName, passwords)
	if err != nil {
		logrus.WithError(err).Errorln("unable to process pool file")
		return configPool, err
	}

	err = poolManager.Add(pools...)
	if err != nil {
		logrus.WithError(err).Errorln("unable to add pools")
		return configPool, err
	}

	err = poolManager.PingDriver(ctx)
	if err != nil {
		logrus.WithError(err).
			Errorln("unable to ping driver")
		return configPool, err
	}

	// setup lifetimes of instances
	busyMaxAgeDuration := time.Hour * time.Duration(busyMaxAge) // includes time required to setup an instance
	freeMaxAgeDuration := time.Hour * time.Duration(freeMaxAge)
	purgerDuration := time.Minute * time.Duration(purgerTime)
	err = poolManager.StartInstancePurger(ctx, busyMaxAgeDuration, freeMaxAgeDuration, purgerDuration)
	if err != nil {
		logrus.WithError(err).
			Errorln("failed to start instance purger")
		return configPool, err
	}
	// lets remove any old instances.
	if !reusePool {
		cleanErr := poolManager.CleanPools(ctx, true, true)
		if cleanErr != nil {
			return configPool, cleanErr
		}
		logrus.Infoln("pools cleaned")
	}
	// seed pools
	buildPoolErr := poolManager.BuildPools(ctx)
	if buildPoolErr != nil {
		logrus.WithError(buildPoolErr).
			Errorln("unable to build pool")
		return configPool, buildPoolErr
	}
	logrus.Infoln("pool created")
	return configPool, nil
}

func SetupPoolWithFile(
	ctx context.Context,
	poolFilePath string,
	poolManager drivers.IManager,
	passwords types.Passwords,
	runnerName string,
	busyAge,
	freeAge,
	purgerTime int64,
	reusePool bool,
) (*config.PoolFile, error) {
	configPool, err := config.ParseFile(poolFilePath)
	if err != nil {
		logrus.WithError(err).
			WithField("path", poolFilePath).
			Errorln("exec: unable to parse pool file")
		return nil, err
	}

	return SetupPool(ctx, configPool, runnerName, passwords, poolManager, busyAge, freeAge, purgerTime, reusePool)
}

func SetupPoolWithEnv(ctx context.Context, env *config.EnvConfig, poolManager drivers.IManager, poolFile string) (*config.PoolFile, error) {
	configPool, confErr := poolfile.ConfigPoolFile(poolFile, env)
	if confErr != nil {
		logrus.WithError(confErr).Fatalln("Unable to load pool file, or use an in memory pool")
	}

	return SetupPool(ctx, configPool, env.Runner.Name, env.Passwords(), poolManager, env.Settings.BusyMaxAge, env.Settings.FreeMaxAge, env.Settings.PurgerTime, env.Settings.ReusePool)

}

func Cleanup(reusePool bool, poolManager drivers.IManager, destroyBusy, destroyFree bool) error {
	if reusePool {
		return nil
	}

	cleanErr := poolManager.CleanPools(context.Background(), destroyBusy, destroyFree)
	if cleanErr != nil {
		logrus.WithError(cleanErr).Errorln("unable to clean pools")
	} else {
		logrus.Infoln("pools cleaned")
	}

	return cleanErr
}
