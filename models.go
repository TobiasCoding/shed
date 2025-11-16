package main

import "time"

// Config holds global configuration and job definitions. It is persisted to
// disk as JSON. ResticPath records the path to the restic executable. If
// AutoStart is true, the application will install itself into the system
// autostart folder. Jobs is a slice of backup jobs. SnapColWidths and
// FileColWidths store the persisted column widths for the snapshots and
// files tables in the UI so that user adjustments can be remembered
// between runs.
type Config struct {
	ResticPath    string    `json:"restic_path"`
	AutoStart     bool      `json:"auto_start"`
	Jobs          []Job     `json:"jobs"`
	SnapColWidths []float32 `json:"snap_col_widths,omitempty"`
	FileColWidths []float32 `json:"file_col_widths,omitempty"`
}

// Job describes a single backup job. Each job backs up a source directory
// (Source) into a destination repository (Destination). If Bitlocker is
// enabled then a password will be requested before starting the backup and
// that password will be used both to unlock the BitLocker volume and to
// encrypt the restic repository. Live controls whether automatic backups are
// scheduled, Interval and IntervalUnit define how often these backups run and
// KeepLast controls retention (how many snapshots to retain). RepoPassword
// stores the repository encryption password unless Bitlocker is enabled; in
// that case the password is provided at backup time.
type Job struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	Destination  string `json:"destination"`
	Bitlocker    bool   `json:"bitlocker"`
	Live         bool   `json:"live"`
	Interval     int    `json:"interval"`
	IntervalUnit string `json:"interval_unit"`
	KeepLast     int    `json:"keep_last"`
	RepoPassword string `json:"repo_password,omitempty"`
}

// Snapshot describes a restic snapshot. ShortID is the abbreviated snapshot
// identifier, Time when the snapshot was created, Paths lists the backed up
// directories, Hostname names the machine on which the backup was made and
// Tags contains any user supplied tags.
type Snapshot struct {
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
}

// FileRow describes a single file change within a snapshot. Change records
// whether the file was added (+), removed (-) or modified (M). Path stores
// the relative file path within the backed up source. Size records the file
// size at backup time (human readable, e.g. bytes), MTime and CTime record
// the modification and creation/change times formatted for display. An
// empty CTime indicates that the underlying platform did not provide a
// creation time.
type FileRow struct {
	Change string // "+", "-", "M"
	Path   string
	Size   string
	MTime  string
	CTime  string
}
