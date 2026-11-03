package refresh

import "time"

// NextRun is when the next scheduled run is due, in UTC, and false when the
// schedule is off. last is the start of the last scheduled run, nil when
// there has been none; since is when the schedule was set.
//
// Without a last run, the first run is the first time the clock reaches the
// chosen hour after since (that day if the hour was still ahead, else the
// next day). After that:
//   - weekly, biweekly and every N days count 7, 14 or N days from the day
//     of the last run, at the hour;
//   - monthly runs on the 1st of the month after the last run, at the hour,
//     so it never drifts across short months.
//
// A next run in the past (the server was off) is due at once.
func NextRun(s Settings, last *time.Time, since time.Time) (time.Time, bool) {
	now := since.UTC()
	if s.Schedule == ScheduleOff || s.Schedule == "" {
		return time.Time{}, false
	}
	if last == nil {
		t := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, time.UTC)
		if t.Before(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}
	l := last.UTC()
	day := time.Date(l.Year(), l.Month(), l.Day(), s.Hour, 0, 0, 0, time.UTC)
	switch s.Schedule {
	case ScheduleWeekly:
		return day.AddDate(0, 0, 7), true
	case ScheduleBiweekly:
		return day.AddDate(0, 0, 14), true
	case ScheduleMonthly:
		// The 1st of the next month: AddDate on day 1 never overflows.
		return time.Date(l.Year(), l.Month(), 1, s.Hour, 0, 0, 0, time.UTC).AddDate(0, 1, 0), true
	case ScheduleDays:
		n := s.EveryDays
		if n < 1 {
			n = 1
		}
		return day.AddDate(0, 0, n), true
	}
	return time.Time{}, false
}
