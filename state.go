package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2/widget"
)

// enableLogs controls whether verbose logging is printed. It is set via the
// command-line flag --logs and used by helper functions to decide whether
// diagnostic messages should be printed.
var enableLogs bool

// Archivo de logs (mismo directorio que config.json).
var (
	logFile     *os.File
	logFileOnce sync.Once
)

// initLogFile abre (o crea) el archivo de log en el mismo directorio que config.json.
func initLogFile() {
	if cfgFile == "" {
		// Aún no sabemos dónde está el config, no podemos crear el log.
		return
	}
	dir := filepath.Dir(cfgFile)
	path := filepath.Join(dir, "shed.log")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		// Si falla, simplemente dejamos que log.Printf vaya a donde esté configurado.
		if enableLogs {
			log.Printf("could not open log file %q: %v", path, err)
		}
		return
	}
	logFile = f
	log.SetOutput(logFile)
	if enableLogs {
		log.Printf("[log] logging to %s", path)
	}
}

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
// Snapshot filtering and selection
var (
	allSnapshots    []Snapshot
	filterSnapQuery string
	filterSnapshots []Snapshot
	snapTable       *widget.Table
	selectedSnapIdx = -1

	// jobName -> snapshotID -> restoreTime (sólo para esta sesión)
	restoredSnapshots map[string]map[string]time.Time

	// Estado de UI: si estamos recalculando el Last check para un job,
	// mostramos un spinner en la columna correspondiente.
	lastCheckUpdatingJob string
	lastCheckUpdating    bool
	lastCheckUpdatingMu  sync.Mutex
)

// Columna y sentido de orden de snapshots.
type SnapSortColumn int

const (
	SnapSortID SnapSortColumn = iota
	SnapSortDate
	SnapSortTime
	SnapSortHost
	SnapSortPaths
	SnapSortTags
	SnapSortLastCheck
)

var (
	// Por defecto ordenamos por Last check ascendente.
	snapSortColumn = SnapSortLastCheck
	snapSortAsc    = true
)

// markSnapshotRestored marca un snapshot como restaurado para un job dado
// y recuerda el timestamp exacto del restore (sólo en memoria).
func markSnapshotRestored(jobName, snapshotID string, t time.Time) {
	if restoredSnapshots == nil {
		restoredSnapshots = make(map[string]map[string]time.Time)
	}
	if restoredSnapshots[jobName] == nil {
		restoredSnapshots[jobName] = make(map[string]time.Time)
	}
	restoredSnapshots[jobName][snapshotID] = t
}

// snapshotRestoreTime devuelve el timestamp de restore para ese snapshot
// (en esta sesión), o false si nunca se restauró.
func snapshotRestoreTime(jobName, snapshotID string) (time.Time, bool) {
	if restoredSnapshots == nil {
		return time.Time{}, false
	}
	m := restoredSnapshots[jobName]
	if m == nil {
		return time.Time{}, false
	}
	t, ok := m[snapshotID]
	return t, ok
}

// File diff filtering and selection
var (
	allFiles        []FileRow
	filterFileQuery string
	filterFiles     []FileRow
	filesTable      *widget.Table
)

// Columna y sentido de orden de archivos.
type FileSortColumn int

const (
	FileSortChange FileSortColumn = iota
	FileSortPath
	FileSortSize
	FileSortMTime
	FileSortCTime
)

var (
	// Por defecto: Path ascendente.
	fileSortColumn = FileSortPath
	fileSortAsc    = true
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
	go func() {
		// Si estamos ordenando por Last check, reordenamos antes de refrescar.
		if snapSortColumn == SnapSortLastCheck {
			sortSnapshotsForCurrentJob()
			rebuildSnapFilter(filterSnapQuery)
		}
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

// logPrintf escribe logs cuando enableLogs es true. Inicializa perezosamente
// un archivo shed.log en el mismo directorio que config.json.
func logPrintf(format string, v ...interface{}) {
	if !enableLogs {
		return
	}
	logFileOnce.Do(initLogFile)
	log.Printf(format, v...)
}

// sortSnapshotsForCurrentJob ordena allSnapshots según la columna y sentido actuales.
func sortSnapshotsForCurrentJob() {
	if len(allSnapshots) == 0 {
		return
	}

	var (
		job     Job
		haveJob bool
	)
	if cfg != nil && selectedJobIndex >= 0 && selectedJobIndex < len(cfg.Jobs) {
		job = cfg.Jobs[selectedJobIndex]
		haveJob = true
	}

	sort.SliceStable(allSnapshots, func(i, j int) bool {
		a := allSnapshots[i]
		b := allSnapshots[j]

		var less bool

		switch snapSortColumn {
		case SnapSortID:
			less = strings.ToLower(a.ShortID) < strings.ToLower(b.ShortID)
		case SnapSortDate, SnapSortTime:
			// Ambas columnas ordenan por el timestamp completo.
			less = a.Time.Before(b.Time)
		case SnapSortHost:
			less = strings.ToLower(a.Hostname) < strings.ToLower(b.Hostname)
		case SnapSortPaths:
			less = len(a.Paths) < len(b.Paths)
		case SnapSortTags:
			less = strings.ToLower(strings.Join(a.Tags, ",")) < strings.ToLower(strings.Join(b.Tags, ","))
		case SnapSortLastCheck:
			if !haveJob {
				less = a.Time.Before(b.Time)
				break
			}
			ta, oka := getSnapshotLastCheck(job, a.ShortID)
			tb, okb := getSnapshotLastCheck(job, b.ShortID)

			switch {
			case !oka && !okb:
				// si ninguno tiene Last check, usamos la fecha de snapshot.
				less = a.Time.Before(b.Time)
			case !oka && okb:
				// los sin Last check van al final en orden asc.
				less = false
			case oka && !okb:
				less = true
			default:
				less = ta.Before(tb)
			}
		default:
			less = a.Time.Before(b.Time)
		}

		if snapSortAsc {
			return less
		}
		return !less
	})
}

func parseFileTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return time.Time{}, false
	}
	layouts := []string{
		"02/01/2006 15:04:05", // formato que se ve en tu screenshot
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		} else if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	if n, err := strconv.ParseInt(digits.String(), 10, 64); err == nil {
		return n
	}
	return 0
}

// sortFiles ordena allFiles según la columna y sentido actuales.
func sortFiles() {
	if len(allFiles) == 0 {
		return
	}

	sort.SliceStable(allFiles, func(i, j int) bool {
		a := allFiles[i]
		b := allFiles[j]

		var less bool

		switch fileSortColumn {
		case FileSortChange:
			less = strings.ToLower(a.Change) < strings.ToLower(b.Change)
		case FileSortPath:
			less = strings.ToLower(a.Path) < strings.ToLower(b.Path)
		case FileSortSize:
			less = parseSize(a.Size) < parseSize(b.Size)
		case FileSortMTime:
			ta, oka := parseFileTime(a.MTime)
			tb, okb := parseFileTime(b.MTime)
			if oka && okb {
				less = ta.Before(tb)
			} else {
				less = strings.ToLower(a.MTime) < strings.ToLower(b.MTime)
			}
		case FileSortCTime:
			ta, oka := parseFileTime(a.CTime)
			tb, okb := parseFileTime(b.CTime)
			if oka && okb {
				less = ta.Before(tb)
			} else {
				less = strings.ToLower(a.CTime) < strings.ToLower(b.CTime)
			}
		default:
			less = strings.ToLower(a.Path) < strings.ToLower(b.Path)
		}

		if fileSortAsc {
			return less
		}
		return !less
	})
}
