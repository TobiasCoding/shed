package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// computeAndStoreLatestDiff computes the file differences for the most
// recently created snapshot of the given job. If the newest snapshot has
// no changes compared to the previous one, the newest snapshot is deleted
// (restic forget --prune) and the "last check" timestamp is recorded for
// the previous snapshot instead.
// computeAndStoreLatestDiff computes the file differences for the most
// recently created snapshot of the given job. If the newest snapshot has
// no changes compared to the previous one, the newest snapshot is deleted
// (restic forget --prune) and the "last check" timestamp is recorded for
// the previous snapshot instead.
func computeAndStoreLatestDiff(job Job, resticPath string, snaps []Snapshot) (string, error) {
	logPrintf("computeAndStoreLatestDiff: job=%s resticPath=%s snaps=%d", job.Name, resticPath, len(snaps))

	if len(snaps) == 0 {
		logPrintf("computeAndStoreLatestDiff: no snapshots for job=%s, nothing to do", job.Name)
		return "", nil
	}

	cur := snaps[len(snaps)-1].ShortID
	var prev string
	if len(snaps) > 1 {
		prev = snaps[len(snaps)-2].ShortID
	}
	logPrintf("computeAndStoreLatestDiff: cur=%s prev=%s", cur, prev)

	rows, err := computeDiff(job, resticPath, cur, prev)
	if err != nil {
		logPrintf("computeAndStoreLatestDiff: computeDiff error for job=%s cur=%s prev=%s: %v", job.Name, cur, prev, err)
		return "", err
	}
	logPrintf("computeAndStoreLatestDiff: diff rows=%d for job=%s cur=%s prev=%s", len(rows), job.Name, cur, prev)

	if len(rows) == 0 && prev != "" {
		// sin cambios: borrar snapshot nuevo y marcar last check en el anterior
		logPrintf("computeAndStoreLatestDiff: NO CHANGES, will forget cur=%s and set last check on prev=%s (job=%s)", cur, prev, job.Name)

		if err := forgetSnapshot(job, resticPath, cur); err != nil {
			logPrintf("computeAndStoreLatestDiff: forgetSnapshot error for job=%s cur=%s: %v", job.Name, cur, err)
			return "", err
		}

		if err := setSnapshotLastCheck(job, prev, time.Now()); err != nil {
			logPrintf("computeAndStoreLatestDiff: setSnapshotLastCheck(prev) error for job=%s prev=%s: %v", job.Name, prev, err)
		} else {
			logPrintf("computeAndStoreLatestDiff: setSnapshotLastCheck(prev) OK for job=%s prev=%s", job.Name, prev)
		}
		return prev, nil
	}

	// con cambios: guardar diff y last check en el snapshot actual
	logPrintf("computeAndStoreLatestDiff: CHANGES detected, storing diff for job=%s cur=%s", job.Name, cur)
	if err := storeDiffRows(job, cur, rows); err != nil {
		logPrintf("computeAndStoreLatestDiff: storeDiffRows error for job=%s cur=%s: %v", job.Name, cur, err)
		return "", err
	}

	if err := setSnapshotLastCheck(job, cur, time.Now()); err != nil {
		logPrintf("computeAndStoreLatestDiff: setSnapshotLastCheck(cur) error for job=%s cur=%s: %v", job.Name, cur, err)
	} else {
		logPrintf("computeAndStoreLatestDiff: setSnapshotLastCheck(cur) OK for job=%s cur=%s", job.Name, cur)
	}

	return cur, nil
}

