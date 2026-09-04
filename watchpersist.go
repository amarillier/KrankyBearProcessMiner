package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// persistedProcessWatch is one process-watch entry as saved to disk. PIDs
// aren't meaningful across a restart (see ReleaseNotes.txt's own framing of
// this feature), so the durable identity is the executable path -- ExePath
// is the strong, unambiguous key; Name is kept as a fallback match (and for
// display) for the rare case a process already exited before its exe path
// could be resolved at quit time (resolveProcessExe returns "" then, same as
// everywhere else it's used).
type persistedProcessWatch struct {
	Name    string
	ExePath string
}

// persistedWatchState is the whole file's shape -- directories restore
// silently (see restorePersistedDirectoryWatches), processes go through
// pendingProcessWatches and the reactivation dialog instead (see
// watchreactivateview.go).
type persistedWatchState struct {
	Processes   []persistedProcessWatch
	Directories []string
}

// pendingProcessWatches holds every persisted process-watch entry not yet
// reactivated or forgotten this session -- loaded once at startup
// (loadPendingProcessWatches), mutated by the reactivation dialog (an entry
// is removed the moment it's reactivated or forgotten; left alone otherwise
// means "still pending," same as if the dialog were never shown at all), and
// read back by persistWatchStateAtQuit so nothing pending is silently lost.
// A package-level var, same session-state convention this codebase already
// uses throughout (e.g. interferenceWindow/-Open).
var pendingProcessWatches []persistedProcessWatch

// watchStateFilePath mirrors util.go's updateCheckStatePath exactly -- same
// <UserConfigDir>/<appName>/ directory the update checker's own JSON cache
// already uses, just a different filename. Deliberately a plain JSON file,
// not Fyne Preferences: this app has never stored a list or structured blob
// in Preferences (confirmed by inspection -- every existing Preferences call
// here is a scalar float or string), so there's no existing convention there
// to extend, while this JSON-file approach already has a working precedent
// in this exact codebase.
func watchStateFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appConfigDirName(), "watchlist.json")
}

// loadWatchState reads the persisted watch state. A missing or corrupt file
// is treated as "nothing persisted" -- no error surfaced, same best-effort,
// silent-when-absent style this app already uses for other optional state
// (e.g. the update checker's own cache, ETW signals being silently inert
// where unsupported).
func loadWatchState() persistedWatchState {
	path := watchStateFilePath()
	if path == "" {
		return persistedWatchState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedWatchState{}
	}
	var state persistedWatchState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedWatchState{}
	}
	return state
}

// loadPendingProcessWatches populates pendingProcessWatches from disk --
// called once at startup, before any process snapshot exists to match
// against (matching itself happens later, once offerWatchReactivation has a
// real snapshot -- see main.go).
func loadPendingProcessWatches() {
	pendingProcessWatches = loadWatchState().Processes
}

// restorePersistedDirectoryWatches silently re-arms every persisted
// directory watch -- the backlog's own framing calls this half
// straightforward, and it is: a directory watch is just a path, valid
// immediately, no process snapshot or review step needed. Called once at
// startup right after the interferenceWatcher exists.
func restorePersistedDirectoryWatches(watcher *interferenceWatcher) {
	for _, dir := range loadWatchState().Directories {
		watcher.watchDir(dir)
	}
}

// persistWatchStateAtQuit saves the current watch state: every actively
// watched process (resolved fresh to name+exe path, regardless of whether it
// was reactivated from a previous session or added new this one) plus
// whatever's left in pendingProcessWatches that was never reactivated or
// forgotten -- so closing the reactivation dialog without acting on an entry
// doesn't lose it. Directories save verbatim from the live watcher, so a
// directory removed via the existing "Remove" button in the Watched Targets
// list is naturally dropped too.
func persistWatchStateAtQuit(watcher *interferenceWatcher) {
	path := watchStateFilePath()
	if path == "" || watcher == nil {
		return
	}

	state := persistedWatchState{
		Directories: append([]string(nil), watcher.dirs...),
	}
	for pid, name := range watcher.pids {
		state.Processes = append(state.Processes, persistedProcessWatch{
			Name:    name,
			ExePath: resolveProcessExe(pid),
		})
	}
	state.Processes = append(state.Processes, pendingProcessWatches...)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// watchReactivationRow is one pending entry's current match state against the
// live process snapshot passed to buildWatchReactivationRows -- see
// watchreactivateview.go for how this renders.
type watchReactivationRow struct {
	Entry       persistedProcessWatch
	MatchedPIDs []int32
}

// buildWatchReactivationRows matches every pendingProcessWatches entry
// against byPID, one row each. Resolves every running PID's exe path exactly
// once regardless of how many pending entries there are (a one-time startup
// cost bounded by the process count, not by entries x processes) -- the same
// "don't repeat gopsutil's per-tick cost mistake for a one-off gather" care
// this app's other one-off snapshots (Threads, Handles) already take,
// applied here even though this only ever runs once per launch.
func buildWatchReactivationRows(byPID map[int32]ProcInfo) []watchReactivationRow {
	if len(pendingProcessWatches) == 0 {
		return nil
	}

	exeByPID := make(map[int32]string, len(byPID))
	for pid := range byPID {
		exeByPID[pid] = resolveProcessExe(pid)
	}

	rows := make([]watchReactivationRow, 0, len(pendingProcessWatches))
	for _, entry := range pendingProcessWatches {
		row := watchReactivationRow{Entry: entry}
		for pid, p := range byPID {
			switch {
			case entry.ExePath != "":
				if exeByPID[pid] == entry.ExePath {
					row.MatchedPIDs = append(row.MatchedPIDs, pid)
				}
			case p.Name == entry.Name:
				row.MatchedPIDs = append(row.MatchedPIDs, pid)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// removePendingProcessWatch drops entry from pendingProcessWatches -- called
// by the reactivation dialog whether the entry was just reactivated or
// forgotten, since either way it's no longer "pending review."
func removePendingProcessWatch(entry persistedProcessWatch) {
	out := pendingProcessWatches[:0]
	for _, e := range pendingProcessWatches {
		if e != entry {
			out = append(out, e)
		}
	}
	pendingProcessWatches = out
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
