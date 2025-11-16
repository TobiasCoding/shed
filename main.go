package main

// shed_1.0 – GUI para backups incrementales con restic (Windows/Linux)
// Autor: tobiasrimoli@protonmail.com

import (
	_ "embed"
	"flag"
	"io"
	"log"
	"os"

	"fyne.io/fyne/v2/app"
)

// iconData embeds the application icon. It is referenced by the UI and
// initialised via the go:embed directive below. The file assets/icon.png
// must exist in the project tree at build time.
//
//go:embed assets/icon.png
var iconData []byte

// main is the entry point of the application. It parses command-line flags,
// loads the configuration, detects the restic binary, starts any live
// backup goroutines and launches the user interface. When the --logs flag
// is provided verbose logging is written to stdout; otherwise it is
// discarded. Errors encountered during startup are logged but do not
// prevent the UI from running.
func main() {
	// parse command line flags
	logs := flag.Bool("logs", false, "enable verbose logging")
	flag.Parse()
	if *logs {
		enableLogs = true
		// direct log output to stdout
		log.SetOutput(os.Stdout)
	} else {
		// discard log output when logs flag not set
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

	// initialise and run Fyne application
	myApp := app.NewWithID("shed_1_0")
	win := buildUI(myApp)
	win.ShowAndRun()
}
