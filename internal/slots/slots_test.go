package slots

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func utcs(ss ...string) []time.Time {
	out := make([]time.Time, len(ss))
	for i, s := range ss {
		out[i] = utc(s)
	}
	return out
}

func hm(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }

func TestDateWeekday(t *testing.T) {
	t.Parallel()
	if got := (Date{2026, time.October, 20}).Weekday(); got != time.Tuesday {
		t.Errorf("2026-10-20 = %s, want Tuesday", got)
	}
	if got := (Date{2026, time.March, 29}).Weekday(); got != time.Sunday {
		t.Errorf("2026-03-29 = %s, want Sunday", got)
	}
}

func TestOverlaps(t *testing.T) {
	t.Parallel()

	nine, ten, eleven := utc("2026-10-20T09:00:00Z"), utc("2026-10-20T10:00:00Z"), utc("2026-10-20T11:00:00Z")
	half := utc("2026-10-20T09:30:00Z")
	tests := []struct {
		name string
		a, b Interval
		want bool
	}{
		{"touching after", Interval{nine, ten}, Interval{ten, eleven}, false},
		{"touching before", Interval{ten, eleven}, Interval{nine, ten}, false},
		{"partial overlap", Interval{nine, ten}, Interval{half, eleven}, true},
		{"contained", Interval{nine, eleven}, Interval{half, ten}, true},
		{"identical", Interval{nine, ten}, Interval{nine, ten}, true},
		{"disjoint", Interval{nine, half}, Interval{ten, eleven}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Overlaps(tt.a, tt.b); got != tt.want {
				t.Errorf("Overlaps = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	t.Parallel()

	saigon := mustLoad(t, "Asia/Ho_Chi_Minh") // UTC+7, no DST
	london := mustLoad(t, "Europe/London")
	tue := Date{2026, time.October, 20}
	q := 15 * time.Minute

	tests := []struct {
		name        string
		date        Date
		loc         *time.Location
		hours       Hours
		duration    time.Duration
		granularity time.Duration
		wantFirst   string
		wantLast    string
		wantCount   int
	}{
		{
			name: "first slot at opening, last ends exactly at closing",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(17, 0)}, duration: time.Hour, granularity: q,
			// 08:00 to 16:00 local in 15-minute steps.
			wantFirst: "2026-10-20T01:00:00Z", wantLast: "2026-10-20T09:00:00Z", wantCount: 33,
		},
		{
			name: "last valid slot for a long service",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(12, 0)}, duration: 2 * time.Hour, granularity: q,
			// 10:00 local is the last start: 10:00 + 2h = 12:00 closing.
			wantFirst: "2026-10-20T01:00:00Z", wantLast: "2026-10-20T03:00:00Z", wantCount: 9,
		},
		{
			name: "service exactly as long as the day",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(10, 0)}, duration: 2 * time.Hour, granularity: q,
			wantFirst: "2026-10-20T01:00:00Z", wantLast: "2026-10-20T01:00:00Z", wantCount: 1,
		},
		{
			name: "service longer than the day",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(9, 0)}, duration: 2 * time.Hour, granularity: q,
			wantCount: 0,
		},
		{
			name: "opening off the grid rounds up to the next grid time",
			date: tue, loc: saigon, hours: Hours{hm(8, 10), hm(10, 0)}, duration: time.Hour, granularity: q,
			// 08:15, 08:30, 08:45, 09:00 local.
			wantFirst: "2026-10-20T01:15:00Z", wantLast: "2026-10-20T02:00:00Z", wantCount: 4,
		},
		{
			name: "coarser grid",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(10, 0)}, duration: 30 * time.Minute, granularity: 30 * time.Minute,
			wantFirst: "2026-10-20T01:00:00Z", wantLast: "2026-10-20T02:30:00Z", wantCount: 4,
		},
		{
			name: "London summer time is UTC+1",
			date: tue, loc: london, hours: Hours{hm(8, 0), hm(18, 0)}, duration: time.Hour, granularity: q,
			wantFirst: "2026-10-20T07:00:00Z", wantLast: "2026-10-20T16:00:00Z", wantCount: 37,
		},
		{
			name: "London winter time is UTC+0",
			date: Date{2026, time.November, 3}, loc: london, hours: Hours{hm(8, 0), hm(18, 0)}, duration: time.Hour, granularity: q,
			wantFirst: "2026-11-03T08:00:00Z", wantLast: "2026-11-03T17:00:00Z", wantCount: 37,
		},
		{
			// 2026-03-29: clocks go from 01:00 GMT to 02:00 BST. Open 00:00 to
			// 04:00 local is only three real hours, and no start falls in the
			// missing 01:00-02:00 local hour.
			name: "spring forward skips the missing hour",
			date: Date{2026, time.March, 29}, loc: london, hours: Hours{hm(0, 0), hm(4, 0)}, duration: time.Hour, granularity: time.Hour,
			wantFirst: "2026-03-29T00:00:00Z", wantLast: "2026-03-29T02:00:00Z", wantCount: 3,
		},
		{
			name: "spring forward with an opening inside the gap starts at the transition",
			date: Date{2026, time.March, 29}, loc: london, hours: Hours{hm(1, 30), hm(4, 0)}, duration: time.Hour, granularity: q,
			// 02:00 BST (01:00Z) to 03:00 BST (02:00Z).
			wantFirst: "2026-03-29T01:00:00Z", wantLast: "2026-03-29T02:00:00Z", wantCount: 5,
		},
		{
			// 2026-10-25: clocks go from 02:00 BST back to 01:00 GMT. Open 00:00
			// to 04:00 local is five real hours, and 01:00 local happens twice.
			name: "fall back offers the repeated hour twice",
			date: Date{2026, time.October, 25}, loc: london, hours: Hours{hm(0, 0), hm(4, 0)}, duration: time.Hour, granularity: time.Hour,
			wantFirst: "2026-10-24T23:00:00Z", wantLast: "2026-10-25T03:00:00Z", wantCount: 5,
		},
		{
			name: "closing before opening",
			date: tue, loc: saigon, hours: Hours{hm(17, 0), hm(8, 0)}, duration: time.Hour, granularity: q,
			wantCount: 0,
		},
		{
			name: "zero duration",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(17, 0)}, duration: 0, granularity: q,
			wantCount: 0,
		},
		{
			name: "zero granularity",
			date: tue, loc: saigon, hours: Hours{hm(8, 0), hm(17, 0)}, duration: time.Hour, granularity: 0,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Candidates(tt.date, tt.loc, tt.hours, tt.duration, tt.granularity)
			if len(got) != tt.wantCount {
				t.Fatalf("got %d candidates %v, want %d", len(got), got, tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if !got[0].Equal(utc(tt.wantFirst)) || !got[len(got)-1].Equal(utc(tt.wantLast)) {
				t.Errorf("range = %s .. %s, want %s .. %s", got[0], got[len(got)-1], tt.wantFirst, tt.wantLast)
			}
			for i, c := range got {
				if c.Location() != time.UTC {
					t.Errorf("candidate %d is in %s, want UTC", i, c.Location())
				}
				if i > 0 && c.Sub(got[i-1]) != tt.granularity {
					t.Errorf("step %d = %s, want %s", i, c.Sub(got[i-1]), tt.granularity)
				}
				if local := c.In(tt.loc); (time.Duration(local.Hour())*time.Hour+time.Duration(local.Minute())*time.Minute)%tt.granularity != 0 {
					t.Errorf("candidate %s (%s local) is off the grid", c, local.Format("15:04"))
				}
			}
		})
	}
}

