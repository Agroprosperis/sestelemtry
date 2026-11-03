package storage

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// pvPlanTestPool connects to a throwaway database named by
// PVPLAN_TEST_DATABASE_URL; without it the test is skipped. It creates
// the pv plan tables and leaves a clean slate for the organization the
// test uses.
func pvPlanTestPool(t *testing.T, org string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PVPLAN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PVPLAN_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := InitPvPlanSchema(ctx, pool); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM pv_orientations_history WHERE organization_id = $1`,
		`DELETE FROM pv_plan_config WHERE organization_id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, org); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	return pool
}

// The seed runs on every start. Once a station has a passport — seeded
// or edited — no later seed may touch it.
func TestSeedStationsLeavesAnEditedPassportAlone(t *testing.T) {
	const org = "pvplan-test"
	pool := pvPlanTestPool(t, org)
	ctx := context.Background()
	store := NewPvPlanStore(pool)

	seed := pvplan.Station{
		OrganizationID: org,
		Source:         pvplan.SourceN8N,
		PacLimitKw:     250,
		GammaPctPerC:   -0.28,
		Orientations: []pvplan.Orientation{
			{Idx: 1, TiltDeg: 30, AzimuthDeg: -13.3, ModulesCount: 504, ModulePowerW: 620, Enabled: true},
		},
	}
	written, err := store.SeedStations(ctx, []pvplan.Station{seed})
	if err != nil || len(written) != 1 {
		t.Fatalf("first seed: written=%v err=%v", written, err)
	}

	edited := seed
	edited.Source = pvplan.SourceModel
	edited.PacLimitKw = 260
	edited.Orientations = []pvplan.Orientation{
		{Idx: 1, TiltDeg: 25, AzimuthDeg: -13.3, ModulesCount: 504, ModulePowerW: 620, Enabled: true},
	}
	if _, err := store.SaveStation(ctx, edited, "engineer"); err != nil {
		t.Fatalf("save: %v", err)
	}

	written, err = store.SeedStations(ctx, []pvplan.Station{seed})
	if err != nil || len(written) != 0 {
		t.Fatalf("second seed: written=%v err=%v, want nothing written", written, err)
	}
	got, ok, err := store.Station(ctx, org)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Source != pvplan.SourceModel || got.PacLimitKw != 260 || got.Orientations[0].TiltDeg != 25 {
		t.Fatalf("passport = %+v, want the edit to survive the seed", got)
	}

	history, err := store.History(ctx, org, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || history[0].By != "engineer" || history[1].Changes[0].Field != "seeded" {
		t.Fatalf("history = %+v, want the edit then the seed", history)
	}
}

// The key is written, kept across edits that don't retype it, cleared
// by an empty string, and never part of the settings read.
func TestPvPlanSettingsKeepTheKeyApart(t *testing.T) {
	pool := pvPlanTestPool(t, "pvplan-test")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM pv_plan_settings`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	store := NewPvPlanStore(pool)

	if _, _, saved, err := store.Settings(ctx); err != nil || saved {
		t.Fatalf("before the first save: saved=%v err=%v", saved, err)
	}
	key := "om-key"
	commercial := pvplan.Settings{ForecastURL: "https://customer-api.open-meteo.com/v1/forecast"}
	if err := store.SaveSettings(ctx, commercial, &key); err != nil {
		t.Fatalf("save: %v", err)
	}
	edited := commercial
	edited.N8NWebhookURL = "https://n8n.example.com/webhook/x"
	if err := store.SaveSettings(ctx, edited, nil); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, keySet, saved, err := store.Settings(ctx)
	if err != nil || !saved || !keySet || got != edited {
		t.Fatalf("after the edit: %+v keySet=%v saved=%v err=%v", got, keySet, saved, err)
	}
	if stored, err := store.OpenMeteoKey(ctx); err != nil || stored != key {
		t.Fatalf("key = %q, %v; want it kept", stored, err)
	}

	cleared := ""
	if err := store.SaveSettings(ctx, edited, &cleared); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, keySet, _, _ := store.Settings(ctx); keySet {
		t.Fatal("key still stored after clearing it")
	}
}
