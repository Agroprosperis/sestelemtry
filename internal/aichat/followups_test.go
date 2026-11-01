package aichat

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nesh/sestelemetry/internal/openai"
)

func TestFollowUpsAsksForJSONAndCleansTheList(t *testing.T) {
	model := &fakeModel{turns: []openai.Result{{
		Text:  `{"questions":["1. Як було вчора?","Скільки виробила СЕС?","  «Який SOC зараз?»  ","- як було вчора?","Що з цінами РДН?","Зайве четверте?"]}`,
		Usage: openai.Usage{InputTokens: 300, OutputTokens: 40},
	}}}
	history := []Message{{Role: RoleUser, Text: "Скільки виробила СЕС?"}}
	tools := []Tool{
		{Name: "current_snapshot", Description: "Останні значення телеметрії об'єкта (опитування щосекунди): потужності СЕС."},
		{Name: "dam_prices", Description: "Ціни РДН за дні доставки. Для одного дня — погодинно."},
	}
	got, usage, err := FollowUps(context.Background(), model, history, "СЕС дала 1,2 МВт·год.", tools)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Як було вчора?", "Який SOC зараз?", "Що з цінами РДН?"}; !slices.Equal(got, want) {
		t.Errorf("questions = %q, want %q", got, want)
	}
	if usage.InputTokens != 300 {
		t.Errorf("usage = %+v", usage)
	}

	req := model.reqs[0]
	if req.Format == nil || req.Format.Name != "follow_ups" || len(req.Tools) != 0 {
		t.Errorf("request = %+v, want a JSON format and no tools", req)
	}
	for _, want := range []string{"Останні значення телеметрії об'єкта (опитування щосекунди)", "Ціни РДН за дні доставки"} {
		if !strings.Contains(req.Instructions, want) {
			t.Errorf("instructions lack the tool summary %q:\n%s", want, req.Instructions)
		}
	}
	if strings.Contains(req.Instructions, "Для одного дня") {
		t.Error("tool summary not cut at the first sentence")
	}
}

func TestFollowUpsRejectsAnswerThatIsNotJSON(t *testing.T) {
	model := &fakeModel{turns: []openai.Result{{Text: "Ось кілька питань: …"}}}
	if _, _, err := FollowUps(context.Background(), model, []Message{{Role: RoleUser, Text: "?"}}, "!", nil); err == nil {
		t.Fatal("prose accepted as the JSON answer")
	}
}
