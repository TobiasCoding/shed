package main

import (
    "context"
    "time"
    "strings"
)

// startLiveBackups launches goroutines for all jobs marked as Live. Each
// goroutine periodically triggers a backup based on the job's Interval and
// IntervalUnit. If a watcher is already running for a job it will be
// cancelled and restarted. This function should be called whenever the job
// list is modified.
func startLiveBackups(resticPath string) {
    // cancel existing
    for idx, cancel := range liveCancelMap {
        cancel()
        delete(liveCancelMap, idx)
    }
    for idx, job := range cfg.Jobs {
        if job.Live && job.Interval > 0 {
            d := toDuration(job.Interval, job.IntervalUnit)
            if d <= 0 {
                continue
            }
            ctx, cancel := context.WithCancel(context.Background())
            liveCancelMap[idx] = cancel
            go watchJob(ctx, idx, job, d, resticPath)
        }
    }
}

// toDuration converts an integer and unit string into a time.Duration. If the
// unit is unrecognised the returned duration is zero.
func toDuration(v int, unit string) time.Duration {
    switch strings.ToLower(unit) {
    case "seconds", "second", "s":
        return time.Duration(v) * time.Second
    case "minutes", "minute", "min", "m":
        return time.Duration(v) * time.Minute
    case "hours", "hour", "h":
        return time.Duration(v) * time.Hour
    case "days", "day", "d":
        return time.Duration(v) * 24 * time.Hour
    default:
        return 0
    }
}

// watchJob runs in a goroutine and periodically runs doBackup for a given job
// until the context is cancelled. After each successful backup it triggers
// snapshot reload and diff computation. Errors are logged via logPrintf and
// ignored thereafter. The snapshot and file tables will be refreshed on
// successful backup.
func watchJob(ctx context.Context, idx int, job Job, interval time.Duration, resticPath string) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    // initial delay until first tick
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // run backup
            logPrintf("[live] running periodic backup for job %s", job.Name)
            // Only run if the job still exists and Live is still enabled
            cfgMutex.Lock()
            if idx >= len(cfg.Jobs) || !cfg.Jobs[idx].Live {
                cfgMutex.Unlock()
                return
            }
            // copy job to avoid race on RepoPassword
            currentJob := cfg.Jobs[idx]
            cfgMutex.Unlock()
            err := doBackup(currentJob, resticPath, "", nil)
            if err != nil {
                logPrintf("[live] backup error for job %s: %v", currentJob.Name, err)
                continue
            }
            // reload snapshots and compute diff
            snaps, err := listSnapshots(currentJob, resticPath)
            if err == nil {
                allSnapshots = snaps
                // rebuild snapshot filter using current filter string
                rebuildSnapFilter(filterSnapQuery)
                if snapTable != nil {
                    snapTable.Refresh()
                }
                // compute diff for latest snapshot
                if _, err2 := computeAndStoreLatestDiff(currentJob, resticPath, snaps); err2 != nil {
                    logPrintf("[live] diff computation failed for job %s: %v", currentJob.Name, err2)
                }
                // refresh files table using current filter
                rebuildFilesFilter(filterFileQuery)
                if filesTable != nil {
                    filesTable.Refresh()
                }
            }
        }
    }
}