package gql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/xtime"
)

// TestCountUsageLogsByCalendarPeriods_MatchesPerPeriodCounts pins the single-pass
// aggregation against the previous four-query implementation: both must return the
// same counters for rows that straddle every calendar boundary.
func TestCountUsageLogsByCalendarPeriods_MatchesPerPeriodCounts(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:usage_stats?mode=memory&_fk=0")

	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	loc := time.UTC
	period := xtime.GetCalendarPeriods(loc)

	now := time.Now().UTC()
	periodStarts := []time.Time{period.Today.Start, period.ThisWeek.Start, period.LastWeek.Start, period.ThisMonth.Start}

	// Rows on the boundary of each period, one second before it, and one well outside.
	timestamps := []time.Time{now}
	for _, start := range periodStarts {
		timestamps = append(timestamps,
			start,
			start.Add(time.Second),
			start.Add(-time.Second),
			start.Add(-30*24*time.Hour),
		)
	}

	for i, ts := range timestamps {
		_, err := client.UsageLog.Create().
			SetRequestID(i + 1).
			SetModelID("test-model").
			SetCreatedAt(ts).
			SetUpdatedAt(ts).
			Save(ctx)
		require.NoError(t, err)
	}

	resolver := &queryResolver{Resolver: &Resolver{client: client}}

	got, err := resolver.countUsageLogsByCalendarPeriods(ctx, period)
	require.NoError(t, err)

	// Reference implementation: the per-period COUNT(*) queries this replaces.
	wantToday, err := client.UsageLog.Query().Where(usagelog.CreatedAtGTE(period.Today.Start)).Count(ctx)
	require.NoError(t, err)

	wantThisWeek, err := client.UsageLog.Query().Where(usagelog.CreatedAtGTE(period.ThisWeek.Start)).Count(ctx)
	require.NoError(t, err)

	wantLastWeek, err := client.UsageLog.Query().
		Where(usagelog.CreatedAtGTE(period.LastWeek.Start), usagelog.CreatedAtLT(period.LastWeek.End)).Count(ctx)
	require.NoError(t, err)

	wantThisMonth, err := client.UsageLog.Query().Where(usagelog.CreatedAtGTE(period.ThisMonth.Start)).Count(ctx)
	require.NoError(t, err)

	require.Equal(t, requestStatsCounts{
		Today:     wantToday,
		ThisWeek:  wantThisWeek,
		LastWeek:  wantLastWeek,
		ThisMonth: wantThisMonth,
	}, got)
}

// TestEarliestPeriodStart covers the month boundary case where the previous week
// starts before the current month.
func TestEarliestPeriodStart(t *testing.T) {
	base := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)

	period := xtime.CalendarPeriods{
		Today:     xtime.Period{Start: base.Add(24 * time.Hour), End: base.Add(48 * time.Hour)},
		ThisWeek:  xtime.Period{Start: base, End: base.Add(7 * 24 * time.Hour)},
		LastWeek:  xtime.Period{Start: base.Add(-7 * 24 * time.Hour), End: base},
		ThisMonth: xtime.Period{Start: base.Add(-24 * time.Hour), End: base.Add(30 * 24 * time.Hour)},
	}

	require.Equal(t, base.Add(-7*24*time.Hour), earliestPeriodStart(period))
}
