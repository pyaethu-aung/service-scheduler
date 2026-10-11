package seed

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHM(t *testing.T) {
	t.Parallel()

	if got := hm(17, 45); !got.Valid || got.Microseconds != (17*60+45)*60*1_000_000 {
		t.Fatalf("hm(17, 45) = %+v", got)
	}
}

// The seed data has to be bookable: these invariants keep it that way.
func TestSeedData(t *testing.T) {
	t.Parallel()

	if n := len(steps()); n != 2+12+4+6+8+20+4+5 {
		t.Errorf("steps() = %d upserts, want one per seed row", n)
	}

	for _, st := range serviceTypes {
		if st.duration%15 != 0 {
			t.Errorf("%s: duration %d is not on the 15-minute grid", st.code, st.duration)
		}
	}

	for _, d := range dealerships {
		if _, err := time.LoadLocation(d.timezone); err != nil {
			t.Errorf("%s: timezone %q: %v", d.name, d.timezone, err)
		}
		if len(d.bays) != 3 || len(d.techs) != 4 {
			t.Errorf("%s: %d bays and %d technicians, want 3 and 4", d.name, len(d.bays), len(d.techs))
		}
		for weekday, h := range d.hours {
			day := int64(24 * time.Hour / time.Microsecond)
			if weekday < 0 || weekday > 6 || h[0].Microseconds < 0 || h[1].Microseconds > day ||
				h[1].Microseconds <= h[0].Microseconds {
				t.Errorf("%s weekday %d: invalid hours %v", d.name, weekday, h)
			}
		}
		// Every service type has a qualified technician at every site.
		qualified := map[uuid.UUID]int{}
		for _, tech := range d.techs {
			for _, st := range tech.skills {
				qualified[st]++
			}
		}
		for _, st := range serviceTypes {
			if qualified[st.id] == 0 {
				t.Errorf("%s: nobody is qualified for %s", d.name, st.code)
			}
		}
	}
}
