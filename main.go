package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"

	fynetooltip "github.com/dweymouth/fyne-tooltip"

	"processminer/internal/startup"
)

const (
	// appName    = "KrankyBear ProcessMiner"
	appVersion = "0.7.0" // see FyneApp.toml
	appAuthor  = "Allan Marillier"
	appID      = "com.github.amarillier.KrankyBearProcessMiner"
)

var appName = "KrankyBear ProcessMiner"
var appCopyright = buildCopyrightNotice()

// adminTitlePrefix returns "Administrator: " when running elevated, ""
// otherwise -- for windows whose title doesn't include appName at all
// (Threads/Children, titled from the target process's own name+PID, e.g.
// "chrome.exe (PID 1234) — Threads") and so never picked up the same
// elevation prefix appName's own change gives every other window for free.
// Found via real-world testing: with one normal + one elevated instance
// both able to open a Threads/Children window for the same or different
// processes, there was no way to tell which instance's window was which.
func adminTitlePrefix() string {
	if isCurrentProcessElevated() {
		return "Administrator: "
	}
	return ""
}

// procSampler mirrors the package-level aboutWindow/helpWindow pattern: a
// single long-lived background worker that must be stopped before quit (see
// quitApp and CLAUDE.md's teardown-order rule).
var procSampler *Sampler

// saveProcessLayout persists the process view's split-divider positions; set
// once newProcessView runs, called from quitApp alongside saveMainWindowGeometry.
var saveProcessLayout func()

// saveWatchList persists Interference Watch's watch list (see
// watchpersist.go); set once interferenceWatcherRef exists, called from
// quitApp the same way saveProcessLayout is.
var saveWatchList func()

// watchReactivationOffered guards offerWatchReactivation to run at most once
// per launch -- OnProcessSnapshot fires on every sample tick, but there's
// only ever one reactivation dialog to show, on the first snapshot that
// gives it something real to match against.
var watchReactivationOffered bool

func buildCopyrightNotice() string {
	const startYear = 2026
	currentYear := time.Now().Year()
	if currentYear <= startYear {
		return "Copyright (c) Allan Marillier, 2026"
	}
	return fmt.Sprintf("Copyright (c) Allan Marillier, 2026-%d", currentYear)
}

