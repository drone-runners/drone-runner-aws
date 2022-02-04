package vmpool

type Strategy interface {
	CountCreateRemove(minSize, maxSize, busyCount, freeCount int) (shouldCreate, shouldRemove int)
	CanCreate(minSize, maxSize, busyCount, freeCount int) bool
}

// Greedy is a pool size management strategy that ignores busy instances,
// never removes any instance and will always create a new instance if it's needed.
type Greedy struct{}

func (Greedy) CountCreateRemove(minSize, maxSize, busyCount, freeCount int) (shouldCreate, shouldRemove int) {
	// Enables that in the pool there are always at least `minSize` of free instances.
	instanceCount := freeCount
	if minSize > instanceCount {
		shouldCreate = minSize - instanceCount
	}
	return
}

func (Greedy) CanCreate(minSize, maxSize, busyCount, freeCount int) bool {
	// It is always possible to create a new instance if the pool does not contain an available free instance.
	return true
}

// MinMax is a pool size management strategy then keeps total number of instances between minimum and maximum value.
type MinMax struct{}

func (MinMax) CountCreateRemove(minSize, maxSize, busyCount, freeCount int) (shouldCreate, shouldRemove int) {
	// The implementation will remove free instances above number of maxPool size. This situation can
	// happen only if the runner is restarted with new configuration that allows a smaller maxPool size.
	// Also, if the pool size is less than the required minimum, it will say how many new instance should be created.

	if minSize < 0 {
		minSize = 0
	}
	if maxSize <= 0 {
		maxSize = 1
	}
	if minSize > maxSize {
		minSize = 0
	}

	instanceCount := busyCount + freeCount

	if instanceCount > maxSize {
		shouldRemove = instanceCount - maxSize
		if freeCount < shouldRemove {
			shouldRemove = freeCount
		}
	} else if instanceCount < minSize {
		shouldCreate = minSize - instanceCount
	}

	return
}

func (MinMax) CanCreate(minSize, maxSize, busyCount, freeCount int) bool {
	// Creating a new instance is allowed only if total number of instances is less than maxSize.
	instanceCount := busyCount + freeCount
	return instanceCount < maxSize
}
