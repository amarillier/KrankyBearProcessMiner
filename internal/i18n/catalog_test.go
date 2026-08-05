package i18n

import "testing"

func TestNormalizeLocale(t *testing.T) {
	cases := map[string]string{
		"":       "en",
		"en":     "en",
		"EN":     "en",
		"ja-JP":  "ja",
		"JA_jp":  "ja",
		"  de  ": "de",
		"pt-BR":  "pt",
	}
	for in, want := range cases {
		if got := NormalizeLocale(in); got != want {
			t.Errorf("NormalizeLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInitAndLookup(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	SetLocale("en")
	if got := T("app.name"); got != "Template App" {
		t.Errorf("T(app.name) = %q", got)
	}
	if got := T("common.close"); got != "Close" {
		t.Errorf("T(common.close) = %q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing key = %q, want the key back", got)
	}
}

func TestInterpolation(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got := TC("windows.help_title", map[string]string{"app_name": "Template App"})
	if got != "Template App - Help" {
		t.Errorf("TC(windows.help_title) = %q", got)
	}
}

func TestMergeAndFallback(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	raw := []byte(`{"common":{"close":"Fermer"},"locale_labels":{"fr":"Français"}}`)
	if err := MergeLocale("fr", raw); err != nil {
		t.Fatalf("MergeLocale: %v", err)
	}
	if !HasCatalog("fr") {
		t.Fatal("HasCatalog(fr) = false")
	}
	SetLocale("fr")
	defer SetLocale("en")
	if got := T("common.close"); got != "Fermer" {
		t.Errorf("fr common.close = %q, want Fermer", got)
	}
	if got := T("common.cancel"); got != "Cancel" {
		t.Errorf("fr common.cancel = %q, want English fallback Cancel", got)
	}
	if got := LocaleDisplayName("fr"); got != "Français" {
		t.Errorf("LocaleDisplayName(fr) = %q", got)
	}
}
