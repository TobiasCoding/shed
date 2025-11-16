//go:generate windres app.rc -O coff -o app.syso
package main

// shed – GUI para backups incrementales con restic (Windows/Linux)
// Autor: tobiasrimoli@protonmail.com

import (
	_ "embed"
	"flag"
	"io"
	"log"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
)

// iconData embeddeado para usar como icono de la aplicación y del tray.
//
//go:embed assets/icon.png
var iconData []byte

// main es el punto de entrada. Configura logs, carga configuración,
// detecta restic, inicia los backups en vivo y levanta la UI.
func main() {
	// parse command line flags
	logs := flag.Bool("logs", false, "enable verbose logging")
	flag.Parse()
	if *logs {
		enableLogs = true
		log.SetOutput(os.Stdout)
	} else {
		log.SetOutput(io.Discard)
	}

	// load configuration from disk (creates cfg and cfgFile)
	cfg = loadConfig()

	// auto-detect restic if not already configured
	if cfg.ResticPath == "" {
		cfg.ResticPath = detectRestic("")
		_ = saveConfig(cfg)
	}

	// start live backups if any job has Live enabled
	startLiveBackups(cfg.ResticPath)

	// initialise Fyne application
	myApp := app.NewWithID("Shed")

	// Icono de la app (también se usa por defecto para el tray).
	if len(iconData) > 0 {
		iconRes := fyne.NewStaticResource("icon.png", iconData)
		myApp.SetIcon(iconRes)
	}

	// Construimos la ventana principal.
	win := buildUI(myApp)

	// ---------------------------------------------------------------------
	// System tray: menú con Show / Exit y cierre por X que solo oculta.
	// ---------------------------------------------------------------------
	if desk, ok := myApp.(desktop.App); ok {
		// Menú del tray: Show y Exit.
		trayMenu := fyne.NewMenu("Shed",
			fyne.NewMenuItem("Show", func() {
				win.Show()
				win.RequestFocus()
			}),
		)
		desk.SetSystemTrayMenu(trayMenu)
	}

	// Al cerrar con la X, solo ocultamos la ventana (la app sigue en el tray).
	win.SetCloseIntercept(func() {
		win.Hide()
	})

	// run application event loop
	win.ShowAndRun()
}
