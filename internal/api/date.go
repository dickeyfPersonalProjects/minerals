package api

import (
	"encoding"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Date is the wire type for calendar-date fields (e.g. acquired_at).
// It serializes as RFC 3339 full-date ("2006-01-02") and accepts either
// full-date or RFC 3339 date-time on input — full-date is preserved as
// midnight UTC. Backed by time.Time so callers can convert with the
// helpers below.
//
// The Playwright e2e suite (frontend/e2e/visibility.spec.ts) submits
// `acquired_at: '2024-01-02'`, and the SPA's edit form rebuilds the
// same shape from <input type="date">. Accepting full-date here keeps
// the operator-facing contract aligned with the underlying semantics —
// acquisition has no meaningful time-of-day.
type Date time.Time

var _ encoding.TextUnmarshaler = (*Date)(nil)

// UnmarshalText satisfies encoding.TextUnmarshaler. Huma's body
// pipeline json-unmarshals into the struct after schema validation;
// json.Unmarshal honors TextUnmarshaler for non-time targets.
func (d *Date) UnmarshalText(data []byte) error {
	s := string(data)
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		*d = Date(t.UTC())
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		*d = Date(t.UTC())
		return nil
	}
	return fmt.Errorf("acquired_at: expected RFC 3339 full-date (YYYY-MM-DD) or date-time, got %q", s)
}

// MarshalJSON outputs the date as a JSON string in full-date form.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(d).UTC().Format(time.DateOnly) + `"`), nil
}

// Schema declares this type as a string with format=date so the
// generated OpenAPI advertises full-date and Huma validates against
// RFC 3339 full-date (validate.go § "date" branch).
func (Date) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Format: "date"}
}

// TimePtr returns the wrapped time as *time.Time, or nil if d is nil.
func (d *Date) TimePtr() *time.Time {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

// DateFromTimePtr is the inverse: convert *time.Time → *Date.
func DateFromTimePtr(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	d := Date(*t)
	return &d
}
