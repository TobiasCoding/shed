package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

const appVersion = "1.0.0"

// Rutas de actualización (lado servidor).
const (
	updateBaseURL          = "https://raw.githubusercontent.com/TobiasCoding/shed/refs/heads/main/resources/shed/updates"
	updateManifestURL      = updateBaseURL + "/manifest.json"
	updateSHA256SUMSURL    = updateBaseURL + "/SHA256SUMS"
	updateBinariesBaseURL  = updateBaseURL + "/binaries"
	updateHTTPTimeout      = 30 * time.Second
	updateDownloadTimeout  = 120 * time.Second
	updateClientUserAgent  = "ShedUpdater/1.0"
	updatePlatformKeySep   = "_"
	updateCurrentExeBackup = ".old"
)

// Estructura esperada del archivo manifest.json:
//
//	{
//	  "latest_version": "1.2.0",
//	  "binaries": {
//	    "windows_amd64": { "file": "shed_1.2.0_windows_amd64.exe" },
//	    "linux_amd64":   { "file": "shed_1.2.0_linux_amd64" }
//	  }
//	}
//
// El nombre de archivo se busca en SHA256SUMS para verificar el binario.
type updateManifest struct {
	LatestVersion string                          `json:"latest_version"`
	Binaries      map[string]updateManifestBinary `json:"binaries"`
}

type updateManifestBinary struct {
	File string `json:"file"`
}

// UpdateInfo representa el resultado de chequear actualizaciones.
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	UpToDate       bool
	PlatformKey    string
	BinaryFile     string // nombre de archivo dentro de /binaries
}

// currentPlatformKey genera la clave tipo "windows_amd64" o "linux_amd64".
func currentPlatformKey() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return runtime.GOOS + updatePlatformKeySep + runtime.GOARCH
}

// initAppVersion se asegura de que cfg.Version refleje appVersion.
func initAppVersion() {
	if cfg == nil {
		return
	}
	if cfg.Version != appVersion {
		cfg.Version = appVersion
		_ = saveConfig(cfg)
	}
}

// CheckForUpdatesSilent se llama al iniciar el programa. Solo consulta el
// manifest y deja registrado en config si hay update o no. No descarga ni
// reemplaza binarios.
func CheckForUpdatesSilent() {
	info, err := checkForUpdatesCore()
	cfg.LastUpdateCheck = time.Now()
	if err != nil {
		cfg.LastUpdateStatus = "Error: " + err.Error()
	} else if info.UpToDate {
		cfg.LastUpdateStatus = fmt.Sprintf("Up to date (v%s)", info.CurrentVersion)
	} else {
		cfg.LastUpdateStatus = fmt.Sprintf("Update available: v%s", info.LatestVersion)
	}
	_ = saveConfig(cfg)
	if enableLogs {
		logPrintf("[update] silent check: %s", cfg.LastUpdateStatus)
	}
}

// CheckForUpdatesDialog se usa desde la UI de Settings. Muestra diálogos
// indicando si hay update y, si el usuario acepta, descarga e intenta
// reemplazar el binario actual.
func CheckForUpdatesDialog(parent fyne.Window) {
	prog := dialog.NewProgressInfinite("Checking for updates", "Contacting update server…", parent)
	prog.Show()

	go func() {
		defer prog.Hide()

		info, err := checkForUpdatesCore()
		cfg.LastUpdateCheck = time.Now()

		if err != nil {
			cfg.LastUpdateStatus = "Error: " + err.Error()
			_ = saveConfig(cfg)
			dialog.ShowError(err, parent)
			return
		}

		if info.UpToDate {
			cfg.LastUpdateStatus = fmt.Sprintf("Up to date (v%s)", info.CurrentVersion)
			_ = saveConfig(cfg)
			dialog.ShowInformation("Updates", "You are already running the latest version.", parent)
			return
		}

		cfg.LastUpdateStatus = fmt.Sprintf("Update available: v%s", info.LatestVersion)
		_ = saveConfig(cfg)

		// Preguntar si descargamos e instalamos.
		msg := fmt.Sprintf("A new version is available.\n\nCurrent: %s\nLatest:  %s\n\nDownload and install now?",
			info.CurrentVersion, info.LatestVersion)

		dialog.ShowConfirm("Update available", msg, func(ok bool) {
			if !ok {
				return
			}
			doDownloadAndInstallUpdate(parent, info)
		}, parent)
	}()
}

// checkForUpdatesCore hace el trabajo de red para obtener manifest.json y
// determinar si hay update para esta plataforma.
func checkForUpdatesCore() (*UpdateInfo, error) {
	client := &http.Client{
		Timeout: updateHTTPTimeout,
	}

	req, err := http.NewRequest("GET", updateManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", updateClientUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest returned status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var mf updateManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}

	if strings.TrimSpace(mf.LatestVersion) == "" {
		return nil, fmt.Errorf("manifest missing latest_version")
	}

	plat := currentPlatformKey()
	bin, ok := mf.Binaries[plat]
	if !ok || strings.TrimSpace(bin.File) == "" {
		return nil, fmt.Errorf("no binary entry in manifest for platform %s", plat)
	}

	current := appVersion
	upToDate := !versionLess(current, mf.LatestVersion)

	return &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  mf.LatestVersion,
		UpToDate:       upToDate,
		PlatformKey:    plat,
		BinaryFile:     bin.File,
	}, nil
}

