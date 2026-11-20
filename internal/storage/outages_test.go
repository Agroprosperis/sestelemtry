package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/outages"
)

// outagesTestPool connects to a throwaway database named by
// OUTAGES_TEST_DATABASE_URL; without it the test is skipped. It creates
// the outage tables and clears what the organization left.
func outagesTestPool(t *testing.T, org string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OUTAGES_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OUTAGES_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := InitOutagesSchema(ctx, pool); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM outage_station_accounts WHERE organization_id = $1`,
		`DELETE FROM outage_intervals WHERE organization_id = $1`,
		`DELETE FROM outage_reads WHERE organization_id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, org); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	return pool
}

func TestOutageAccountKeepsThePasswordApart(t *testing.T) {
	const org = "outages-test"
	ctx := context.Background()
	store := NewOutagesStore(outagesTestPool(t, org))

	secret := "s3cret"
	a := outages.Account{OrganizationID: org, Host: "vn.e-svitlo.com.ua", Login: "a@b.c"}
	if err := store.SaveAccount(ctx, a, &secret); err != nil {
		t.Fatal(err)
	}
	a.EIC = "62Z0000000000001"
	// A save without a password keeps the stored one.
	if err := store.SaveAccount(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	got, _, ok, err := store.Account(ctx, org)
	if err != nil || !ok || !got.PasswordConfigured || got.EIC != a.EIC {
		t.Fatalf("account = %+v ok=%v err=%v", got, ok, err)
	}
	if p, err := store.Password(ctx, org); err != nil || p != secret {
		t.Fatalf("password = %q, %v", p, err)
	}
	if err := store.DeleteAccount(ctx, org); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := store.Account(ctx, org); ok {
		t.Fatal("account still there after delete")
	}
}

func TestOutageReadKeepsEarlierDays(t *testing.T) {
	const org = "outages-test"
	ctx := context.Background()
	store := NewOutagesStore(outagesTestPool(t, org))
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	ok := outages.Read{AttemptedAt: day(19, 12), SucceededAt: day(19, 12)}

	// Yesterday's read: yesterday evening and this morning.
	if err := store.ReplaceIntervals(ctx, org, day(19, 0), []outages.Interval{
		{Start: day(19, 18), End: day(19, 20), Kind: outages.KindSchedule},
		{Start: day(20, 10), End: day(20, 12), Kind: outages.KindSchedule},
	}, ok); err != nil {
		t.Fatal(err)
	}
	// Today's read moves this morning's outage and adds tomorrow's.
	if err := store.ReplaceIntervals(ctx, org, day(20, 0), []outages.Interval{
		{Start: day(20, 14), End: day(20, 18), Kind: outages.KindSchedule},
		{Start: day(21, 4), End: day(21, 6), Kind: outages.KindSchedule},
	}, outages.Read{AttemptedAt: day(20, 12), SucceededAt: day(20, 12)}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Intervals(ctx, org, day(19, 0), day(22, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{day(19, 18), day(20, 14), day(21, 4)}
	if len(got) != len(want) {
		t.Fatalf("intervals = %+v, want starts %v", got, want)
	}
	for i, iv := range got {
		if !iv.Start.Equal(want[i]) {
			t.Fatalf("interval %d starts %v, want %v", i, iv.Start, want[i])
		}
	}

	// A failed read keeps the time of the last success.
	if err := store.MarkRead(ctx, org, outages.Read{AttemptedAt: day(20, 13), Error: "немає зʼєднання"}); err != nil {
		t.Fatal(err)
	}
	_, read, _, err := store.Account(ctx, org)
	if err != nil || !read.SucceededAt.Equal(day(20, 12)) || read.Error == "" {
		t.Fatalf("read = %+v, %v", read, err)
	}
}
