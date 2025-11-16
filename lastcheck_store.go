package main

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"sync"
	"time"
)

// lastCheckData guarda, por job y snapshot, el timestamp del último check
// (cuando se calculó diff entre snapshots).
//
//	job.Name -> snapshotID -> time.RFC3339
var (
	lastCheckMu   sync.Mutex
	lastCheckData map[string]map[string]string
)

func lastCheckFile() string {
	return filepath.Join(configDir(), "lastcheck.json")
}

func loadLastCheck() {
	lastCheckMu.Lock()
	defer lastCheckMu.Unlock()

	if lastCheckData != nil {
		return
	}

	data, err := ioutil.ReadFile(lastCheckFile())
	if err != nil {
		lastCheckData = make(map[string]map[string]string)
		return
	}

	var m map[string]map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		lastCheckData = make(map[string]map[string]string)
		return
	}
	lastCheckData = m
}

func saveLastCheckLocked() error {
	b, err := json.MarshalIndent(lastCheckData, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(lastCheckFile(), b, 0644)
}

// setSnapshotLastCheck registra el timestamp de último check para un
// snapshot concreto de un job.
// setSnapshotLastCheck registra el timestamp de último check para un
// snapshot concreto de un job.
func setSnapshotLastCheck(job Job, snapshotID string, t time.Time) error {
	loadLastCheck()

	lastCheckMu.Lock()
	defer lastCheckMu.Unlock()

	if lastCheckData == nil {
		lastCheckData = make(map[string]map[string]string)
	}
	if lastCheckData[job.Name] == nil {
		lastCheckData[job.Name] = make(map[string]string)
	}

	ts := t.Format(time.RFC3339)
	lastCheckData[job.Name][snapshotID] = ts

	logPrintf("setSnapshotLastCheck: job=%s snapshot=%s time=%s", job.Name, snapshotID, ts)

	if err := saveLastCheckLocked(); err != nil {
		logPrintf("setSnapshotLastCheck: saveLastCheckLocked ERROR job=%s snapshot=%s: %v", job.Name, snapshotID, err)
		return err
	}

	logPrintf("setSnapshotLastCheck: saveLastCheckLocked OK job=%s snapshot=%s", job.Name, snapshotID)
	return nil
}

// getSnapshotLastCheck devuelve el último timestamp de check, si existe,
// para ese job+snapshot.
// getSnapshotLastCheck devuelve el último timestamp de check, si existe,
// para ese job+snapshot.
func getSnapshotLastCheck(job Job, snapshotID string) (time.Time, bool) {
	loadLastCheck()

	lastCheckMu.Lock()
	defer lastCheckMu.Unlock()

	if lastCheckData == nil {
		logPrintf("getSnapshotLastCheck: no data loaded job=%s snapshot=%s", job.Name, snapshotID)
		return time.Time{}, false
	}
	byJob, ok := lastCheckData[job.Name]
	if !ok {
		logPrintf("getSnapshotLastCheck: job not found job=%s snapshot=%s", job.Name, snapshotID)
		return time.Time{}, false
	}
	str, ok := byJob[snapshotID]
	if !ok {
		logPrintf("getSnapshotLastCheck: snapshot not found job=%s snapshot=%s", job.Name, snapshotID)
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		logPrintf("getSnapshotLastCheck: parse error job=%s snapshot=%s value=%q err=%v", job.Name, snapshotID, str, err)
		return time.Time{}, false
	}

	logPrintf("getSnapshotLastCheck: HIT job=%s snapshot=%s time=%s", job.Name, snapshotID, t.Format(time.RFC3339))
	return t, true
}
