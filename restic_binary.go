package main

import (
	"archive/zip"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Versión de restic que estamos gestionando y origen de los artefactos.
const (
	resticVersion       = "0.18.1"
	resticBaseURL       = "https://raw.githubusercontent.com/TobiasCoding/shed/refs/heads/main/resources/restic"
	resticSHA256SUMSURL = resticBaseURL + "/SHA256SUMS"
)

// ensureResticBinary se asegura de que tengamos un binario de restic listo:
//
//  1. Si cfg.ResticPath está definido y el archivo existe, lo usa.
//  2. En caso contrario, descarga el artefacto adecuado (según GOOS/GOARCH)
//     desde resticBaseURL, verifica el SHA256, lo descomprime en el mismo
//     directorio que config.json y actualiza cfg.ResticPath.
//  3. Lanza "restic version" para validar el binario antes de usarlo.
func ensureResticBinary() (string, error) {
	// 1) Si en config hay ruta y existe, la usamos.
	if strings.TrimSpace(cfg.ResticPath) != "" {
		if _, err := os.Stat(cfg.ResticPath); err == nil {
			if enableLogs {
				logPrintf("using restic from config: %q", cfg.ResticPath)
			}
			return cfg.ResticPath, nil
		}
		if enableLogs {
			logPrintf("configured restic_path %q not found, will download a fresh copy", cfg.ResticPath)
		}
	}

	if cfgFile == "" {
		return "", errors.New("cfgFile is empty: cannot determine config directory to store restic")
	}
	configDir := filepath.Dir(cfgFile)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", fmt.Errorf("could not create config dir %q: %w", configDir, err)
	}

	artifactName, _, err := resticFilenamesForPlatform()
	if err != nil {
		return "", err
	}

	artifactPath := filepath.Join(configDir, artifactName)
	binaryPath := filepath.Join(configDir, strings.TrimSuffix(artifactName, filepath.Ext(artifactName)))
	if strings.HasSuffix(artifactName, ".zip") && runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	// Si ya tenemos el binario descomprimido, lo reutilizamos.
	if _, err := os.Stat(binaryPath); err == nil {
		if enableLogs {
			logPrintf("restic binary already present at %q", binaryPath)
		}
		if err := checkRestic(binaryPath); err != nil {
			if enableLogs {
				logPrintf("existing restic binary at %q failed self-check: %v; redownloading", binaryPath, err)
			}
		} else {
			cfg.ResticPath = binaryPath
			_ = saveConfig(cfg)
			return binaryPath, nil
		}
	}

	if enableLogs {
		logPrintf("downloading restic %s (%s) into %q", resticVersion, artifactName, artifactPath)
	}

	// Descarga artefacto comprimido.
	if err := downloadFile(resticBaseURL+"/"+artifactName, artifactPath); err != nil {
		return "", fmt.Errorf("failed to download restic artifact: %w", err)
	}

	if enableLogs {
		logPrintf("downloaded %s, verifying SHA256 integrity…", artifactName)
	}

	// Verificación de SHA256.
	if err := verifySHA256(artifactName, artifactPath); err != nil {
		_ = os.Remove(artifactPath)
		return "", err
	}

	if enableLogs {
		logPrintf("SHA256 verification OK for %s, decompressing…", artifactName)
	}

	// Descompresión según extensión.
	if strings.HasSuffix(artifactName, ".bz2") {
		if err := decompressBzip2ToFile(artifactPath, binaryPath); err != nil {
			_ = os.Remove(artifactPath)
			return "", fmt.Errorf("failed to decompress %s: %w", artifactName, err)
		}
	} else if strings.HasSuffix(artifactName, ".zip") {
		if err := decompressZipSingleBinary(artifactPath, binaryPath); err != nil {
			_ = os.Remove(artifactPath)
			return "", fmt.Errorf("failed to unzip %s: %w", artifactName, err)
		}
	} else {
		_ = os.Remove(artifactPath)
		return "", fmt.Errorf("unsupported artifact extension for %s", artifactName)
	}

	// Ya no necesitamos el archivo comprimido.
	_ = os.Remove(artifactPath)

	// Permisos de ejecución (en Windows se ignoran, pero no molesta).
	_ = os.Chmod(binaryPath, 0o755)

	// Verificación final: "restic version".
	if err := checkRestic(binaryPath); err != nil {
		return "", fmt.Errorf("downloaded restic did not pass self-check: %w", err)
	}

	cfg.ResticPath = binaryPath
	if err := saveConfig(cfg); err != nil && enableLogs {
		logPrintf("warning: could not save config after setting restic_path: %v", err)
	}

	if enableLogs {
		logPrintf("restic is ready at %q", binaryPath)
	}
	return binaryPath, nil
}

