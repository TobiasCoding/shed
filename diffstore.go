package main

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"sync"
)

// diffMutex protects access to the persistent diff cache on disk. It is
// separate from cfgMutex because diff data lives in its own file.
var diffMutex sync.Mutex

// persistentDiff holds the cached file diffs for all jobs and snapshots. The
// first key is the job name to avoid conflicts between jobs that might have
// identical snapshot IDs. The second key is the snapshot ID. Each value is
// a slice of FileRow entries describing file changes.
//
// For example persistentDiff["Photos backup"]["1234abcd"] will return
// the list of FileRow objects for snapshot 1234abcd of job named "Photos
// backup".
var persistentDiff map[string]map[string][]FileRow

// diffFile returns the location of the diff cache file inside the config
// directory.
func diffFile() string {
	diffPath := filepath.Join(configDir(), "diffcache.json")
	logPrintf("[config] Using diffcache.json at: %s", diffPath)
	return diffPath
}

// loadPersistentDiff reads the diffcache.json from disk. It populates
// persistentDiff. If the file does not exist or cannot be parsed then
// persistentDiff is initialised to an empty map.
func loadPersistentDiff() {
	diffMutex.Lock()
	defer diffMutex.Unlock()
	if persistentDiff != nil {
		return
	}
	f := diffFile()
	data, err := ioutil.ReadFile(f)
	if err != nil {
		persistentDiff = make(map[string]map[string][]FileRow)
		return
	}
	var m map[string]map[string][]FileRow
	if err := json.Unmarshal(data, &m); err != nil {
		persistentDiff = make(map[string]map[string][]FileRow)
		return
	}
	persistentDiff = m
}

// savePersistentDiff writes the persistent diff map to disk. It must be
// called with diffMutex locked.
func savePersistentDiff() error {
	f := diffFile()
	b, err := json.MarshalIndent(persistentDiff, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(f, b, 0644)
}

// getDiffRows returns the cached diff rows for the given job and snapshot
// identifier. If no rows are cached the second return value will be false.
func getDiffRows(job Job, snapshotID string) ([]FileRow, bool) {
	loadPersistentDiff()
	diffMutex.Lock()
	defer diffMutex.Unlock()
	if persistentDiff == nil {
		return nil, false
	}
	if byJob, ok := persistentDiff[job.Name]; ok {
		if rows, ok2 := byJob[snapshotID]; ok2 {
			return rows, true
		}
	}
	return nil, false
}

// storeDiffRows persists a set of FileRow entries for a job and snapshot ID.
// It overwrites any existing entry for that pair. The diff file is saved
// immediately.
func storeDiffRows(job Job, snapshotID string, rows []FileRow) error {
	loadPersistentDiff()
	diffMutex.Lock()
	defer diffMutex.Unlock()
	if persistentDiff == nil {
		persistentDiff = make(map[string]map[string][]FileRow)
	}
	if persistentDiff[job.Name] == nil {
		persistentDiff[job.Name] = make(map[string][]FileRow)
	}
	persistentDiff[job.Name][snapshotID] = rows
	return savePersistentDiff()
}