func main() {
	langFlag := flag.String("lang", "", "UI language code (e.g. en, de); overrides the saved preference for this run")
	mesaFallbackFlag := flag.Bool(startup.MesaFallbackFlagName, false, "internal: relaunch flag for the Mesa3D OpenGL fallback (Windows only)")
	flag.Parse()

	startup.EnsureWindowsOpenGLReady(*mesaFallbackFlag)

	a := app.NewWithID(appID)
	a.SetIcon(resourceKrankyBearProcessMinerPng)
	setupI18n(a, *langFlag) // load message catalog + resolve UI language before building any UI
	loadTheme(a)

	if anotherInstanceRunning() {
		showAlreadyRunningAndExit(a)
		return
	}

	// Windows' own convention for an elevated console window's title bar --
	// matters more here than usual since running elevated is now a real,
	// user-visible mode (see singleinstance.go's one normal + one elevated
	// exception), not just an internal detail. appName is a var (not a
	// const) specifically so every window built from it -- System Info,
	// Resource Details, Capture Trace, etc. -- picks this up too, not just
	// the main window.
	if isCurrentProcessElevated() {
		appName = "Administrator: " + appName
	}

	win := a.NewWindow(appName)
	win.SetIcon(resourceKrankyBearProcessMinerPng)
	win.Resize(mainWindowLaunchSize(a)) // restore previous size (size only - Fyne can't restore position)

	procSampler = NewSampler()
	processView, applyProcessSnapshot, refreshNow, endSelected, saveLayout, interferenceWatcherRef, onWatchListChanged, jumpToPID := newProcessView(a, win, procSampler)
	saveProcessLayout = saveLayout
	showInterference := func() { showInterferenceWindow(a, interferenceWatcherRef, onWatchListChanged) }

	// Restore persisted watches (see watchpersist.go): directories re-arm
	// silently right away (just a path, no process snapshot needed);
	// process watches load as pending and wait for offerWatchReactivation
	// below, which needs a real snapshot to match against.
	restorePersistedDirectoryWatches(interferenceWatcherRef)
	loadPendingProcessWatches()
	saveWatchList = func() { persistWatchStateAtQuit(interferenceWatcherRef) }

	updateResourceDetail, updateResourceDetailProcesses, showResourceDetail := newResourceDetailWindow(a, jumpToPID)
	graphsView, updateGraphs := newSystemGraphsView(showResourceDetail)

	procSampler.OnSystemSnapshot(func(s SystemSnapshot) {
		fyne.Do(func() {
			updateGraphs(s)
			updateResourceDetail(s)
		})
	})
	procSampler.OnProcessSnapshot(func(s ProcessSnapshot) {
		fyne.Do(func() {
			applyProcessSnapshot(s)
			updateResourceDetailProcesses(s)
			if !watchReactivationOffered {
				watchReactivationOffered = true
				byPID := make(map[int32]ProcInfo, len(s.Procs))
				for _, p := range s.Procs {
					byPID[p.PID] = p
				}
				offerWatchReactivation(a, interferenceWatcherRef, byPID)
			}
		})
	})

	// Wraps the window content in a tooltip render layer so ttwidget-based
	// controls (table headers, buttons, checks, selects) actually show their
	// SetToolTip text -- Fyne itself has no built-in tooltip support yet.
	// Torn down in quitApp.
	win.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewBorder(graphsView, nil, nil, nil, processView), win.Canvas()))
	win.SetMainMenu(buildMenu(a, win, refreshNow, endSelected, showResourceDetail, showInterference))
	setupSystemTray(a, win, refreshNow, endSelected, showResourceDetail, showInterference)

	// Boss-key hide (CLAUDE.md "Hide all / show all windows"): no matching
	// show/resume hotkey by design -- canvas shortcuts only fire on a
	// focused window, so a hidden window can't be un-hidden by hotkey
	// anyway. Reveal via tray/menu "Show All" instead.
	win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyH, Modifier: fyne.KeyModifierAlt},
		func(fyne.Shortcut) { hideAllWindows(win) })

	// Closing the window quits the app. Deferred via fyne.Do so quit() runs on a
	// clean loop iteration outside whatever callback triggered it — quitting
	// directly from inside a menu-item click or close-intercept callback can hang
	// on Windows (see CLAUDE.md "Quitting cleanly").
	win.SetCloseIntercept(func() { fyne.Do(func() { quitApp(a, win) }) })

	checkForUpdatesAuto(a) // quiet, once-per-day check; dialog only if an update exists

	// Best-effort, silent: the 4th Interference Watch signal (Defender
	// AMFilter file-scan monitoring) needs Administrator rights to create
	// its real-time ETW session at all (confirmed empirically), same
	// requirement Capture Trace already has. Unlike starting a capture,
	// this isn't a deliberate user action with its own dialog -- it's a
	// bonus signal that's simply not there if unelevated (Interference
	// Watch's other three signals work fine either way), so any error here
	// is discarded rather than surfaced.
	_ = startAVMonitor()

	procSampler.Start() // start background sampling only once content/menu/tray are wired

	win.ShowAndRun()
}

// ── Window geometry ──────────────────────────────────────────────────────────
// Fyne has no cross-platform window position/display restore, so only size is
// persisted (see CLAUDE.md "Window size persistence").

const (
	prefWinWidth  = "mainWindowWidth"
	prefWinHeight = "mainWindowHeight"
	minWinWidth   = 400
	minWinHeight  = 300
	maxWinDim     = 8000
	defaultWinW   = 900
	defaultWinH   = 650
)

func mainWindowLaunchSize(a fyne.App) fyne.Size {
	w := a.Preferences().FloatWithFallback(prefWinWidth, defaultWinW)
	h := a.Preferences().FloatWithFallback(prefWinHeight, defaultWinH)
	if w < minWinWidth || w > maxWinDim {
		w = defaultWinW
	}
	if h < minWinHeight || h > maxWinDim {
		h = defaultWinH
	}
	return fyne.NewSize(float32(w), float32(h))
}

