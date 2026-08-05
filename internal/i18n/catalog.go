// Package i18n provides the UI message catalog for this app.
// English defaults ship embedded in the binary; other locales load at runtime from
// <install>/assets/i18n/<code>.json. Look up short strings with T / TC (dot-path keys
// such as "menu.file.preferences"); long-form copy (help, about, updates, license)
// uses the *Body helpers in bundled_text.go.
//
// This is reusable scaffolding (the engine is app-agnostic). To adopt it in a new app:
//  1. call setupI18n(app, langFlag) early in main(), before building any UI (see i18n.go);
//  2. fill internal/i18n/embedded_defaults/* and assets/i18n/* with your strings;
//  3. replace literal UI strings with i18n.T("...") / i18n.TC("...", vars) as you go.
package i18n

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// English-only defaults ship in the binary. Other locales load from
// <install>/assets/i18n/<code>.json at runtime.
// Long-form copy (help, about, updates, license) uses assets/i18n/<name>_<code>.txt with English
// embedded under embedded_defaults/*_en.txt; see bundled_text.go.
//
//go:embed embedded_defaults/en.json
var embeddedDefaultEN []byte

// Well-known preference keys (Fyne) for UI language.
const (
	PrefKeyUILanguage           = "ui.language"
	PrefKeyUILanguagePromptDone = "ui.language_prompt_done"
)

var (
	mu       sync.RWMutex
	locale   = "en"
	catalogs map[string]map[string]interface{}
)

// NormalizeLocale turns "ja-JP", "JA_jp" into "ja". Empty string becomes "en".
func NormalizeLocale(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "en"
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '_'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Init loads the embedded English catalog only. Other locales are merged from assets/i18n/<code>.json at startup.
func Init() error {
	if len(embeddedDefaultEN) == 0 {
		return fmt.Errorf("i18n: missing embedded English catalog")
	}
	enRoot, err := parseRoot(embeddedDefaultEN)
	if err != nil {
		return fmt.Errorf("i18n: parse embedded en: %w", err)
	}
	mu.Lock()
	catalogs = map[string]map[string]interface{}{
		"en": enRoot,
	}
	mu.Unlock()
	return nil
}

// SortedCatalogLocales returns locale codes that have a loaded catalog, sorted with "en" first.
func SortedCatalogLocales() []string {
	mu.RLock()
	defer mu.RUnlock()
	if catalogs == nil {
		return []string{"en"}
	}
	rest := make([]string, 0, len(catalogs))
	hasEn := false
	for code, root := range catalogs {
		if code == "" || root == nil {
			continue
		}
		if code == "en" {
			hasEn = true
			continue
		}
		rest = append(rest, code)
	}
	sort.Strings(rest)
	if hasEn {
		return append([]string{"en"}, rest...)
	}
	return rest
}

// LocaleDisplayName returns a human-readable name for a catalog code (e.g. "nl" → "Nederlands").
// It reads from that locale's own catalog so contributor packs only need locale_labels.<code>
// or a single locale_labels.self in their JSON; falls back to code if missing.
func LocaleDisplayName(code string) string {
	code = NormalizeLocale(code)
	if code == "" {
		return ""
	}
	key := "locale_labels." + code
	s := TCInLocale(code, key, nil)
	if s != "" && s != key {
		return s
	}
	s = TCInLocale(code, "locale_labels.self", nil)
	if s != "" && s != "locale_labels.self" {
		return s
	}
	return code
}

// MergeLocale replaces a locale from raw JSON bytes (e.g. newer assets/i18n/ja.json during dev).
func MergeLocale(code string, raw []byte) error {
	code = NormalizeLocale(code)
	if code == "" {
		return fmt.Errorf("i18n: empty locale")
	}
	root, err := parseRoot(raw)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if catalogs == nil {
		return fmt.Errorf("i18n: Init not called")
	}
	catalogs[code] = root
	return nil
}

func parseRoot(raw []byte) (map[string]interface{}, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	return root, nil
}

// SetLocale sets the active locale for T / TC (e.g. "en", "ja").
func SetLocale(code string) {
	mu.Lock()
	locale = NormalizeLocale(code)
	mu.Unlock()
}

// Locale returns the active locale code.
func Locale() string {
	mu.RLock()
	defer mu.RUnlock()
	return locale
}

// T returns a message for key (dot path such as "menu.file.preferences").
// Missing keys fall back to English, then to key.
func T(key string) string {
	return TC(key, nil)
}

// TC is like T but replaces {{name}} placeholders from vars.
func TC(key string, vars map[string]string) string {
	mu.RLock()
	loc := locale
	cat := catalogs
	mu.RUnlock()
	return tcWith(cat, loc, key, vars)
}

// TCInLocale is like TC but uses loc as the catalog without changing the active locale.
func TCInLocale(loc string, key string, vars map[string]string) string {
	loc = NormalizeLocale(loc)
	mu.RLock()
	cat := catalogs
	mu.RUnlock()
	return tcWith(cat, loc, key, vars)
}

func tcWith(cat map[string]map[string]interface{}, loc string, key string, vars map[string]string) string {
	if cat == nil {
		return key
	}
	parts := strings.Split(key, ".")
	s, ok := lookup(cat, loc, parts)
	if !ok && loc != "en" {
		s, ok = lookup(cat, "en", parts)
	}
	if !ok {
		return key
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// HasCatalog reports whether a non-empty message catalog exists for code (e.g. ja, de).
func HasCatalog(code string) bool {
	code = NormalizeLocale(code)
	mu.RLock()
	defer mu.RUnlock()
	if catalogs == nil {
		return false
	}
	root, ok := catalogs[code]
	return ok && root != nil
}

func lookup(cat map[string]map[string]interface{}, loc string, parts []string) (string, bool) {
	root := cat[loc]
	if root == nil {
		return "", false
	}
	return getString(root, parts)
}

func getString(m map[string]interface{}, parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	head := parts[0]
	if strings.HasPrefix(head, "_") {
		return "", false
	}
	v, ok := m[head]
	if !ok {
		return "", false
	}
	if len(parts) == 1 {
		s, ok := v.(string)
		return s, ok
	}
	next, ok := v.(map[string]interface{})
	if !ok {
		return "", false
	}
	return getString(next, parts[1:])
}
