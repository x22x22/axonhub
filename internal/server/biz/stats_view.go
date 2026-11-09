package biz

import (
	"context"
	"fmt"
	"os"
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
	// dashboardStatsRefreshHour is the local hour the views are refreshed.
	//
	// The views only need history: the resolvers read "today" straight from the
	// detail tables, and a day's history never changes after it ends. One refresh
	// a day at a quiet hour is therefore enough, and it keeps the IO away from
	// request traffic.
	dashboardStatsRefreshHour = 3

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
		log.Int("hour", dashboardStatsRefreshHour),
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

func (r *StatsViewRefresher) loop(ctx context.Context) {
	for {
		next := nextDashboardStatsRefresh(time.Now())
		timer := time.NewTimer(time.Until(next))

		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
			r.refreshAll(ctx)
		}
	}
}

// nextDashboardStatsRefresh returns the next occurrence of the refresh hour.
func nextDashboardStatsRefresh(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(),
		dashboardStatsRefreshHour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}

	return next
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