func saveMainWindowGeometry(a fyne.App, win fyne.Window) {
	size := win.Canvas().Size()
	a.Preferences().SetFloat(prefWinWidth, float64(size.Width))
	a.Preferences().SetFloat(prefWinHeight, float64(size.Height))
}

// quitting guards quitApp against a second trigger (window close, File>Quit,
// tray Quit, or the same one clicked twice) while a first call's background
// teardown goroutine is still running -- without this, a second call would
// show a second progress dialog and double-run every Stop()/save call.
var quitting atomic.Bool

// quitApp does teardown in the order CLAUDE.md calls out: stop background work
// first, then persist geometry, then quit. cancelCapture goes first, even
// before procSampler.Stop() -- an active WPR trace session is an OS-level
// resource that outlives this process if not explicitly torn down, unlike
// procSampler's in-process goroutine (see capture_windows.go).
//
// Confirmed via real-world testing that stopping a live kernel ETW session
// (cancelCapture/stopAVMonitor/stopNetIOMonitor) can take several real
// seconds under load -- draining a NetIO session with sustained network
// traffic measured ~6s. Running that inline on the main/UI goroutine (as
// this used to) blocks the message pump for that whole time, which is
// exactly why Windows would pop its own "Not Responding" dialog even though
// nothing was actually deadlocked. So the slow teardown calls run on a
// background goroutine instead, with a progress dialog shown for the
// duration so it visibly reads as "closing", not hung -- the fyne.Do at the
// end is the one required to touch Fyne objects again from that goroutine
// (CLAUDE.md "fyne.Do is mandatory").
//
// The watchdog is still here for the separate, unresolved report of the
// process surviving quit for hours, not just seconds (see
// project_quit_hang_investigation memory) -- if the background teardown
// below is still running after 4s, far short of "hours," it dumps every
// goroutine's stack to a temp file so a real recurrence is finally
// diagnosable instead of guessed at a third time.
func quitApp(a fyne.App, win fyne.Window) {
	if !quitting.CompareAndSwap(false, true) {
		return
	}

	progress := dialog.NewCustomWithoutButtons("Closing "+appName, container.NewVBox(
		widget.NewLabel("Flushing monitoring/trace data, please wait…"),
		widget.NewProgressBarInfinite(),
	), win)
	progress.Show()

	watchdogDone := make(chan struct{})
	go func() {
		select {
		case <-watchdogDone:
		case <-time.After(4 * time.Second):
			buf := make([]byte, 4<<20)
			n := runtime.Stack(buf, true)
			path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-quit-hang-%d.txt", appID, time.Now().Unix()))
			_ = os.WriteFile(path, buf[:n], 0o644)
		}
	}()

	go func() {
		cancelCapture()
		stopAVMonitor()
		stopNetIOMonitor()
		if procSampler != nil {
			procSampler.Stop()
		}
		close(watchdogDone)

		fyne.Do(func() {
			progress.Hide()
			fynetooltip.DestroyWindowToolTipLayer(win.Canvas())
			saveMainWindowGeometry(a, win)
			if saveProcessLayout != nil {
				saveProcessLayout()
			}
			if saveWatchList != nil {
				saveWatchList()
			}
			a.Quit()
		})
	}()
}

// ── Menu + tray (mirror each other; see CLAUDE.md "System tray + main menu") ──

