package main

import (
	"testing"

	"github.com/nesh/sestelemetry/internal/config"
)

func TestOrgsWithLocationSkipsOrgsWithoutLocation(t *testing.T) {
	cfg := &config.Root{
		Organizations: []config.Organization{
			{ID: "with-loc", Location: &config.Location{Latitude: 49, Longitude: 28}},
			{ID: "without-loc"},
			{ID: "another", Location: &config.Location{Latitude: 50, Longitude: 30, City: "Київ"}},
		},
	}
	got := orgsWithLocation(cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 orgs with location, got %d", len(got))
	}
	if got[0].ID != "with-loc" || got[0].Latitude != 49 || got[0].Longitude != 28 {
		t.Fatalf("orgs[0]: %+v", got[0])
	}
	if got[1].ID != "another" || got[1].Latitude != 50 || got[1].Longitude != 30 {
		t.Fatalf("orgs[1]: %+v", got[1])
	}
}

func TestOrgsWithLocationEmptyConfig(t *testing.T) {
	cfg := &config.Root{}
	got := orgsWithLocation(cfg)
	if len(got) != 0 {
		t.Fatalf("expected 0 orgs, got %d", len(got))
	}
}
