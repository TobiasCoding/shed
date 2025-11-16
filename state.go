package main

import (
	"context"
	"log"
	"strings"
	"sync"

	"fyne.io/fyne/v2/widget"
)

// enableLogs controls whether verbose logging is printed. It is set via the
// command-line flag --logs and used by helper functions to decide whether
// diagnostic messages should be printed.
var enableLogs bool

// statusLabel is used by the UI to display status messages. It is initialised
// in buildUI and may be updated from background goroutines via setStatus.
var statusLabel *widget.Label

// statusMu protects concurrent updates to statusLabel.
var statusMu sync.Mutex

// Job list filtering
var (
	jobList          *widget.List
	jobSearch        *widget.Entry
	filterJobIdx     []int
	selectedJobIndex = -1
)

// Snapshot filtering and selection
// Snapshot filtering and selection
var (
	allSnapshots    []Snapshot
	filterSnapQuery string
	filterSnapshots []Snapshot
	snapTable       *widget.Table
	selectedSnapIdx = -1

	// Estado de UI: si estamos recalculando el Last check para un job,
	// mostramos un spinner en la columna correspondiente.
	lastCheckUpdatingJob string
	lastCheckUpdating    bool
	lastCheckUpdatingMu  sync.Mutex
)

// File diff filtering and selection
var (
	allFiles        []FileRow
	filterFileQuery string
	filterFiles     []FileRow
	filesTable      *widget.Table
)

// liveCancelMap holds cancellation functions for live backup goroutines
// keyed by the job index. It is used to stop existing schedules before
// launching new ones when the job definition changes.
var liveCancelMap = make(map[int]context.CancelFunc)

// -----------------------------------------------------------------------------
// Estado de contraseñas en memoria (ofuscadas)
// -----------------------------------------------------------------------------

// pwCache guarda contraseñas de repositorio/BitLocker ofuscadas en memoria
// mientras la app está abierta. NO se persiste a disco.
var (
	pwCacheMu sync.Mutex
	pwCache   = make(map[string]string) // job.Name -> obfuscated password
)

// clave simple para ofuscar; no es criptografía fuerte, sólo evita texto plano.
var pwKey = []byte("shed_temp_key")

func obfuscatePassword(plain string) string {
	if plain == "" {
		return ""
	}
	b := []byte(plain)
	for i := range b {
		b[i] ^= pwKey[i%len(pwKey)]
	}
	return string(b)
}

func deobfuscatePassword(enc string) string {
	if enc == "" {
		return ""
	}
	b := []byte(enc)
	for i := range b {
		b[i] ^= pwKey[i%len(pwKey)]
	}
	return string(b)
}

// setJobPassword guarda en memoria temporal la contraseña de un job.
func setJobPassword(jobName, plain string) {
	pwCacheMu.Lock()
	defer pwCacheMu.Unlock()
	if strings.TrimSpace(plain) == "" {
		delete(pwCache, jobName)
		return
	}
	pwCache[jobName] = obfuscatePassword(plain)
}

// getJobPassword recupera la contraseña de un job, si está cacheada.
func getJobPassword(jobName string) (string, bool) {
	pwCacheMu.Lock()
	defer pwCacheMu.Unlock()
	enc, ok := pwCache[jobName]
	if !ok {
		return "", false
	}
	return deobfuscatePassword(enc), true
}

// clearJobPassword borra explícitamente la contraseña cacheada de un job.
func clearJobPassword(jobName string) {
	pwCacheMu.Lock()
	defer pwCacheMu.Unlock()
	delete(pwCache, jobName)
}

// effectivePassword devuelve la contraseña “real” a usar con restic:
// 1) si el Job tiene RepoPassword, usa esa;
// 2) si no, intenta la cache en memoria;
// 3) si tampoco hay, devuelve "" (repo sin contraseña).
func effectivePassword(job Job) string {
	if strings.TrimSpace(job.RepoPassword) != "" {
		return job.RepoPassword
	}
	if pw, ok := getJobPassword(job.Name); ok && strings.TrimSpace(pw) != "" {
		return pw
	}
	return ""
}

// -----------------------------------------------------------------------------
// UI helpers
// -----------------------------------------------------------------------------

// notifyLastCheckChanged se llama cada vez que se persiste un nuevo "Last check"
// para un snapshot. Refresca la tabla de snapshots en el hilo principal para que
// la columna "Last check" se actualice sin tener que reiniciar la aplicación.
func notifyLastCheckChanged(job Job, snapshotID string) {
	if snapTable == nil {
		return
	}
	if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
		return
	}
	if cfg.Jobs[selectedJobIndex].Name != job.Name {
		return
	}
	// Ejecución desde la goroutine de fondo:
	// Usamos un canal o un simple go-call para pasar al hilo principal,
	// si es que Fyne lo requiere. Pero Fyne documenta que muchos métodos
	// son seguros para usar desde go-rutinas. :contentReference[oaicite:2]{index=2}
	go func() {
		snapTable.Refresh()
	}()
}

// logPrintf prints to the standard logger when enableLogs is true. It is used
// instead of direct log.Printf calls throughout the application.
// setLastCheckUpdating marca un job como "actualizando Last check" para la UI.
func setLastCheckUpdating(jobName string, updating bool) {
	lastCheckUpdatingMu.Lock()
	defer lastCheckUpdatingMu.Unlock()

	if updating {
		lastCheckUpdatingJob = jobName
		lastCheckUpdating = true
		if enableLogs {
			logPrintf("UI: Last check updating started for job=%s", jobName)
		}
	} else {
		if lastCheckUpdatingJob == jobName {
			lastCheckUpdating = false
			lastCheckUpdatingJob = ""
			if enableLogs {
				logPrintf("UI: Last check updating finished for job=%s", jobName)
			}
		}
	}
}

// isLastCheckUpdating devuelve true si la UI debe mostrar el spinner para ese job.
func isLastCheckUpdating(jobName string) bool {
	lastCheckUpdatingMu.Lock()
	defer lastCheckUpdatingMu.Unlock()
	return lastCheckUpdating && lastCheckUpdatingJob == jobName
}

// setStatus updates the status label text. It acquires a mutex to avoid
// concurrent updates. If the statusLabel is nil (UI not yet initialised)
// then the call is ignored.
func setStatus(t string) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if statusLabel != nil {
		statusLabel.SetText(t)
	}
}

// logPrintf prints to the standard logger when enableLogs is true. It is used
// instead of direct log.Printf calls throughout the application.
func logPrintf(format string, v ...interface{}) {
	if enableLogs {
		log.Printf(format, v...)
	}
}