func TestCandidates_DSTWallClock(t *testing.T) {
	t.Parallel()
	london := mustLoad(t, "Europe/London")

	local := func(ts []time.Time) []string {
		out := make([]string, len(ts))
		for i, c := range ts {
			out[i] = c.In(london).Format("15:04 MST")
		}
		return out
	}

	spring := local(Candidates(Date{2026, time.March, 29}, london, Hours{0, hm(4, 0)}, time.Hour, time.Hour))
	if want := []string{"00:00 GMT", "02:00 BST", "03:00 BST"}; !slices.Equal(spring, want) {
		t.Errorf("spring forward = %v, want %v", spring, want)
	}

	fall := local(Candidates(Date{2026, time.October, 25}, london, Hours{0, hm(4, 0)}, time.Hour, time.Hour))
	if want := []string{"00:00 BST", "01:00 BST", "01:00 GMT", "02:00 GMT", "03:00 GMT"}; !slices.Equal(fall, want) {
		t.Errorf("fall back = %v, want %v", fall, want)
	}
}

func TestOnGrid(t *testing.T) {
	t.Parallel()
	saigon := mustLoad(t, "Asia/Ho_Chi_Minh")
	day := Date{2026, time.October, 20}
	hours := Hours{hm(8, 0), hm(17, 0)}

	tests := []struct {
		start string
		want  bool
	}{
		{"2026-10-20T01:00:00Z", true},  // 08:00, opening
		{"2026-10-20T09:00:00Z", true},  // 16:00, last start for 60 minutes
		{"2026-10-20T09:15:00Z", false}, // would end after closing
		{"2026-10-20T00:45:00Z", false}, // before opening
		{"2026-10-20T01:05:00Z", false}, // off the grid
		{"2026-10-21T01:00:00Z", false}, // another day
	}
	for _, tt := range tests {
		if got := OnGrid(utc(tt.start), day, saigon, hours, time.Hour, 15*time.Minute); got != tt.want {
			t.Errorf("OnGrid(%s) = %v, want %v", tt.start, got, tt.want)
		}
	}
}

