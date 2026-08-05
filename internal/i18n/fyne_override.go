package i18n

import (
	"embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
)

// Fyne's built-in widget strings ("Error" dialog title, OK/Cancel/Yes/No buttons, file picker
// chrome, calendar weekdays, entry context menu) are rendered through its own translation
// bundle, picked via lang.SystemLocale() — which on Windows uses GetUserDefaultLocaleName
// (the OS *region* setting), not GetUserPreferredUILanguages (the display language). That
// means a user running Windows in English display language with Region set to Japan
// would see Japanese Fyne chrome even though they explicitly chose English in our app's
// language picker.
//
// ApplyFyneLocaleOverride works around this by registering OUR copy of Fyne's bundle for
// the user's *chosen* app language under whatever locale tag Fyne's localizer settled on.
// Fyne's localizer then renders user-language content even when the OS region disagrees.
//
// Limitations:
//   - If the user's chosen language has no Fyne bundle (it/ko/nl/af/hb in Fyne v2.7.4),
//     this is a no-op and Fyne falls back to English for its chrome — same as today.
//   - One Fyne dialog (the file-overwrite confirmation) is hardcoded English at the source;
//     its Yes/No buttons get fixed by this override but its title/body do not.

//go:embed fyne_bundles/*.json
var fyneBundlesFS embed.FS

// fyneBundleForLocale returns embedded Fyne translation bundle bytes for the given locale,
// or nil if we don't ship one. Locale codes match Fyne's bundle filenames (no region tag).
func fyneBundleForLocale(code string) []byte {
	if code == "" {
		return nil
	}
	data, err := fyneBundlesFS.ReadFile("fyne_bundles/" + code + ".json")
	if err != nil {
		return nil
	}
	return data
}

// ApplyFyneLocaleOverride forces Fyne's built-in widget strings to match the user's chosen
// app language regardless of OS region. Never errors fatally (a bad override would just
// leave Fyne's chrome at its OS-detected locale, which is the current behaviour anyway).
//
// Call exactly once at startup, after fyne.NewApp() and i18n.Init() — the chosen language
// must be resolved first.
func ApplyFyneLocaleOverride() {
	userLang := NormalizeLocale(Locale())
	if userLang == "" {
		return
	}
	bundle := fyneBundleForLocale(userLang)
	if bundle == nil {
		// User picked a language Fyne doesn't ship a bundle for (e.g. it/ko/nl/af/hb).
		// Nothing we can do here — Fyne's own fallback will pick English for chrome.
		return
	}
	fyneAuto := NormalizeLocale(lang.SystemLocale().LanguageString())
	if fyneAuto == userLang {
		// Fyne's auto-detection already matches the user's choice — no override needed.
		return
	}
	// Register OUR copy of Fyne's user-language bundle under the OS-detected locale tag.
	if err := lang.AddTranslationsForLocale(bundle, fyne.Locale(fyneAuto)); err != nil {
		// Failure is non-fatal; the chrome stays at whatever Fyne picked.
		_ = err
	}
}
