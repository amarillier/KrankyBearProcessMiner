package i18n

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"processminer/internal/apppaths"
)

//go:embed embedded_defaults/help_en.txt
var helpBodyEN []byte

//go:embed embedded_defaults/about_en.txt
var aboutBodyEN []byte

//go:embed embedded_defaults/updates_en.txt
var updatesBodyEN []byte

//go:embed embedded_defaults/license_en.txt
var licenseBodyEN []byte

// loadLocalizedAssetFile returns assets/i18n/<prefix>_<locale>.txt when present beside the app,
// then assets/i18n/<prefix>_en.txt when locale is not English, then embedded English bytes.
func loadLocalizedAssetFile(prefix string, loc string, embedded []byte) []byte {
	loc = NormalizeLocale(loc)
	if loc != "" {
		rel := filepath.FromSlash("assets/i18n/" + prefix + "_" + loc + ".txt")
		if p := apppaths.ResolveBundledAsset(rel); p != "" {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				return b
			}
		}
	}
	if loc != "en" {
		rel := filepath.FromSlash("assets/i18n/" + prefix + "_en.txt")
		if p := apppaths.ResolveBundledAsset(rel); p != "" {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				return b
			}
		}
	}
	return embedded
}

// HelpBody returns the in-app help document for the active locale, with {{app_name}} replaced.
func HelpBody(appName string) string {
	loc := NormalizeLocale(Locale())
	raw := loadLocalizedAssetFile("help", loc, helpBodyEN)
	return strings.ReplaceAll(string(raw), "{{app_name}}", appName)
}

// AboutBody returns About dialog text with {{app_name}}, {{version}}, and {{copyright}} replaced.
func AboutBody(appName, version, copyright string) string {
	loc := NormalizeLocale(Locale())
	raw := loadLocalizedAssetFile("about", loc, aboutBodyEN)
	s := string(raw)
	s = strings.ReplaceAll(s, "{{app_name}}", appName)
	s = strings.ReplaceAll(s, "{{version}}", version)
	s = strings.ReplaceAll(s, "{{copyright}}", copyright)
	return s
}

// UpdatesBody returns the Check for Updates dialog / CLI text with {{version}} and {{releases_url}} replaced.
func UpdatesBody(version, releasesURL string) string {
	loc := NormalizeLocale(Locale())
	raw := loadLocalizedAssetFile("updates", loc, updatesBodyEN)
	s := string(raw)
	s = strings.ReplaceAll(s, "{{version}}", version)
	s = strings.ReplaceAll(s, "{{releases_url}}", releasesURL)
	return s
}

// LicenseLegalText returns the license prose for the active locale (About / License windows).
func LicenseLegalText() string {
	loc := NormalizeLocale(Locale())
	raw := loadLocalizedAssetFile("license", loc, licenseBodyEN)
	return string(raw)
}
