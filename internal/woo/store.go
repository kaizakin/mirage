package woo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	// Store timezones must resolve even in images without system tzdata (scratch, distroless).
	_ "time/tzdata"
)

// Location returns the store's configured timezone, read from the WordPress REST index.
func (c *Client) Location(ctx context.Context) (*time.Location, error) {
	var index struct {
		TimezoneString string      `json:"timezone_string"`
		GMTOffset      json.Number `json:"gmt_offset"` // hours; may be fractional ("5.5") and sent as a string
	}
	if _, err := c.do(ctx, http.MethodGet, "/wp-json/", nil, nil, &index); err != nil {
		return nil, err
	}

	// timezone_string is empty when the site uses a manual "UTC+5:30"-style offset.
	if index.TimezoneString != "" {
		return time.LoadLocation(index.TimezoneString)
	}
	hours, err := index.GMTOffset.Float64()
	if err != nil {
		return nil, fmt.Errorf("woocommerce: gmt_offset %q: %w", index.GMTOffset, err)
	}
	return time.FixedZone(fmt.Sprintf("UTC%+g", hours), int(hours*3600)), nil
}

// StartOfToday returns midnight of the current day in the store's timezone.
func (c *Client) StartOfToday(ctx context.Context) (time.Time, error) {
	loc, err := c.Location(ctx)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := time.Now().In(loc).Date()
	return startOfDay(y, m, d, loc), nil
}

// startOfDay finds the first instant of a date. Where DST skips midnight (America/Santiago),
// time.Date normalizes 00:00 back into the previous day, so step forward to the transition.
func startOfDay(y int, m time.Month, d int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	for t.Day() != d {
		t = t.Add(30 * time.Minute)
	}
	return t
}