// versionLess hace una comparación simple de versiones "a.b.c" numéricas.
// Devuelve true si a < b.
func versionLess(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(as) {
			fmt.Sscanf(as[i], "%d", &ai)
		}
		if i < len(bs) {
			fmt.Sscanf(bs[i], "%d", &bi)
		}
		if ai < bi {
			return true
		}
		if ai > bi {
			return false
		}
	}
	return false
}

// doDownloadAndInstallUpdate descarga el binario, verifica SHA256 y trata
// de reemplazar el ejecutable actual.
func doDownloadAndInstallUpdate(parent fyne.Window, info *UpdateInfo) {
	prog := dialog.NewProgressInfinite("Downloading update", "Please wait…", parent)
	prog.Show()

	go func() {
		defer prog.Hide()

		exePath, err := os.Executable()
		if err != nil {
			dialog.ShowError(fmt.Errorf("cannot determine current executable: %w", err), parent)
			return
		}
		exePath, _ = filepath.Abs(exePath)
		exeDir := filepath.Dir(exePath)

		tmpPath := filepath.Join(exeDir, info.BinaryFile+".tmp")
		finalPath := filepath.Join(exeDir, info.BinaryFile)

		// 1) Descargar binario nuevo en tmpPath.
		if enableLogs {
			logPrintf("[update] downloading %s to %s", info.BinaryFile, tmpPath)
		}
		if err := downloadUpdateBinary(info.BinaryFile, tmpPath); err != nil {
			dialog.ShowError(err, parent)
			return
		}

		// 2) Verificar SHA256 contra SHA256SUMS.
		if enableLogs {
			logPrintf("[update] verifying SHA256 for %s", info.BinaryFile)
		}
		if err := verifyUpdateSHA256(info.BinaryFile, tmpPath); err != nil {
			_ = os.Remove(tmpPath)
			dialog.ShowError(err, parent)
			return
		}

		// 3) Mover tmpPath a finalPath (nombre de archivo que define el manifest).
		if err := os.Rename(tmpPath, finalPath); err != nil {
			_ = os.Remove(tmpPath)
			dialog.ShowError(fmt.Errorf("could not move new binary into place: %w", err), parent)
			return
		}

		// 4) Intentar reemplazar el ejecutable actual con el nuevo.
		backupPath := exePath + updateCurrentExeBackup

		// Best-effort backup.
		_ = os.Remove(backupPath)
		_ = os.Rename(exePath, backupPath)

		if err := os.Rename(finalPath, exePath); err != nil {
			// No pudimos reemplazar en caliente (Windows puede bloquear).
			// Dejamos el nuevo binario en finalPath y avisamos al usuario.
			msg := fmt.Sprintf(
				"New version downloaded and verified at:\n\n%s\n\n"+
					"Could not replace the running executable automatically.\n"+
					"Close Shed and replace the file manually with this new binary.",
				finalPath,
			)
			dialog.ShowInformation("Update downloaded", msg, parent)
			return
		}

		// Actualizamos versión en config.
		cfg.Version = info.LatestVersion
		cfg.LastUpdateCheck = time.Now()
		cfg.LastUpdateStatus = fmt.Sprintf("Updated to v%s", info.LatestVersion)
		_ = saveConfig(cfg)

		dialog.ShowInformation("Update installed",
			"Update installed successfully.\nPlease restart Shed to use the new version.",
			parent)
	}()
}

// downloadUpdateBinary descarga el binario desde /binaries/<fileName>.
func downloadUpdateBinary(fileName, destPath string) error {
	client := &http.Client{
		Timeout: updateDownloadTimeout,
	}
	url := updateBinariesBaseURL + "/" + fileName

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateClientUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %s", resp.Status)
	}

	tmp := destPath + ".dl"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// verifyUpdateSHA256 verifica el hash de filePath usando SHA256SUMS.
func verifyUpdateSHA256(fileName, filePath string) error {
	sums, err := fetchUpdateSHA256Sums()
	if err != nil {
		return err
	}
	exp, ok := sums[fileName]
	if !ok {
		return fmt.Errorf("no SHA256 entry for %s in SHA256SUMS", fileName)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, exp) {
		return fmt.Errorf("SHA256 mismatch for %s: expected %s, got %s", fileName, exp, got)
	}
	return nil
}

// fetchUpdateSHA256Sums descarga y parsea SHA256SUMS en un map filename->hash.
func fetchUpdateSHA256Sums() (map[string]string, error) {
	client := &http.Client{
		Timeout: updateHTTPTimeout,
	}
	resp, err := client.Get(updateSHA256SUMSURL)
	if err != nil {
		return nil, fmt.Errorf("could not download SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SHA256SUMS returned status %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		name := parts[len(parts)-1]
		m[name] = hash
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("no entries parsed from SHA256SUMS")
	}
	return m, nil
}