// computeDiff computes the diff rows between two snapshots. If prevID is
// empty then all files in curID are treated as additions. The returned
// FileRow list is sorted by path. Size, modification and creation times are
// extracted from the current snapshot where applicable. Deleted files will
// have empty size and times. Errors from restic commands are propagated.
func computeDiff(job Job, resticPath, curID, prevID string) ([]FileRow, error) {
	// List nodes of current snapshot
	nodesCur, err := listNodes(job, resticPath, curID)
	if err != nil {
		return nil, err
	}
	// map absolute path to node info
	curMap := make(map[string]lsNode)
	for _, n := range nodesCur {
		curMap[n.Path] = n
	}
	var prevMap map[string]lsNode
	if prevID != "" {
		nodesPrev, err := listNodes(job, resticPath, prevID)
		if err != nil {
			return nil, err
		}
		prevMap = make(map[string]lsNode)
		for _, n := range nodesPrev {
			prevMap[n.Path] = n
		}
	}

	var changes []changeEntry
	if prevID == "" {
		// all files are additions
		for p := range curMap {
			changes = append(changes, changeEntry{Change: "+", Path: p})
		}
	} else {
		// run restic diff to get changed paths
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		args := []string{"-r", job.Destination}
		pwd := effectivePassword(job)
		if strings.TrimSpace(pwd) == "" {
			args = append(args, "--insecure-no-password")
		}
		args = append(args, "diff", prevID, curID)
		out, err := runRestic(ctx, resticPath, pwd, args...)
		if err != nil {
			return nil, err
		}
		// parse diff output lines
		lines := strings.Split(string(out), "\n")
		for _, ln := range lines {
			if len(ln) < 2 {
				continue
			}
			ch := ln[0]
			if ch != '+' && ch != '-' && ch != 'M' {
				continue
			}
			path := strings.TrimSpace(ln[1:])
			if path == "" {
				continue
			}
			changes = append(changes, changeEntry{Change: string(ch), Path: path})
		}
	}
	// Build file rows
	var rows []FileRow
	for _, c := range changes {
		relPath := toRelative(job.Source, c.Path)
		var size, mtime, ctime string
		if c.Change == "-" {
			// removed file: look up in prevMap
			if n, ok := prevMap[c.Path]; ok {
				size = formatSize(n.Size)
				mtime = formatTime(n.MTime)
				ctime = formatTime(n.CTime)
			}
		} else {
			// added or modified file: look up in curMap
			if n, ok := curMap[c.Path]; ok {
				size = formatSize(n.Size)
				mtime = formatTime(n.MTime)
				ctime = formatTime(n.CTime)
			}
		}
		rows = append(rows, FileRow{
			Change: c.Change,
			Path:   relPath,
			Size:   size,
			MTime:  mtime,
			CTime:  ctime,
		})
	}
	// sort rows by path for deterministic ordering
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Path < rows[j].Path
	})
	return rows, nil
}

// changeEntry is an intermediate type used when parsing restic diff output.
type changeEntry struct {
	Change string
	Path   string
}

// lsNode represents a single file node within a restic snapshot as returned
// by the ls --json command. Only the fields relevant to the UI are stored.
type lsNode struct {
	Path  string
	Size  *int64     `json:"size"`
	MTime *time.Time `json:"mtime"`
	CTime *time.Time `json:"ctime"`
	Type  string     `json:"type"`
}

// listNodes executes "restic ls --json" for a snapshot and returns all
// file-type nodes (directories are skipped). The returned nodes contain
// absolute paths. The function streams and decodes JSON lines to avoid
// loading the entire output into memory at once.
func listNodes(job Job, resticPath, snapID string) ([]lsNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	args := []string{"-r", job.Destination}
	pwd := effectivePassword(job)
	if strings.TrimSpace(pwd) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "ls", "--json", snapID)
	// Use exec.Command directly to stream output to decoder
	cmd := exec.CommandContext(ctx, resticPath, args...)
	env := os.Environ()
	if strings.TrimSpace(pwd) != "" {
		env = append(env, "RESTIC_PASSWORD="+pwd)
	}
	cmd.Env = env
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer cmd.Wait()
	dec := json.NewDecoder(out)
	var nodes []lsNode
	for {
		var m map[string]interface{}
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		// Each JSON line contains a struct_type field; we're interested in
		// nodes only
		if t, ok := m["struct_type"]; ok {
			if tStr, ok := t.(string); ok && tStr == "node" {
				// decode again into lsNode for typed fields
				b, _ := json.Marshal(m)
				var n lsNode
				if err := json.Unmarshal(b, &n); err != nil {
					continue
				}
				if n.Type == "file" {
					nodes = append(nodes, n)
				}
			}
		}
	}
	return nodes, nil
}

