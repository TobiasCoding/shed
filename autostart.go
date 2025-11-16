package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Constantes para los nombres de las entradas de autostart en cada SO.
const (
	winRunKeyPath        = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	winRunValueName      = "Shed"
	linuxDesktopFileName = "shed.desktop"
)

// installAutostart registra Shed para que se inicie con el sistema.
// - En Windows crea/actualiza una entrada en el Run key del usuario.
// - En Linux crea un .desktop en ~/.config/autostart.
// - En otros SO no hace nada y devuelve nil.
func installAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "windows":
		// Usamos la herramienta "reg" para escribir en:
		// HKCU\Software\Microsoft\Windows\CurrentVersion\Run
		// Esto hace que Windows muestre shed como programa estándar de inicio.
		cmd := exec.Command(
			"reg", "add", winRunKeyPath,
			"/v", winRunValueName,
			"/t", "REG_SZ",
			"/d", exe,
			"/f",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("could not add autostart registry value: %w (%s)", err, string(out))
		}
		return nil

	case "linux":
		// Creamos ~/.config/autostart/shed.desktop
		confDir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(confDir, "autostart")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}

		desktopPath := filepath.Join(dir, linuxDesktopFileName)
		content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Shed
Exec=%s
X-GNOME-Autostart-enabled=true
`, exe)

		if err := os.WriteFile(desktopPath, []byte(content), 0o644); err != nil {
			return err
		}
		return nil
	default:
		// Otros sistemas: no se implementa autostart.
		return nil
	}
}

// uninstallAutostart elimina cualquier registro de autostart creado por
// installAutostart(). Si la entrada no existe, devuelve nil igualmente.
func uninstallAutostart() error {
	switch runtime.GOOS {
	case "windows":
		// Eliminamos la entrada del Run key del usuario.
		cmd := exec.Command(
			"reg", "delete", winRunKeyPath,
			"/v", winRunValueName,
			"/f",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Si el valor no existe, "reg" devuelve error con este texto.
			lower := strings.ToLower(string(out))
			if strings.Contains(lower, "unable to find") ||
				strings.Contains(lower, "the system was unable to find") {
				return nil
			}
			return fmt.Errorf("could not delete autostart registry value: %w (%s)", err, string(out))
		}
		return nil

	case "linux":
		// Borramos ~/.config/autostart/shed.desktop si existe.
		confDir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		desktopPath := filepath.Join(confDir, "autostart", linuxDesktopFileName)
		if err := os.Remove(desktopPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	default:
		return nil
	}
}
