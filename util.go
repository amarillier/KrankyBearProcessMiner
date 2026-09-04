package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	updatechecker "github.com/amarillier/go-update-checker"
	"github.com/hashicorp/go-version"
)

func checkFileExists(filePath string) bool {
	_, error := os.Stat(filePath)
	return !errors.Is(error, os.ErrNotExist)
}

// appConfigDirName returns the directory name for per-user app data
// (<UserConfigDir>/<appConfigDirName()>/...), stable regardless of
// elevation. appName itself gets an "Administrator: " prefix on an elevated
// Windows launch (main.go, so every window title shows it) -- using appName
// directly here would put a literal colon in a Windows path component
// (illegal there outside the drive-letter position), silently failing every
// os.MkdirAll/file write under it, and would also give an elevated run a
// completely separate state directory from a normal run's, splitting the
// persisted watchlist and update-check cache exactly the "one normal + one
// elevated" exception (see CLAUDE.md/Help) was added to keep working
// together. Found while adding a debug log path for Phase 2 ETW's
// network/disk-I/O work; fixed here once and reused by every call site
// below rather than repeating the mistake in a new one.
func appConfigDirName() string {
	return strings.TrimPrefix(appName, "Administrator: ")
}

// updateCheckStatePath returns the per-user path where the update checker
// caches the last-seen release tag, namespaced by repo (the library only
// caches tag/name, not which repo, so a bare filename could clash if this
// app ever checks more than one repo).
func updateCheckStatePath(repo string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appConfigDirName(), "latestcheck-"+repo+".json")
}

// updateChecker checks repoOwner/repo's latest published GitHub release against
// appVersion. minDaysInterval throttles the check: 0 = never throttled (manual
// "Check for Updates"), >0 for a quiet automatic check on launch (see
// checkForUpdatesAuto in update.go). remoteTag is the latest release's tag, so
// callers can tell via versionIsNewer when this build is *ahead* of it (an
// unpublished/dev build), not just whether an update is available.
func updateChecker(repoOwner string, repo string, repoName string, repodl string, minDaysInterval int) (msg string, updateAvailable bool, remoteTag string) {
	if p := updateCheckStatePath(repo); p != "" {
		updatechecker.SetCheckStatePath(p)
	}
	uc := updatechecker.New(repoOwner, repo, repoName, repodl, minDaysInterval, false)
	uc.CheckForUpdate(appVersion)
	return uc.Message, uc.UpdateAvailable, uc.RemoteTag
}

// versionIsNewer reports whether local is a strictly newer semantic version
// than remote -- i.e. this build is ahead of the latest published release (see
// CLAUDE.md's Update checker notes: shown with the HardHat badge rather than
// reported as "up to date"). Returns false if remote is empty (e.g. no
// published release was found, or the check was throttled with no prior cache)
// or either side fails to parse, since neither case can be confidently called
// "ahead".
func versionIsNewer(local, remote string) bool {
	if strings.TrimSpace(remote) == "" {
		return false
	}
	lv, errL := version.NewVersion(strings.TrimPrefix(strings.TrimSpace(local), "v"))
	rv, errR := version.NewVersion(strings.TrimPrefix(strings.TrimSpace(remote), "v"))
	if errL != nil || errR != nil {
		return false
	}
	return lv.GreaterThan(rv)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