func TestFree(t *testing.T) {
	t.Parallel()

	bay1, bay2 := uuid.New(), uuid.New()
	tech1, tech2 := uuid.New(), uuid.New()
	starts := utcs("2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z")
	busy := func(bay, tech uuid.UUID, from, to string) Busy {
		return Busy{BayID: bay, TechnicianID: tech, Interval: Interval{utc(from), utc(to)}}
	}

	tests := []struct {
		name   string
		bays   []uuid.UUID
		techs  []uuid.UUID
		busy   []Busy
		wanted []time.Time
	}{
		{
			name: "nothing booked", bays: []uuid.UUID{bay1}, techs: []uuid.UUID{tech1},
			wanted: starts,
		},
		{
			name: "one bay busy, another free", bays: []uuid.UUID{bay1, bay2}, techs: []uuid.UUID{tech1, tech2},
			busy:   []Busy{busy(bay1, tech1, "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z")},
			wanted: starts,
		},
		{
			name: "only bay busy", bays: []uuid.UUID{bay1}, techs: []uuid.UUID{tech1, tech2},
			busy:   []Busy{busy(bay1, tech1, "2026-10-20T09:30:00Z", "2026-10-20T10:30:00Z")},
			wanted: utcs("2026-10-20T11:00:00Z"),
		},
		{
			name: "only qualified technician busy", bays: []uuid.UUID{bay1, bay2}, techs: []uuid.UUID{tech1},
			busy:   []Busy{busy(bay2, tech1, "2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z")},
			wanted: utcs("2026-10-20T09:00:00Z", "2026-10-20T11:00:00Z"),
		},
		{
			name: "touching appointments do not block", bays: []uuid.UUID{bay1}, techs: []uuid.UUID{tech1},
			busy: []Busy{
				busy(bay1, tech1, "2026-10-20T08:00:00Z", "2026-10-20T09:00:00Z"),
				busy(bay1, tech1, "2026-10-20T12:00:00Z", "2026-10-20T13:00:00Z"),
			},
			wanted: starts,
		},
		{
			name: "bay and technician free but at different starts", bays: []uuid.UUID{bay1}, techs: []uuid.UUID{tech1, tech2},
			busy: []Busy{
				busy(bay1, tech2, "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z"),
				busy(bay2, tech1, "2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z"),
			},
			// 10:00: bay1 free, tech2 free. 09:00: bay1 busy. 11:00: all free.
			wanted: utcs("2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z"),
		},
		{
			name: "no bays", techs: []uuid.UUID{tech1},
			wanted: []time.Time{},
		},
		{
			name: "no qualified technicians", bays: []uuid.UUID{bay1},
			wanted: []time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Free(starts, time.Hour, tt.bays, tt.techs, tt.busy)
			if !slices.EqualFunc(got, tt.wanted, time.Time.Equal) {
				t.Errorf("Free = %v, want %v", got, tt.wanted)
			}
		})
	}
}

func TestNotBefore(t *testing.T) {
	t.Parallel()
	starts := utcs("2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z")

	if got := NotBefore(starts, utc("2026-10-20T10:00:00Z")); !slices.EqualFunc(got, starts[1:], time.Time.Equal) {
		t.Errorf("now on a start: %v", got)
	}
	if got := NotBefore(starts, utc("2026-10-20T10:01:00Z")); !slices.EqualFunc(got, starts[2:], time.Time.Equal) {
		t.Errorf("now between starts: %v", got)
	}
	if got := NotBefore(starts, utc("2026-10-21T00:00:00Z")); len(got) != 0 {
		t.Errorf("past day: %v", got)
	}
}

func TestDateOfAndDayBounds(t *testing.T) {
	t.Parallel()
	saigon := mustLoad(t, "Asia/Ho_Chi_Minh")
	london := mustLoad(t, "Europe/London")

	// 23:30Z on the 19th is already the 20th in Ho Chi Minh City.
	if got := DateOf(utc("2026-10-19T23:30:00Z"), saigon); got != (Date{2026, time.October, 20}) {
		t.Errorf("DateOf = %+v, want 2026-10-20", got)
	}

	tests := []struct {
		name       string
		date       Date
		loc        *time.Location
		start, end string
		hours      time.Duration
	}{
		{"ordinary day", Date{2026, time.October, 20}, saigon, "2026-10-19T17:00:00Z", "2026-10-20T17:00:00Z", 24 * time.Hour},
		{"spring forward is 23h", Date{2026, time.March, 29}, london, "2026-03-29T00:00:00Z", "2026-03-29T23:00:00Z", 23 * time.Hour},
		{"fall back is 25h", Date{2026, time.October, 25}, london, "2026-10-24T23:00:00Z", "2026-10-26T00:00:00Z", 25 * time.Hour},
		{"month end rolls over", Date{2026, time.October, 31}, london, "2026-10-31T00:00:00Z", "2026-11-01T00:00:00Z", 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DayBounds(tt.date, tt.loc)
			if !got.Start.Equal(utc(tt.start)) || !got.End.Equal(utc(tt.end)) || got.End.Sub(got.Start) != tt.hours {
				t.Errorf("DayBounds = [%s, %s), want [%s, %s) of %s", got.Start, got.End, tt.start, tt.end, tt.hours)
			}
		})
	}
}
