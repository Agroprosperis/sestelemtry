package main

import "testing"

func TestWorthOpeningOnlyShows(t *testing.T) {
	cases := map[string]bool{
		"/account_legal": true,
		"/account_legal/view_main_details?a=15905&highlight_main=account_legal": true,
		"/account_household/show_only_disconnections?eic=62Z1234567890123":      true,
		"/account_legal_osr_tt/select_indication_ty_osr_tt?a=15905":             false,
		"/user/logout?_next=/":                     false,
		"/user/change_password":                    false,
		"/account_legal/send_request?a=15905":      false,
		"/ip_cabinet/static/css/style.css":         false,
		"/useful_info/tarif_info":                  false,
		"/account_legal/view_main_details/extra/x": false,
	}
	for p, want := range cases {
		if got := worthOpening(p); got != want {
			t.Errorf("worthOpening(%q) = %v, want %v", p, got, want)
		}
	}
}
