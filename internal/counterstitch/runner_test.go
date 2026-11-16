package counterstitch

import (
	"context"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/counters"
)

// Кролевець: the grid lost at 18:27:40 by the generator controller, back
// at 04:00. The SmartLogger's grid counters fell to zero at 18:27:39 and
// came back at 04:04; its PV counter blipped for 35 s at the switchover.
func TestMarkOffgrid(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	lost, back := at("2026-11-15T16:27:40Z"), at("2026-11-16T02:00:00Z")
	r := &Runner{OffgridBetween: func(_ context.Context, _ string, from, to time.Time) (bool, error) {
		// Off-grid readings fall anywhere in [lost, back).
		return from.Before(back) && !to.Before(lost), nil
	}}
	eps := []counters.Episode{
		{Kind: counters.KindDropout, Start: at("2026-11-15T16:27:39Z"), End: at("2026-11-16T02:04:10Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-15T16:27:39Z"), End: at("2026-11-15T16:28:14Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-14T09:00:00Z"), End: at("2026-11-16T02:05:00Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-14T09:00:00Z"), End: at("2026-11-14T12:00:00Z")},
		{Kind: counters.KindSeam, Start: at("2026-11-15T16:28:00Z"), End: at("2026-11-15T16:28:00Z")},
	}
	if err := r.markOffgrid(context.Background(), "ke", eps); err != nil {
		t.Fatal(err)
	}
	want := []counters.Kind{counters.KindOffgrid, counters.KindOffgrid, counters.KindOffgrid, counters.KindDropout, counters.KindSeam}
	for i, w := range want {
		if eps[i].Kind != w {
			t.Errorf("episode %d: %q, want %q", i, eps[i].Kind, w)
		}
	}
}
