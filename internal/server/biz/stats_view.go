package biz

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
)

// dashboardStatsViews are the materialized views the dashboard resolvers read
// instead of the multi-terabyte detail tables.
var dashboardStatsViews = []string{
	"usage_stats_daily",
	"request_stats_daily",
	"execution_stats_daily",
}

const (
	// dashboardStatsRefreshInterval is how often the aggregates are refreshed.
	//
	// Five minutes keeps "today" close to live while staying well above the time a
	// full refresh takes (~50s for all three views on the production dataset). A
	// run that overruns the interval does not start another one; see loop.
	dashboardStatsRefreshInterval = 5 * time.Minute

	// dashboardStatsRefreshTimeout bounds a single refresh so a stuck refresh
	// cannot hold a connection forever.
	dashboardStatsRefreshTimeout = 5 * time.Minute

	// dashboardStatsRefreshEnv turns the refresher off when set to "disabled".
	dashboardStatsRefreshEnv = "AXONHUB_DASHBOARD_STATS_VIEW_REFRESH"
)

// StatsViewRefresher refreshes the dashboard materialized views once a day.
//
// This must never run on the request path: it exists so the dashboard resolvers
// can read a few thousand pre-aggregated rows instead of scanning the detail
// tables on every page load.
type StatsViewRefresher struct {
	client *ent.Client
	cancel context.CancelFunc

	// running guards the refresh: at most one run at a time, and a tick that
	// arrives while a run is still in flight is skipped instead of queued.
	running atomic.Bool
}

// NewStatsViewRefresher creates the refresher.
func NewStatsViewRefresher(client *ent.Client) *StatsViewRefresher {
	return &StatsViewRefresher{client: client}
}

// Start launches the refresh loop.
func (r *StatsViewRefresher) Start(ctx context.Context) error {
	if os.Getenv(dashboardStatsRefreshEnv) == "disabled" {
		log.Info(ctx, "dashboard stats view refresher disabled", log.String("env", dashboardStatsRefreshEnv))

		return nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	go r.loop(runCtx)

	log.Info(ctx, "dashboard stats view refresher started",
		log.String("interval", dashboardStatsRefreshInterval.String()),
		log.Int("views", len(dashboardStatsViews)))

	return nil
}

// Stop stops the refresh loop.
func (r *StatsViewRefresher) Stop(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}

	return nil
}

// loop refreshes on a fixed interval, one run at a time.
//
// If a run is still in flight when the next tick fires, that tick is skipped: the
// aggregates are a cache, so a missed update is harmless, while queueing runs
// would pile overlapping IO onto the database that also serves request traffic.
func (r *StatsViewRefresher) loop(ctx context.Context) {
	ticker := time.NewTicker(dashboardStatsRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !r.running.CompareAndSwap(false, true) {
				log.Info(ctx, "Dashboard stats refresh is still running, skipping this interval",
					log.String("interval", dashboardStatsRefreshInterval.String()))

				continue
			}

			go func() {
				defer r.running.Store(false)

				r.refreshAll(ctx)
			}()
		}
	}
}

func (r *StatsViewRefresher) refreshAll(ctx context.Context) {
	for _, view := range dashboardStatsViews {
		started := time.Now()

		if err := r.refreshOne(ctx, view); err != nil {
			log.Warn(ctx, "Failed to refresh dashboard stats view",
				log.String("view", view),
				log.Cause(err),
				log.Duration("elapsed", time.Since(started)))

			continue
		}

		log.Info(ctx, "Refreshed dashboard stats view",
			log.String("view", view),
			log.Duration("elapsed", time.Since(started)))
	}
}

func (r *StatsViewRefresher) refreshOne(ctx context.Context, view string) error {
	driver, ok := r.client.Driver().(*entsql.Driver)
	if !ok {
		return fmt.Errorf("unexpected ent driver type %T", r.client.Driver())
	}

	refreshCtx, cancel := context.WithTimeout(ctx, dashboardStatsRefreshTimeout)
	defer cancel()

	// CONCURRENTLY keeps readers unblocked. It requires the unique index the
	// migration creates; without it PostgreSQL rejects the statement.
	// view is a constant from dashboardStatsViews, never user input.
	_, err := driver.DB().ExecContext(refreshCtx,
		fmt.Sprintf("REFRESH MATERIALIZED VIEW CONCURRENTLY %s", view))

	return err
}
