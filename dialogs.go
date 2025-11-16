package main

import (
    "fyne.io/fyne/v2"
    "fyne.io/fyne/v2/dialog"
)

// openFolderDialog presents a folder selection dialog to the user. A new
// temporary window is created as the dialog parent so that the dialog
// appears centred and modal. When the user selects a folder the onSelect
// callback is invoked with the selected path. If the dialog is cancelled or
// an error occurs the callback is not invoked. The window is closed
// automatically after the dialog completes.
func openFolderDialog(title string, onSelect func(path string)) {
    win := fyne.CurrentApp().NewWindow(title)
    dlg := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
        if err != nil {
            dialog.ShowError(err, win)
            win.Close()
            return
        }
        if uri != nil {
            onSelect(uri.Path())
        }
        win.Close()
    }, win)
    win.Resize(fyne.NewSize(900, 600))
    dlg.Show()
    win.Show()
}

// openFileDialog presents a file selection dialog to the user. A new
// temporary window is created as the dialog parent. When the user selects
// a file the onSelect callback is invoked with the file path. If the
// dialog is cancelled or an error occurs the callback is not invoked. The
// window is closed automatically after the dialog completes.
func openFileDialog(title string, onSelect func(path string)) {
    win := fyne.CurrentApp().NewWindow(title)
    dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
        if err != nil {
            dialog.ShowError(err, win)
            win.Close()
            return
        }
        if r != nil {
            onSelect(r.URI().Path())
            _ = r.Close()
        }
        win.Close()
    }, win)
    win.Resize(fyne.NewSize(900, 600))
    dlg.Show()
    win.Show()
}