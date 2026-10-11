// Package slots is the slot engine: pure functions that build the start-time
// grid for a dealership's local day and decide which starts are free. It does
// no I/O and never reads the clock; callers pass every input in.
//
// Time model (docs/system-design.md A4, A7, A11):
//   - Opening hours are local wall-clock times in the dealership's timezone.
//   - Intervals are half-open, [start, end), so touching intervals do not
//     overlap.
//   - Starts step by the grid granularity through real time from the opening
//     instant, and a start is a candidate when start + duration is at or
//     before the closing instant. On a spring-forward day the local times
//     that do not exist are never produced; on a fall-back day the repeated
//     local hour yields two distinct instants. Durations are elapsed time.
package slots

import (
	"time"

	"github.com/google/uuid"
)

// Date is a local calendar date.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Weekday returns the day of the week, independent of any timezone.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC).Weekday()
}

// Hours are a day's opening hours as offsets from local midnight on the wall
// clock, e.g. 8h and 18h for 08:00 to 18:00.
type Hours struct {
	Opens, Closes time.Duration
}

// Interval is a half-open time range [Start, End).
type Interval struct {
	Start, End time.Time
}

// Overlaps reports whether a and b share any instant. Intervals that only
// touch (a.End == b.Start) do not overlap.
func Overlaps(a, b Interval) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

// Candidates returns every start on date, in ascending UTC order, such that
// the start is on the local grid and [start, start + duration) lies within
// the opening hours. It returns nil when nothing fits.
func Candidates(date Date, loc *time.Location, hours Hours, duration, granularity time.Duration) []time.Time {
	if duration <= 0 || granularity <= 0 || hours.Closes <= hours.Opens {
		return nil
	}
	// Round the opening time up to the grid on the local wall clock, then walk
	// real time from there.
	firstWall := (hours.Opens + granularity - 1) / granularity * granularity
	start := instant(date, firstWall, loc)
	closing := instant(date, hours.Closes, loc)

	var out []time.Time
	for t := start; !t.Add(duration).After(closing); t = t.Add(granularity) {
		out = append(out, t.UTC())
	}
	return out
}

// OnGrid reports whether start is one of the candidates for date, so the
// booking path validates exactly what availability offers.
func OnGrid(start time.Time, date Date, loc *time.Location, hours Hours, duration, granularity time.Duration) bool {
	for _, c := range Candidates(date, loc, hours, duration, granularity) {
		if c.Equal(start) {
			return true
		}
	}
	return false
}

// instant returns the first instant on date whose local wall-clock time is
// at or after wall. For a wall time skipped by a spring-forward transition
// that is the transition itself; for a repeated wall time it is the earlier
// occurrence.
func instant(date Date, wall time.Duration, loc *time.Location) time.Time {
	naive := time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC).Add(wall)

	// The offsets in force around this wall time: at most two, either side
	// of a transition.
	var best time.Time
	found := false
	for _, probe := range []time.Time{naive.Add(-26 * time.Hour), naive.Add(26 * time.Hour)} {
		_, offset := probe.In(loc).Zone()
		t := naive.Add(-time.Duration(offset) * time.Second)
		if wallClock(t, loc).Equal(naive) && (!found || t.Before(best)) {
			best, found = t, true
		}
	}
	if found {
		return best
	}

	// The wall time does not exist (a spring-forward gap). Reading it with
	// the earlier offset lands after the gap; the transition is where that
	// offset's period begins.
	_, before := naive.Add(-26 * time.Hour).In(loc).Zone()
	after := naive.Add(-time.Duration(before) * time.Second)
	transition, _ := after.In(loc).ZoneBounds()
	return transition
}

// wallClock returns t's local date and time, re-labelled as UTC so it can be
// compared with a naive wall-clock value.
func wallClock(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

// Busy is a confirmed appointment holding a bay and a technician.
type Busy struct {
	BayID, TechnicianID uuid.UUID
	Interval
}

// Free returns the starts at which at least one of bays and at least one of
// technicians have no busy interval overlapping [start, start + duration).
// Order is preserved.
func Free(starts []time.Time, duration time.Duration, bays, technicians []uuid.UUID, busy []Busy) []time.Time {
	out := make([]time.Time, 0, len(starts))
	for _, s := range starts {
		want := Interval{Start: s, End: s.Add(duration)}
		if anyFree(bays, want, busy, func(b Busy) uuid.UUID { return b.BayID }) &&
			anyFree(technicians, want, busy, func(b Busy) uuid.UUID { return b.TechnicianID }) {
			out = append(out, s)
		}
	}
	return out
}

// anyFree reports whether some resource in ids has no busy interval, keyed
// by holder, that overlaps want.
func anyFree(ids []uuid.UUID, want Interval, busy []Busy, holder func(Busy) uuid.UUID) bool {
	for _, id := range ids {
		free := true
		for _, b := range busy {
			if holder(b) == id && Overlaps(b.Interval, want) {
				free = false
				break
			}
		}
		if free {
			return true
		}
	}
	return false
}

// NotBefore drops the starts earlier than now. Order is preserved.
func NotBefore(starts []time.Time, now time.Time) []time.Time {
	out := make([]time.Time, 0, len(starts))
	for _, s := range starts {
		if !s.Before(now) {
			out = append(out, s)
		}
	}
	return out
}
