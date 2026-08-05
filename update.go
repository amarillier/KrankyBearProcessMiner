// Package main provides update checking dialog
// Note: About and Help dialogs have been moved to separate files:
//   - about.go: About dialog (reusable)
//   - help.go: Help dialog (reusable)
//   - dialogs.go: Update checker dialog (this file)
package main

import (
	"net/url"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var updateWindow fyne.Window

// updateOpen tracks whether the Update window should be considered "open"
// for hide-all/show-all purposes (windows.go) -- see aboutOpen in about.go.
var updateOpen bool

// aheadOfLatestRelease caches the most recent update check's verdict on
// whether this build is newer than the latest published GitHub release (an
// unpublished/dev build) -- set by checkForUpdatesManual/Auto below, and read
// by about.go's showAbout so the About window's HardHat badge reflects the
// last known state without running its own network check.
var aheadOfLatestRelease atomic.Bool

// Repo checked by "Check for Updates". rename-app.sh does not rewrite these —
// point them at your project's actual GitHub repo when you start a new app.
// The repo must be public with a published Release (a bare git tag is not
// enough); otherwise the checker silently reports "you are running the
// latest version" (see CLAUDE.md's Update checker notes).
const (
	updateRepoOwner = "amarillier"
	updateRepo      = "KrankyBearProcessMiner"
	updateRepoDL    = "https://github.com/amarillier/KrankyBearProcessMiner/releases/latest"
)

// checkForUpdatesAuto runs a quiet, throttled (once/day) check on launch and
// only pops a dialog when an update is actually available.
func checkForUpdatesAuto(a fyne.App) {
	go func() {
		msg, available, remoteTag := updateChecker(updateRepoOwner, updateRepo, appName, updateRepoDL, 1)
		ahead := versionIsNewer(appVersion, remoteTag)
		aheadOfLatestRelease.Store(ahead)
		if !available {
			return
		}
		fyne.Do(func() { showUpdateDialog(a, msg, available, ahead) })
	}()
}

// checkForUpdatesManual runs an unthrottled check and always shows the
// result, for the Help menu / tray "Check for Updates" action.
func checkForUpdatesManual(a fyne.App) {
	go func() {
		msg, available, remoteTag := updateChecker(updateRepoOwner, updateRepo, appName, updateRepoDL, 0)
		ahead := versionIsNewer(appVersion, remoteTag)
		aheadOfLatestRelease.Store(ahead)
		fyne.Do(func() { showUpdateDialog(a, msg, available, ahead) })
	}()
}

// showUpdateDialog shows the update-check result. When ahead is true (this
// build is newer than the latest published release -- an unpublished/dev
// build), a small HardHat badge appears beside the normal app icon rather
// than replacing it, so the dialog still reads as this app with a highlight,
// not a different app.
func showUpdateDialog(a fyne.App, message string, updateAvailable bool, ahead bool) {
	if updateWindow != nil && updateWindow.Content().Visible() {
		updateWindow.Show()
		updateWindow.RequestFocus()
		return
	}

	updateWindow = a.NewWindow(appName + " - Update Check")
	updateWindow.SetIcon(resourceKrankyBearProcessMinerPng)

	// ImageFillContain via newBrandingDialogImage keeps this at
	// brandingImageSizeDialog regardless of the source PNG's native
	// resolution (ImageFillOriginal renders at native size -- 1024x1024 for
	// this app's icon -- which blows the window up; see CLAUDE.md).
	icon := newBrandingDialogImage(resourceKrankyBearProcessMinerPng)

	var iconDisplay fyne.CanvasObject = icon
	if ahead {
		badge := newBrandingBadgeImage(resourceKrankyBearHardHatPng)
		iconDisplay = container.NewHBox(icon, badge)
	}

	messageLabel := widget.NewLabel(message)
	messageLabel.Wrapping = fyne.TextWrapWord
	messageLabel.Alignment = fyne.TextAlignCenter

	var content *fyne.Container
	if updateAvailable {
		releaseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearProcessMiner/releases/latest")
		releaseLink := widget.NewHyperlink("Download Latest Release", releaseURL)
		releaseLink.Alignment = fyne.TextAlignCenter

		notesURL, _ := url.Parse("https://github.com/amarillier/KrankyBearProcessMiner/blob/main/ReleaseNotes.txt")
		notesLink := widget.NewHyperlink("View Release Notes", notesURL)
		notesLink.Alignment = fyne.TextAlignCenter

		content = container.NewVBox(
			container.NewCenter(iconDisplay),
			widget.NewSeparator(),
			messageLabel,
			widget.NewSeparator(),
			container.NewCenter(releaseLink),
			container.NewCenter(notesLink),
		)
	} else {
		content = container.NewVBox(
			container.NewCenter(iconDisplay),
			widget.NewSeparator(),
			messageLabel,
		)
	}

	updateWindow.SetContent(container.NewPadded(content))
	updateWindow.Resize(fyne.NewSize(480, 420))

	updateWindow.SetCloseIntercept(func() {
		updateOpen = false
		updateWindow.Hide()
	})

	updateOpen = true
	updateWindow.Show()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
