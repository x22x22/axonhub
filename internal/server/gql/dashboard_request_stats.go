package gql

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/pkg/xtime"
)

// requestStatsCounts holds the four dashboard counters computed in one pass.
type requestStatsCounts struct {
	Today     int
	ThisWeek  int
	LastWeek  int
	ThisMonth int
}

// countUsageLogsByCalendarPeriods computes the dashboard request counters with a
// single scan over usage_logs.
//
// The previous implementation issued four separate COUNT(*) queries, each one
// scanning its own index range. On installations with millions of usage log rows
// that added seconds to every dashboard refresh, so the four counters are now
// derived from one conditional aggregation over the widest range.
func (r *queryResolver) countUsageLogsByCalendarPeriods(ctx context.Context, period xtime.CalendarPeriods) (requestStatsCounts, error) {
	var counts requestStatsCounts

	driver, ok := r.client.Driver().(*entsql.Driver)
	if !ok {
		return counts, fmt.Errorf("failed to get underlying SQL driver")
	}

	placeholder := func(index int) string {
		if driver.Dialect() == dialect.Postgres {
			return fmt.Sprintf("$%d", index)
		}

		return "?"
	}

	query := fmt.Sprintf(`SELECT
	COALESCE(SUM(CASE WHEN created_at >= %[1]s THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at >= %[2]s THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at >= %[3]s AND created_at < %[4]s THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at >= %[5]s THEN 1 ELSE 0 END), 0)
FROM usage_logs
WHERE created_at >= %[6]s`,
		placeholder(1), placeholder(2), placeholder(3), placeholder(4), placeholder(5), placeholder(6),
	)

	var today, thisWeek, lastWeek, thisMonth int64

	err := driver.DB().QueryRowContext(ctx, query,
		period.Today.Start,
		period.ThisWeek.Start,
		period.LastWeek.Start,
		period.LastWeek.End,
		period.ThisMonth.Start,
		earliestPeriodStart(period),
	).Scan(&today, &thisWeek, &lastWeek, &thisMonth)
	if err != nil {
		return counts, err
	}

	counts.Today = int(today)
	counts.ThisWeek = int(thisWeek)
	counts.LastWeek = int(lastWeek)
	counts.ThisMonth = int(thisMonth)

	return counts, nil
}

// earliestPeriodStart returns the smallest lower bound of the four calendar
// periods. LastWeek may start before ThisMonth (when a month starts mid-week), so
// the scan range must consider every period, not only the month.
func earliestPeriodStart(period xtime.CalendarPeriods) time.Time {
	earliest := period.Today.Start

	for _, start := range []time.Time{period.ThisWeek.Start, period.LastWeek.Start, period.ThisMonth.Start} {
		if start.Before(earliest) {
			earliest = start
		}
	}

	return earliest
}
