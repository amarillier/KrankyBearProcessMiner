package main

import (
	"flag"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"

	"processminer/internal/startup"
)

const (
	// appName    = "KrankyBear ProcessMiner"
	appVersion = "0.1.0" // see FyneApp.toml
	appAuthor  = "Allan Marillier"
	appID      = "com.github.amarillier.KrankyBearProcessMiner"
)

var appName = "KrankyBear ProcessMiner"
var appCopyright = buildCopyrightNotice()

// procSampler mirrors the package-level aboutWindow/helpWindow pattern: a
// single long-lived background worker that must be stopped before quit (see
// quitApp and CLAUDE.md's teardown-order rule).
var procSampler *Sampler

// saveProcessLayout persists the process view's split-divider positions; set
// once newProcessView runs, called from quitApp alongside saveMainWindowGeometry.
var saveProcessLayout func()

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

	win := a.NewWindow(appName)
	win.SetIcon(resourceKrankyBearProcessMinerPng)
	win.Resize(mainWindowLaunchSize(a)) // restore previous size (size only - Fyne can't restore position)

	graphsView, updateGraphs := newSystemGraphsView()
	procSampler = NewSampler()
	processView, applyProcessSnapshot, refreshNow, endSelected, saveLayout := newProcessView(a, win, procSampler)
	saveProcessLayout = saveLayout

	procSampler.OnSystemSnapshot(func(s SystemSnapshot) { fyne.Do(func() { updateGraphs(s) }) })
	procSampler.OnProcessSnapshot(func(s ProcessSnapshot) { fyne.Do(func() { applyProcessSnapshot(s) }) })

	win.SetContent(container.NewBorder(graphsView, nil, nil, nil, processView))
	win.SetMainMenu(buildMenu(a, win, refreshNow, endSelected))
	setupSystemTray(a, win, refreshNow, endSelected)

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

// quitApp does teardown in the order CLAUDE.md calls out: stop background work
// first, then persist geometry, then quit.
func quitApp(a fyne.App, win fyne.Window) {
	if procSampler != nil {
		procSampler.Stop()
	}
	saveMainWindowGeometry(a, win)
	if saveProcessLayout != nil {
		saveProcessLayout()
	}
	a.Quit()
}

// ── Menu + tray (mirror each other; see CLAUDE.md "System tray + main menu") ──

func buildMenu(a fyne.App, win fyne.Window, refreshNow, endSelected func()) *fyne.MainMenu {
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("Quit", func() { fyne.Do(func() { quitApp(a, win) }) }),
	)
	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("Light Theme", func() { setLightTheme(a) }),
		fyne.NewMenuItem("Dark Theme", func() { setDarkTheme(a) }),
		fyne.NewMenuItem("System Theme", func() { setSystemTheme(a) }),
	)
	processMenu := fyne.NewMenu("Process",
		fyne.NewMenuItem("Refresh Now", refreshNow),
		fyne.NewMenuItem("End Process", endSelected),
	)
	windowMenu := fyne.NewMenu("Window",
		fyne.NewMenuItem("Hide All (Alt+H)", func() { hideAllWindows(win) }),
		fyne.NewMenuItem("Show All", func() { showAllWindows(win) }),
	)
	helpMenu := fyne.NewMenu("Help",
		fyne.NewMenuItem("Help", func() { showHelp(a) }),
		fyne.NewMenuItem("Check for Updates", func() { checkForUpdatesManual(a) }),
		fyne.NewMenuItem("About", func() { showAbout(a) }),
	)
	return fyne.NewMainMenu(fileMenu, viewMenu, processMenu, windowMenu, helpMenu)
}

// setupSystemTray mirrors the main menu. Tray callbacks fire off the main
// goroutine, so every body is wrapped in fyne.Do (CLAUDE.md "fyne.Do is
// mandatory").
func setupSystemTray(a fyne.App, win fyne.Window, refreshNow, endSelected func()) {
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
		fyne.NewMenuItem("Help", func() { fyne.Do(func() { showHelp(a) }) }),
		fyne.NewMenuItem("Check for Updates", func() { checkForUpdatesManual(a) }),
		fyne.NewMenuItem("About", func() { fyne.Do(func() { showAbout(a) }) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { fyne.Do(func() { quitApp(a, win) }) }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(resourceKrankyBearProcessMinerPng)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
