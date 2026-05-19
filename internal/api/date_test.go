package api

import (
	"encoding/json"
	"testing"
	"time"
)

// TestDate_UnmarshalAcceptsFullDateAndDateTime pins the contract
// fixed in mi-s2ma: acquired_at must accept either RFC 3339 full-date
// ("2024-01-02") or RFC 3339 date-time. The Playwright visibility
// spec submits the full-date form; legacy values stored as
// date-times still flow through unchanged.
func TestDate_UnmarshalAcceptsFullDateAndDateTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want time.Time
	}{
		{"full-date", `{"d":"2024-01-02"}`, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"date-time-Z", `{"d":"2024-01-02T12:34:56Z"}`, time.Date(2024, 1, 2, 12, 34, 56, 0, time.UTC)},
		{"date-time-offset", `{"d":"2024-01-02T12:34:56+02:00"}`,
			time.Date(2024, 1, 2, 10, 34, 56, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out struct {
				D Date `json:"d"`
			}
			if err := json.Unmarshal([]byte(tc.body), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := time.Time(out.D); !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDate_RejectsGarbage verifies the unmarshaller surfaces a
// helpful error for strings that match neither accepted format.
func TestDate_RejectsGarbage(t *testing.T) {
	t.Parallel()
	var out struct {
		D Date `json:"d"`
	}
	err := json.Unmarshal([]byte(`{"d":"not-a-date"}`), &out)
	if err == nil {
		t.Fatal("expected error for invalid date, got nil")
	}
}

// TestDate_MarshalEmitsFullDate locks the wire shape on the response
// side — the SPA's fmtDate() and toDateInputValue() helpers both
// rely on receiving YYYY-MM-DD (no time component).
func TestDate_MarshalEmitsFullDate(t *testing.T) {
	t.Parallel()
	d := Date(time.Date(2024, 1, 2, 12, 34, 56, 0, time.UTC))
	got, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `"2024-01-02"`; string(got) != want {
		t.Errorf("marshal = %s, want %s", got, want)
	}
}

// TestDate_RoundTripThroughTimePtr ensures the helpers used at the
// handler boundary preserve the value across the Date ↔ *time.Time
// hop the wire→domain conversion performs.
func TestDate_RoundTripThroughTimePtr(t *testing.T) {
	t.Parallel()
	orig := Date(time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))
	back := DateFromTimePtr((&orig).TimePtr())
	if back == nil {
		t.Fatal("DateFromTimePtr returned nil")
	}
	if !time.Time(*back).Equal(time.Time(orig)) {
		t.Errorf("round-trip mismatch: got %s, want %s", time.Time(*back), time.Time(orig))
	}
	if DateFromTimePtr(nil) != nil {
		t.Error("DateFromTimePtr(nil) should return nil")
	}
	var nilD *Date
	if nilD.TimePtr() != nil {
		t.Error("(*Date)(nil).TimePtr() should return nil")
	}
}