// resticFilenamesForPlatform devuelve el nombre del artefacto comprimido y el
// nombre del binario final (dentro del directorio de config) según GOOS/GOARCH.
func resticFilenamesForPlatform() (artifactName, binaryName string, err error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	// Nombre del binario final que vamos a usar en disco.
	binaryName = "restic"
	if goos == "windows" {
		binaryName = "restic.exe"
	}

	base := "restic_" + resticVersion + "_"

	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return base + "windows_amd64.zip", binaryName, nil
		case "386":
			return base + "windows_386.zip", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported Windows arch for restic: %s", arch)
		}
	case "linux":
		switch arch {
		case "amd64":
			return base + "linux_amd64.bz2", binaryName, nil
		case "386":
			return base + "linux_386.bz2", binaryName, nil
		case "arm":
			return base + "linux_arm.bz2", binaryName, nil
		case "arm64":
			return base + "linux_arm64.bz2", binaryName, nil
		case "mips":
			return base + "linux_mips.bz2", binaryName, nil
		case "mips64":
			return base + "linux_mips64.bz2", binaryName, nil
		case "mips64le":
			return base + "linux_mips64le.bz2", binaryName, nil
		case "mipsle":
			return base + "linux_mipsle.bz2", binaryName, nil
		case "ppc64le":
			return base + "linux_ppc64le.bz2", binaryName, nil
		case "riscv64":
			return base + "linux_riscv64.bz2", binaryName, nil
		case "s390x":
			return base + "linux_s390x.bz2", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported Linux arch for restic: %s", arch)
		}
	case "darwin":
		switch arch {
		case "amd64":
			return base + "darwin_amd64.bz2", binaryName, nil
		case "arm64":
			return base + "darwin_arm64.bz2", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported macOS arch for restic: %s", arch)
		}
	case "freebsd":
		switch arch {
		case "386":
			return base + "freebsd_386.bz2", binaryName, nil
		case "amd64":
			return base + "freebsd_amd64.bz2", binaryName, nil
		case "arm":
			return base + "freebsd_arm.bz2", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported FreeBSD arch for restic: %s", arch)
		}
	case "openbsd":
		switch arch {
		case "386":
			return base + "openbsd_386.bz2", binaryName, nil
		case "amd64":
			return base + "openbsd_amd64.bz2", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported OpenBSD arch for restic: %s", arch)
		}
	case "netbsd":
		switch arch {
		case "386":
			return base + "netbsd_386.bz2", binaryName, nil
		case "amd64":
			return base + "netbsd_amd64.bz2", binaryName, nil
		default:
			return "", "", fmt.Errorf("unsupported NetBSD arch for restic: %s", arch)
		}
	case "dragonfly":
		if arch == "amd64" {
			return base + "dragonfly_amd64.bz2", binaryName, nil
		}
		return "", "", fmt.Errorf("unsupported DragonFly arch for restic: %s", arch)
	case "solaris":
		if arch == "amd64" {
			return base + "solaris_amd64.bz2", binaryName, nil
		}
		return "", "", fmt.Errorf("unsupported Solaris arch for restic: %s", arch)
	case "aix":
		if arch == "ppc64" {
			return base + "aix_ppc64.bz2", binaryName, nil
		}
		return "", "", fmt.Errorf("unsupported AIX arch for restic: %s", arch)
	default:
		return "", "", fmt.Errorf("unsupported OS/arch for restic: %s/%s", goos, arch)
	}
}

// downloadFile descarga un archivo desde url a destPath.
func downloadFile(url, destPath string) error {
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("http GET %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http GET %s returned status %s", url, resp.Status)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Movimiento atómico al nombre final.
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

// verifySHA256 descarga SHA256SUMS y verifica que filePath tenga el hash
// esperado para artifactName.
func verifySHA256(artifactName, filePath string) error {
	sums, err := fetchSHA256Sums()
	if err != nil {
		return err
	}
	expected, ok := sums[artifactName]
	if !ok {
		return fmt.Errorf("no SHA256 entry for %s in SHA256SUMS", artifactName)
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
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA256 mismatch for %s: expected %s, got %s", artifactName, expected, actual)
	}
	return nil
}

// fetchSHA256Sums descarga y parsea el archivo SHA256SUMS en un mapa
// filename -> hexhash.
func fetchSHA256Sums() (map[string]string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Get(resticSHA256SUMSURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SHA256SUMS returned status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
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
		filename := parts[len(parts)-1]
		result[filename] = hash
	}
	if len(result) == 0 {
		return nil, errors.New("parsed no entries from SHA256SUMS")
	}
	return result, nil
}

// decompressBzip2ToFile descomprime un .bz2 (binario plano) en destPath.
func decompressBzip2ToFile(srcPath, destPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	r := bzip2.NewReader(in)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return nil
}

// decompressZipSingleBinary descomprime el primer archivo regular del ZIP en destPath.
// No respetamos el nombre interno; siempre escribimos en destPath.
func decompressZipSingleBinary(srcPath, destPath string) error {
	r, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer r.Close()

	var file *zip.File
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			file = f
			break
		}
	}
	if file == nil {
		return fmt.Errorf("no regular file found inside zip %s", srcPath)
	}

	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, rc); err != nil {
		return err
	}
	return nil
}

// detectRestic intenta localizar el ejecutable restic en el sistema.
// Se mantiene por compatibilidad, aunque ahora preferimos ensureResticBinary().
func detectRestic(path string) string {
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			if _, err := os.Stat(abs); err == nil && checkRestic(abs) == nil {
				if enableLogs {
					logPrintf("using user-provided restic path: %q", abs)
				}
				return abs
			}
		}
	}
	cands := []string{}
	if p, err := exec.LookPath("restic"); err == nil {
		cands = append(cands, p)
	}
	if p, err := exec.LookPath("restic.exe"); err == nil {
		cands = append(cands, p)
	}
	if runtime.GOOS == "windows" {
		pf := os.Getenv("ProgramFiles")
		if pf != "" {
			for _, pat := range []string{
				filepath.Join(pf, "restic", "restic*.exe"),
				filepath.Join(pf, "restic*.exe"),
			} {
				if m, _ := filepath.Glob(pat); len(m) > 0 {
					cands = append(cands, m...)
				}
			}
		}
	}
	for _, c := range cands {
		if checkRestic(c) == nil {
			if enableLogs {
				logPrintf("restic detected at %q", c)
			}
			return c
		}
	}
	return ""
}

// checkRestic ejecuta "restic version" en la ruta dada para verificar
// que el binario funciona.
func checkRestic(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "version").Run()
}
