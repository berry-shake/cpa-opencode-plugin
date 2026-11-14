package plugin

import "time"

// Snapshot seams: the only place the RPC snapshot tests reach into the
// Service's unexported state. Everything else drives the plugin through
// Service.Call and the host callbacks, so after the refactor only this file
// has to be rewritten against the new internals.

// snapshotClock is the time the service clock is pinned to. It shows up as
// the status "now", catalog and Go usage fetch times and the auth refresh
// horizon, and it keeps the account cache from expiring inside a case.
var snapshotClock = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// pinSnapshotClock fixes the service clock. The request log and the proxy
// store keep their own wall clocks; their timestamps are normalised instead.
func pinSnapshotClock(svc *Service) {
	svc.now = func() time.Time { return snapshotClock }
}

// holdBackgroundRefresh keeps plugin.register from starting the catalog
// refresh loop. A refresh that register queues is then run, or dropped, by
// the runner at a deterministic point.
func holdBackgroundRefresh(svc *Service) {
	svc.loopOnce.Do(func() {})
}

// dropQueuedRefresh discards a queued catalog refresh without running it.
func dropQueuedRefresh(svc *Service) {
	select {
	case <-svc.kick:
	default:
	}
}

// runQueuedRefresh runs, synchronously, the refresh the background loop
// would run for a queued kick, and reports whether one was queued.
func runQueuedRefresh(svc *Service) bool {
	select {
	case <-svc.kick:
		_, _ = svc.refreshCatalog(false)
		return true
	default:
		return false
	}
}

// waitForStreams blocks until every background stream pump has closed its
// host stream and written its request log entry.
func waitForStreams(svc *Service) {
	svc.streams.Wait()
}
