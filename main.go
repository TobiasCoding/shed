//go:generate windres app.rc -O coff -o app.syso
package main

// shed – GUI para backups incrementales con restic (Windows/Linux)
// Autor: tobiasrimoli@protonmail.com

import (
	_ "embed"
	"flag"
	"fmt"
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
// detecta/prepara restic, inicia los backups en vivo y levanta la UI.
func main() {
	// parse command line flags
	logs := flag.Bool("logs", false, "enable verbose logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *logs {
		enableLogs = true
		log.SetOutput(os.Stdout)
	} else {
		log.SetOutput(io.Discard)
	}

	// Si se invoca sólo para mostrar versión, no hacemos más nada (no UI, no restic).
	if *showVersion {
		fmt.Printf("Shed %s\n", appVersion)
		return
	}

	// load configuration from disk (creates cfg and cfgFile)
	cfg = loadConfig()

	// Actualizar versión en config si cambió.
	initAppVersion()

	// Aseguramos que haya un binario de restic disponible:
	// - si restic_path está definido y existe, lo usamos;
	// - si no, lo descargamos/verificamos/descomprimimos en el dir de config.
	resticPath, err := ensureResticBinary()
	if err != nil {
		log.Fatalf("could not prepare restic binary: %v", err)
	}
	cfg.ResticPath = resticPath

	// start live backups if any job has Live enabled
	startLiveBackups(resticPath)

	// Chequeo silencioso de updates en segundo plano (no bloquea la UI).
	go CheckForUpdatesSilent()

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
