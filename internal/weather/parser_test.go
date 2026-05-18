package weather

import (
	"testing"
	"time"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int          { return &v }
func ptrS(v string) *string    { return &v }

func sampleForecast() *Forecast {
	f := &Forecast{
		UTCOffsetSeconds: 3 * 3600,
	}
	f.Hourly.Time = []string{"2026-05-15T00:00", "2026-05-15T01:00"}
	f.Hourly.Temperature2m = []*float64{ptrF(12.3), ptrF(11.7)}
	f.Hourly.CloudCover = []*float64{ptrF(40), ptrF(55)}
	f.Hourly.IsDay = []*int{ptrI(0), ptrI(0)}
	f.Hourly.ShortwaveRadiation = []*float64{ptrF(0), ptrF(0)}
	f.Hourly.DirectRadiation = []*float64{ptrF(0), nil}
	f.Hourly.DiffuseRadiation = []*float64{nil, ptrF(2.1)}
	f.Hourly.GlobalTiltedIrradiance = []*float64{ptrF(0), ptrF(0)}

	f.Daily.Time = []string{"2026-05-15"}
	f.Daily.Sunrise = []*string{ptrS("2026-05-15T05:12")}
	f.Daily.Sunset = []*string{ptrS("2026-05-15T20:30")}
	f.Daily.DaylightDuration = []*float64{ptrF(55080)}
	f.Daily.SunshineDuration = []*float64{ptrF(40000)}
	f.Daily.ShortwaveRadiationSum = []*float64{ptrF(25.4)}
	return f
}

func TestBuildRowsConvertsLocalToUTC(t *testing.T) {
	hourly, daily, err := BuildRows("org-a", sampleForecast(), "https://example.com")
	if err != nil {
		t.Fatalf("BuildRows: %v", err)
	}
	if len(hourly) != 2 {
		t.Fatalf("hourly rows: got %d, want 2", len(hourly))
	}
	// 2026-05-15T00:00 local with offset +03:00 = 2026-05-14T21:00 UTC.
	wantHour := time.Date(2026, 5, 14, 21, 0, 0, 0, time.UTC)
	if !hourly[0].Hour.Equal(wantHour) {
		t.Fatalf("hour[0]: got %s, want %s", hourly[0].Hour, wantHour)
	}
	if hourly[0].IsDay == nil || *hourly[0].IsDay {
		t.Fatalf("hour[0].IsDay: got %v, want pointer to false", hourly[0].IsDay)
	}
	if hourly[0].Temperature2mC == nil || *hourly[0].Temperature2mC != 12.3 {
		t.Fatalf("hour[0].Temperature2mC: got %v", hourly[0].Temperature2mC)
	}
	if hourly[0].DiffuseWm2 != nil {
		t.Fatalf("hour[0].DiffuseWm2: expected nil for missing, got %v", *hourly[0].DiffuseWm2)
	}
	if hourly[1].DirectWm2 != nil {
		t.Fatalf("hour[1].DirectWm2: expected nil for missing, got %v", *hourly[1].DirectWm2)
	}
	if hourly[0].SourceURL != "https://example.com" {
		t.Fatalf("hour[0].SourceURL: got %q", hourly[0].SourceURL)
	}
	if hourly[0].OrganizationID != "org-a" {
		t.Fatalf("hour[0].OrganizationID: got %q", hourly[0].OrganizationID)
	}

	if len(daily) != 1 {
		t.Fatalf("daily rows: got %d, want 1", len(daily))
	}
	wantDay := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if !daily[0].Day.Equal(wantDay) {
		t.Fatalf("day[0]: got %s, want %s", daily[0].Day, wantDay)
	}
	if daily[0].Sunrise == nil {
		t.Fatal("sunrise should not be nil")
	}
	wantSunrise := time.Date(2026, 5, 15, 2, 12, 0, 0, time.UTC) // 05:12 +0300 -> 02:12 UTC
	if !daily[0].Sunrise.Equal(wantSunrise) {
		t.Fatalf("sunrise: got %s, want %s", daily[0].Sunrise, wantSunrise)
	}
	if daily[0].DaylightDurationS == nil || *daily[0].DaylightDurationS != 55080 {
		t.Fatalf("daylight_duration_s: got %v", daily[0].DaylightDurationS)
	}
}

func TestBuildRowsRejectsBadTimestamp(t *testing.T) {
	f := sampleForecast()
	f.Hourly.Time[0] = "not-a-time"
	_, _, err := BuildRows("org-a", f, "u")
	if err == nil {
		t.Fatal("expected error for bad hourly timestamp")
	}
}

func TestBuildRowsHandlesNilForecast(t *testing.T) {
	_, _, err := BuildRows("org-a", nil, "u")
	if err == nil {
		t.Fatal("expected error for nil forecast")
	}
}

func TestBuildRowsZeroOffsetIsUTC(t *testing.T) {
	f := &Forecast{UTCOffsetSeconds: 0}
	f.Hourly.Time = []string{"2026-05-15T10:00"}
	f.Daily.Time = []string{"2026-05-15"}
	hourly, _, err := BuildRows("org-a", f, "u")
	if err != nil {
		t.Fatalf("BuildRows: %v", err)
	}
	want := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	if !hourly[0].Hour.Equal(want) {
		t.Fatalf("hour[0] for UTC offset: got %s, want %s", hourly[0].Hour, want)
	}
}