func buildMenu(a fyne.App, win fyne.Window, refreshNow, endSelected func(), showResourceDetail func(resourceKind), showInterference func()) *fyne.MainMenu {
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("Quit", func() { fyne.Do(func() { quitApp(a, win) }) }),
	)
	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("Hide All (Alt+H)", func() { hideAllWindows(win) }),
		fyne.NewMenuItem("Show All", func() { showAllWindows(win) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(a) }),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(a) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(a) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Resource Details", func() { showResourceDetail(resCPU) }),
		fyne.NewMenuItem("System Info", func() { showSystemInfo(a) }),
		fyne.NewMenuItem("Watch for Interference", showInterference),
		fyne.NewMenuItem("Capture Trace", func() { showCaptureWindow(a) }),
	)
	processMenu := fyne.NewMenu("Process",
		fyne.NewMenuItem("Refresh Now", refreshNow),
		fyne.NewMenuItem("End Process", endSelected),
		fyne.NewMenuItemSeparator(),
	)
	helpMenu := fyne.NewMenu("Help",
		fyne.NewMenuItem("Help", func() { showHelp(a) }),
		fyne.NewMenuItem("Check for Updates", func() { checkForUpdatesManual(a) }),
		fyne.NewMenuItem("About", func() { showAbout(a) }),
	)
	return fyne.NewMainMenu(fileMenu, viewMenu, processMenu, helpMenu)
}

// setupSystemTray mirrors the main menu. Tray callbacks fire off the main
// goroutine, so every body is wrapped in fyne.Do (CLAUDE.md "fyne.Do is
// mandatory").
func setupSystemTray(a fyne.App, win fyne.Window, refreshNow, endSelected func(), showResourceDetail func(resourceKind), showInterference func()) {
	desk, ok := a.(desktop.App)
	if !ok {
		return // not a desktop driver
	}
	menu := fyne.NewMenu(appName,
		fyne.NewMenuItem("Show All", func() { fyne.Do(func() { showAllWindows(win) }) }),
		fyne.NewMenuItem("Hide All", func() { fyne.Do(func() { hideAllWindows(win) }) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Refresh Now", func() { fyne.Do(refreshNow) }),
		fyne.NewMenuItem("End Process", func() { fyne.Do(endSelected) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Light Theme", func() { fyne.Do(func() { setLightTheme(a) }) }),
		fyne.NewMenuItem("Dark Theme", func() { fyne.Do(func() { setDarkTheme(a) }) }),
		fyne.NewMenuItem("System Theme", func() { fyne.Do(func() { setSystemTheme(a) }) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Resource Details", func() { fyne.Do(func() { showResourceDetail(resCPU) }) }),
		fyne.NewMenuItem("System Info", func() { fyne.Do(func() { showSystemInfo(a) }) }),
		fyne.NewMenuItem("Watch for Interference", func() { fyne.Do(showInterference) }),
		fyne.NewMenuItem("Capture Trace", func() { fyne.Do(func() { showCaptureWindow(a) }) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Help", func() { fyne.Do(func() { showHelp(a) }) }),
		fyne.NewMenuItem("Check for Updates", func() { checkForUpdatesManual(a) }),
		fyne.NewMenuItem("About", func() { fyne.Do(func() { showAbout(a) }) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { fyne.Do(func() { quitApp(a, win) }) }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(resourceKrankyBearProcessMinerPng)

	// Fyne's own desktop.App interface has no tooltip setter (checked its
	// definition directly: just SetSystemTrayMenu/SetSystemTrayIcon/
	// SetSystemTrayWindow) -- but fyne.io/systray, the library Fyne's own
	// desktop driver already uses internally for the tray icon, does expose
	// one (Windows and macOS; a documented no-op on Linux), and it was
	// already an indirect dependency via Fyne itself -- promoted to direct
	// here, the same way purego was promoted for handles_darwin.go in 0.6.0.
	// Uses appName, not a hardcoded string, so an elevated instance's tray
	// tooltip says "Administrator: ..." too, matching every window's title
	// bar under the same convention.
	//
	// Timing note: Fyne's own SetSystemTrayIcon call above works no matter
	// when it's called because Fyne caches the icon and re-applies it once
	// the tray is actually ready (see its internal onReady callback) --
	// there's no equivalent caching for a raw systray.SetTooltip call made
	// directly from application code like this, so it's deferred slightly
	// rather than called inline, to give the tray time to actually exist
	// first. Not yet verified against real hardware whether this delay is
	// reliably long enough -- flagged explicitly rather than assumed, the
	// same standard held elsewhere for anything that can't be tested from
	// this Mac.
	time.AfterFunc(500*time.Millisecond, func() {
		systray.SetTooltip(appName)
	})
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
