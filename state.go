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
var (
	allSnapshots    []Snapshot
	filterSnapQuery string
	filterSnapshots []Snapshot
	snapTable       *widget.Table
	selectedSnapIdx = -1
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