// toRelative converts an absolute path into a path relative to the job's
// source directory. Restic always stores absolute paths using forward
// slashes, even on Windows. The source path is converted to forward slash
// form as well. Leading slashes are trimmed. If the path is not under
// source it is returned unchanged.
func toRelative(source, absPath string) string {
	src := filepath.ToSlash(filepath.Clean(source))
	p := absPath
	if strings.HasPrefix(p, src) {
		rel := strings.TrimPrefix(p[len(src):], "/")
		if rel == "" {
			return "."
		}
		return rel
	}
	// On Windows restic may strip the volume letter, e.g. C:\path becomes /C/path
	// We therefore compare against just the last element of the volume.
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(source)
		if vol != "" {
			// volume name like C:
			v := strings.TrimSuffix(strings.ToUpper(vol), ":")
			prefix := "/" + v
			if strings.HasPrefix(strings.ToUpper(p), prefix) {
				rel := strings.TrimPrefix(p[len(prefix):], "/")
				if rel == "" {
					return "."
				}
				return rel
			}
		}
	}
	return p
}

// formatSize converts a pointer to an int64 into a human readable string. If
// the pointer is nil the returned string is "-". Sizes are displayed in
// bytes; no further units are applied to avoid confusion.
func formatSize(p *int64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatInt(*p, 10)
}

// formatTime converts a pointer to time.Time into a formatted string. If
// the pointer is nil the returned string is "-". Times are formatted as
// dd/mm/yyyy hh:mm:ss.
func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("02/01/2006 15:04:05")
}

// diffSummary devuelve la cantidad de archivos agregados (+), modificados (M)
// y eliminados (-) para el snapshot dado. Los datos se leen del cache
// persistente (diffcache.json). Si no hay info para ese snapshot, ok = false.
func diffSummary(job Job, snapshotID string) (added, modified, deleted int, ok bool) {
	rows, ok := getDiffRows(job, snapshotID)
	if !ok {
		return 0, 0, 0, false
	}
	for _, r := range rows {
		switch r.Change {
		case "+":
			added++
		case "M":
			modified++
		case "-":
			deleted++
		}
	}
	return added, modified, deleted, true
}

func restoredSnapshotKey(snapshotID string) string {
	return snapshotID + " (restored)"
}

// computeAndStoreRestoreDiff calcula el diff de un restore:
//
//   - curID: el snapshot que se restauró.
//   - prevID: la versión cronológicamente previa al momento del restore
//     (último snapshot de la lista "snaps").
//   - Guarda el resultado en diffcache.json bajo la clave "<ID> (restored)".
func computeAndStoreRestoreDiff(job Job, resticPath string, snaps []Snapshot, restoredID string) (string, error) {
	if len(snaps) == 0 {
		return "", nil
	}

	// "Versión cronológicamente previa": el snapshot más nuevo existente
	// antes de registrar el restore.
	prevID := snaps[len(snaps)-1].ShortID

	// Si el único snapshot que existe es el mismo que se restauró,
	// no tiene sentido diffs: usamos prev vacío (sin cambios).
	if len(snaps) == 1 && prevID == restoredID {
		prevID = ""
	}

	rows, err := computeDiff(job, resticPath, restoredID, prevID)
	if err != nil {
		return "", err
	}

	key := restoredSnapshotKey(restoredID)
	if err := storeDiffRows(job, key, rows); err != nil {
		return "", err
	}
	return key, nil
}
