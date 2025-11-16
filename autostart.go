package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
)

// installAutostart installs an autostart entry for shed_1.0. On Windows
// it writes a small batch file into the user's Startup folder which
// launches the current executable. On Linux it writes a .desktop file
// into the user's autostart directory. On other platforms the function
// returns nil without doing anything. If the autostart files cannot be
// written an error is returned. The user's shell environment is not
// modified.
func installAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("AppData")
		if appData == "" {
			return errors.New("AppData not set")
		}
		dir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
		_ = os.MkdirAll(dir, 0755)
		return ioutil.WriteFile(filepath.Join(dir, "shed_1_0_start.bat"),
			[]byte(fmt.Sprintf("@echo off\r\nstart \"\" \"%s\"\r\n", exe)), 0644)
	case "linux":
		conf, _ := os.UserConfigDir()
		dir := filepath.Join(conf, "autostart")
		_ = os.MkdirAll(dir, 0755)
		return ioutil.WriteFile(filepath.Join(dir, "shed_1_0.desktop"),
			[]byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=shed_1.0\nExec=%s\nX-GNOME-Autostart-enabled=true\n", exe)), 0644)
	}
	return nil
}
