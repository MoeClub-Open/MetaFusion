package catalog

import (
	"fmt"
	"time"
)

// UI date filters are inclusive UTC calendar dates; SQL uses an exclusive end.
func calendarRange(from, to string) (*time.Time, *time.Time, error) {
	var start, end *time.Time
	for _, value := range []struct {
		raw string
		dst **time.Time
		end bool
	}{{from, &start, false}, {to, &end, true}} {
		if value.raw == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", value.raw)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid_date_range")
		}
		if value.end {
			day = day.AddDate(0, 0, 1)
		}
		*value.dst = &day
	}
	if start != nil && end != nil && !start.Before(*end) {
		return nil, nil, fmt.Errorf("invalid_date_range")
	}
	return start, end, nil
}
