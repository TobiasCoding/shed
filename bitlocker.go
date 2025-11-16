package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// isDriveAccessible intenta leer el root de la unidad (por ejemplo "E:\").
// Si puede listar el contenido sin error, asumimos que la unidad está
// desbloqueada y accesible.
func isDriveAccessible(drive string) bool {
	path := drive
	if !strings.HasSuffix(path, `\`) && !strings.HasSuffix(path, `/`) {
		path += `\`
	}
	_, err := os.ReadDir(path)
	return err == nil
}

// escapeForSingleQuotes escapa comillas simples para usarlas dentro
// de una cadena entrecomillada con '...' en PowerShell.
func escapeForSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// unlockBitlocker intenta desbloquear un volumen BitLocker lanzando
// SIEMPRE un PowerShell ELEVADO (Start-Process -Verb RunAs).
// - Si la unidad ya está accesible, no hace nada.
// - Si después del comando la unidad es accesible, se considera éxito.
// - Si no, devuelve error.
func unlockBitlocker(sourcePath, password string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	if strings.TrimSpace(password) == "" {
		return errors.New("BitLocker password cannot be empty")
	}

	vol := filepath.VolumeName(sourcePath)
	if vol == "" {
		return errors.New("unable to determine volume for BitLocker unlock")
	}

	drive := strings.TrimSuffix(vol, ":") + ":"

	// Si la unidad ya es accesible, no hacemos nada.
	if isDriveAccessible(drive) {
		logPrintf("[bitlocker] drive %s already accessible, skipping unlock", drive)
		return nil
	}

	pwEsc := escapeForSingleQuotes(password)

	// Script interno que realmente hace el Unlock-BitLocker
	inner := fmt.Sprintf(
		"$pw = ConvertTo-SecureString '%s' -AsPlainText -Force; "+
			"$ErrorActionPreference = 'Stop'; "+
			"Unlock-BitLocker -MountPoint '%s' -Password $pw -ErrorAction Stop",
		pwEsc, drive,
	)

	// Para logs normales: sin la contraseña
	sanitInner := strings.ReplaceAll(inner, password, "******")
	logPrintf("[bitlocker] INNER (sanitized): %s", sanitInner)

	// ⚠️ SOLO PARA DEBUG PUNTUAL:
	// logPrintf("[bitlocker] INNER (PLAIN DEBUG): %s", inner)

	// Usamos comillas simples en $inner, escapando comillas simples internas
	innerForOuter := escapeForSingleQuotes(inner)

	script := fmt.Sprintf(`
$inner = '%s';
$p = Start-Process -FilePath 'powershell.exe' -ArgumentList '-NoProfile','-NonInteractive','-Command',$inner -Verb RunAs -Wait -PassThru;
exit $p.ExitCode;
`, innerForOuter)

	logPrintf("[bitlocker] OUTER: %s", script)
	logPrintf("[bitlocker] EXEC: powershell.exe -NoProfile -NonInteractive -Command \"%s\"", script)

	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command", script,
	)

	output, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(output))
	outStrSanitized := strings.ReplaceAll(outStr, password, "******")

	logPrintf("[bitlocker] OUTPUT (sanitized): %s", outStrSanitized)
	logPrintf("[bitlocker] ERR: %v", err)

	// Independientemente del exit status, si ahora la unidad es accesible,
	// lo consideramos éxito.
	if isDriveAccessible(drive) {
		logPrintf("[bitlocker] drive %s accessible after elevated unlock", drive)
		return nil
	}

	// Si sigue bloqueada, devolvemos error.
	if outStrSanitized != "" {
		return fmt.Errorf("BitLocker unlock failed for %s:\n%s", drive, outStrSanitized)
	}
	if err != nil {
		return fmt.Errorf("BitLocker unlock failed for %s: %v", drive, err)
	}
	return fmt.Errorf("BitLocker unlock failed for %s: drive is still locked or not accessible", drive)
}
