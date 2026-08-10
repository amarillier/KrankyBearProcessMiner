package main

import "fyne.io/fyne/v2"

// hideAllWindows hides the main window and every currently-open secondary
// window together (CLAUDE.md "Hide all / show all windows"). It deliberately
// does not clear each window's open flag (aboutOpen/helpOpen/updateOpen) --
// those mean "should reappear on show-all", not "currently visible" -- so
// showAllWindows restores exactly the set that was open, the same way a
// user-initiated close (via each window's SetCloseIntercept) does clear the
// flag so a closed window stays closed.
func hideAllWindows(win fyne.Window) {
	win.Hide()
	if aboutWindow != nil && aboutOpen {
		aboutWindow.Hide()
	}
	if helpWindow != nil && helpOpen {
		helpWindow.Hide()
	}
	if updateWindow != nil && updateOpen {
		updateWindow.Hide()
	}
	if sysInfoWindow != nil && sysInfoOpen {
		sysInfoWindow.Hide()
	}
	if resourceDetailWindow != nil && resourceDetailOpen {
		resourceDetailWindow.Hide()
	}
	if interferenceWindow != nil && interferenceOpen {
		interferenceWindow.Hide()
	}
	if childDrilldownWindow != nil && childDrilldownOpen {
		childDrilldownWindow.Hide()
	}
	if threadsWindow != nil && threadsOpen {
		threadsWindow.Hide()
	}
	if captureWindow != nil && captureOpen {
		captureWindow.Hide()
	}
}

// showAllWindows reveals the main window plus whichever secondary windows
// were open before the last hideAllWindows.
func showAllWindows(win fyne.Window) {
	win.Show()
	win.RequestFocus()
	if aboutWindow != nil && aboutOpen {
		aboutWindow.Show()
	}
	if helpWindow != nil && helpOpen {
		helpWindow.Show()
	}
	if updateWindow != nil && updateOpen {
		updateWindow.Show()
	}
	if sysInfoWindow != nil && sysInfoOpen {
		sysInfoWindow.Show()
	}
	if resourceDetailWindow != nil && resourceDetailOpen {
		resourceDetailWindow.Show()
	}
	if interferenceWindow != nil && interferenceOpen {
		interferenceWindow.Show()
	}
	if childDrilldownWindow != nil && childDrilldownOpen {
		childDrilldownWindow.Show()
	}
	if threadsWindow != nil && threadsOpen {
		threadsWindow.Show()
	}
	if captureWindow != nil && captureOpen {
		captureWindow.Show()
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
