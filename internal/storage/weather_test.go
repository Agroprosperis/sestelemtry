package storage

import (
	"context"
	"testing"
	"time"
)

func TestInitWeatherSchemaRequiresPool(t *testing.T) {
	if err := InitWeatherSchema(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestUpsertWeatherHourlyEmptyNoPoolNeeded(t *testing.T) {
	if err := UpsertWeatherHourly(context.Background(), nil, nil); err != nil {
		t.Fatalf("expected nil for empty rows, got %v", err)
	}
}

func TestUpsertWeatherHourlyRequiresPoolForNonEmpty(t *testing.T) {
	err := UpsertWeatherHourly(context.Background(), nil, []WeatherHourlyRow{{
		OrganizationID: "org-a",
		Hour:           time.Now(),
		SourceURL:      "http://example.com",
	}})
	if err == nil {
		t.Fatal("expected error for nil pool with non-empty rows")
	}
}

func TestUpsertWeatherDailyEmptyNoPoolNeeded(t *testing.T) {
	if err := UpsertWeatherDaily(context.Background(), nil, nil); err != nil {
		t.Fatalf("expected nil for empty rows, got %v", err)
	}
}

func TestUpsertWeatherDailyRequiresPoolForNonEmpty(t *testing.T) {
	err := UpsertWeatherDaily(context.Background(), nil, []WeatherDailyRow{{
		OrganizationID: "org-a",
		Day:            time.Now(),
		SourceURL:      "http://example.com",
	}})
	if err == nil {
		t.Fatal("expected error for nil pool with non-empty rows")
	}
}

func TestQueryWeatherHourlyRequiresPool(t *testing.T) {
	if _, err := QueryWeatherHourly(context.Background(), nil, "org-a", time.Now(), time.Now()); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestQueryWeatherDailyRequiresPool(t *testing.T) {
	if _, err := QueryWeatherDaily(context.Background(), nil, "org-a", time.Now(), time.Now()); err == nil {
		t.Fatal("expected error for nil pool")
	}
}
