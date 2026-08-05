// Package main - i18n.go wires the UI message catalog (internal/i18n) into the app.
// English ships embedded in the binary; optional locale packs (assets/i18n/<code>.json)
// are merged at startup so adding a language needs no rebuild. The active locale comes
// from the -lang flag, then the saved preference, defaulting to English.
//
// Reusable i18n scaffolding. To activate it in a new app:
//   - add a `-lang` string flag in main() and call setupI18n(app, *langFlag) right after
//     fyne.NewApp(), before building any UI;
//   - add a Language picker to Preferences using uiLanguageOptions() (persist the choice
//     to i18n.PrefKeyUILanguage and prompt for a restart — a live menu rebuild can crash
//     SetMainMenu on macOS);
//   - migrate literal UI strings to i18n.T(...) / i18n.TC(...) over time.
package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"fyne.io/fyne/v2"

	"processminer/internal/apppaths"
	"processminer/internal/i18n"
)

// localePackJSON matches a two-letter locale pack file, e.g. de.json (not en.json-only).
var localePackJSON = regexp.MustCompile(`^[a-z]{2}\.json$`)

// setupI18n initialises the catalog: load embedded English, merge any on-disk locale
// packs, resolve the active language (flag → preference → English), and align Fyne's
// own widget chrome to that language. Must run after fyne.NewApp() and before any UI
// is built. A failure to load the embedded English catalog is fatal (it ships in-binary).
func setupI18n(a fyne.App, langFlag string) {
	if err := i18n.Init(); err != nil {
		log.Fatalf("i18n: %v", err)
	}
	mergeI18nFromAssets()
	applyUILanguage(a, langFlag)
}

// mergeI18nFromAssets merges every assets/i18n/<code>.json locale pack found beside the
// executable, inside a macOS .app bundle, or in the working directory. Dropping in or
// removing a pack changes the available languages without a rebuild.
func mergeI18nFromAssets() {
	dir := apppaths.ResolveBundledDir("assets/i18n")
	if dir == "" {
		return // no packs on disk; embedded English is enough
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "en.json" || !localePackJSON.MatchString(name) {
			continue // English is embedded; ignore non-pack JSON
		}
		code := strings.TrimSuffix(name, ".json")
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(b) == 0 {
			continue
		}
		if err := i18n.MergeLocale(code, b); err != nil {
			log.Printf("i18n: skip locale pack %s: %v", name, err)
		}
	}
}

// applyUILanguage resolves the active locale: a non-empty -lang flag wins (and is saved),
// otherwise the saved preference, defaulting to English. Unknown codes fall back to English.
// It then aligns Fyne's built-in widget strings to the chosen language.
func applyUILanguage(a fyne.App, langFlag string) {
	prefs := a.Preferences()
	lang := strings.TrimSpace(langFlag)
	if lang == "" {
		lang = strings.TrimSpace(prefs.StringWithFallback(i18n.PrefKeyUILanguage, "en"))
	}
	code := i18n.NormalizeLocale(lang)
	if !i18n.HasCatalog(code) {
		code = "en"
	}
	prefs.SetString(i18n.PrefKeyUILanguage, code)
	i18n.SetLocale(code)
	i18n.ApplyFyneLocaleOverride()
}

// uiLanguageOptions returns the display labels for the Preferences language picker and a
// map from each label back to its locale code. Labels are the languages' own endonyms
// (e.g. "English", "Deutsch"), in catalog order (English first).
func uiLanguageOptions() (labels []string, codeByLabel map[string]string) {
	codeByLabel = map[string]string{}
	for _, code := range i18n.SortedCatalogLocales() {
		label := i18n.LocaleDisplayName(code)
		if _, clash := codeByLabel[label]; clash {
			label = label + " (" + code + ")" // disambiguate identical endonyms
		}
		labels = append(labels, label)
		codeByLabel[label] = code
	}
	return labels, codeByLabel
}
