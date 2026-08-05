// Package apppaths resolves bundled assets (e.g. assets/i18n/de.json) across the
// dev layout and packaged layouts (next to the executable, inside a macOS .app
// bundle's Contents/Resources, or the working directory). Ported from the same
// helper in ../TaniumGeminatus so the i18n package can find on-disk locale packs.
package apppaths

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveBundledAsset finds rel (e.g. assets/i18n/de.json) for packaged and dev layouts.
// Order: next to the executable, then Contents/Resources on macOS .app bundles, then working directory.
func ResolveBundledAsset(rel string) string {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return ""
	}
	for _, p := range bundledSearchPaths(rel) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// ResolveBundledDir finds rel as a directory using the same search order as ResolveBundledAsset.
func ResolveBundledDir(rel string) string {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return ""
	}
	for _, p := range bundledSearchPaths(rel) {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

func bundledSearchPaths(rel string) []string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, rel))
		if strings.EqualFold(filepath.Base(exeDir), "MacOS") {
			candidates = append(candidates, filepath.Join(filepath.Dir(exeDir), "Resources", rel))
		}
	}
	candidates = append(candidates, rel)
	return candidates
}
