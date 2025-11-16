package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// detectRestic tries to locate the restic executable. If the supplied path
// parameter is non-empty it is checked first. Otherwise the system PATH is
// searched for "restic" (or "restic.exe" on Windows) and a few well-known
// installation locations on Windows. If enableLogs is true diagnostic
// messages will be printed. On success the absolute path to the executable is
// returned. On failure an empty string is returned.
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

// checkRestic runs "restic version" on the given path to verify the binary
// executes successfully within a short timeout. It returns nil on success.
func checkRestic(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "version").Run()
}

// runRestic executes the restic binary with the supplied arguments. If a
// password is provided it is passed via the RESTIC_PASSWORD environment
// variable. The command output (combined stdout/stderr) is returned. On
// failure the error from exec.Command is returned alongside the output.
func runRestic(ctx context.Context, resticPath, password string, args ...string) ([]byte, error) {
	if resticPath == "" {
		return nil, errors.New("restic path is not set")
	}
	cmd := exec.CommandContext(ctx, resticPath, args...)
	env := os.Environ()
	if strings.TrimSpace(password) != "" {
		env = append(env, "RESTIC_PASSWORD="+password)
	}
	cmd.Env = env
	if enableLogs {
		logPrintf("[restic] %q %v", resticPath, args)
	}
	out, err := cmd.CombinedOutput()
	if enableLogs {
		if err != nil {
			logPrintf("[restic] ERROR: %v\n[restic] OUTPUT:\n%s", err, string(out))
		} else {
			trimmed := string(out)
			if len(trimmed) > 200 {
				trimmed = trimmed[:200]
			}
			logPrintf("[restic] OK. OUTPUT (trimmed): %s", trimmed)
		}
	}
	return out, err
}

// ensureRepo checks whether a restic repository exists at dest. If not it
// initialises a new repository. The password controls encryption; if empty
// the repository is initialised with insecure-no-password. The repository
// initialisation is performed with a generous timeout. On success nil is
// returned.
func ensureRepo(resticPath, dest, password string) error {
	if _, err := os.Stat(filepath.Join(dest, "config")); err == nil {
		return nil
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	args := []string{"-r", dest}
	if strings.TrimSpace(password) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "init")
	if _, err := runRestic(ctx, resticPath, password, args...); err != nil {
		return fmt.Errorf("restic init failed: %w", err)
	}
	return nil
}

// doBackup performs a restic backup of job.Source into job.Destination. It
// ensures the repository exists, runs the backup and optionally applies
// retention policies. The progress callback is called with human readable
// status messages. If RepoPassword is empty and passwordOverride is supplied
// then passwordOverride is used instead. The retention policy (KeepLast) is
// applied after the backup completes. On success nil is returned.
func doBackup(job Job, resticPath string, passwordOverride string, progress func(string)) error {
	// Determinamos la contraseña efectiva: config > cache memoria > override.
	pwd := effectivePassword(job)
	if strings.TrimSpace(pwd) == "" && strings.TrimSpace(passwordOverride) != "" {
		pwd = passwordOverride
	}

	// Si tenemos una contraseña, la dejamos cacheada para futuras operaciones
	if strings.TrimSpace(pwd) != "" {
		setJobPassword(job.Name, pwd)
	}

	if err := ensureRepo(resticPath, job.Destination, pwd); err != nil {
		return err
	}
	if progress != nil {
		progress("Starting backup…")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{"-r", job.Destination}
	if strings.TrimSpace(pwd) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "backup", job.Source)
	out, err := runRestic(ctx, resticPath, pwd, args...)
	if err != nil {
		if progress != nil {
			progress("Backup failed")
		}
		return fmt.Errorf("backup error: %w (out=%s)", err, string(out))
	}
	if progress != nil {
		progress("Backup complete")
	}
	if job.KeepLast > 0 {
		if progress != nil {
			progress(fmt.Sprintf("Applying retention keep-last=%d…", job.KeepLast))
		}
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()
		args2 := []string{"-r", job.Destination}
		if strings.TrimSpace(pwd) == "" {
			args2 = append(args2, "--insecure-no-password")
		}
		args2 = append(args2, "forget", fmt.Sprintf("--keep-last=%d", job.KeepLast), "--prune")
		out2, err2 := runRestic(ctx2, resticPath, pwd, args2...)
		if err2 != nil {
			return fmt.Errorf("forget/prune error: %w (out=%s)", err2, string(out2))
		}
		if progress != nil {
			progress("Retention applied")
		}
	}
	return nil
}

// forgetSnapshot deletes a single snapshot from the repository using
// "restic forget <id> --prune". It uses the job's effective password.
func forgetSnapshot(job Job, resticPath, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return errors.New("snapshot ID must be provided")
	}

	pwd := effectivePassword(job)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	args := []string{"-r", job.Destination}
	if strings.TrimSpace(pwd) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "forget", snapshotID, "--prune")

	out, err := runRestic(ctx, resticPath, pwd, args...)
	if err != nil {
		return fmt.Errorf("restic forget failed: %w (out=%s)", err, string(out))
	}
	return nil
}

// listSnapshots retrieves the list of snapshots for a job from the repository.
// The returned slice is sorted by time in ascending order. If el repositorio
// todavía no existe, devuelve una lista vacía sin crear nada.
func listSnapshots(job Job, resticPath string) ([]Snapshot, error) {
	pwd := effectivePassword(job)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := []string{"-r", job.Destination}
	if strings.TrimSpace(pwd) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "snapshots", "--json")
	out, err := runRestic(ctx, resticPath, pwd, args...)
	if err != nil {
		// Si el repo no existe, interpretamos como "sin snapshots".
		s := string(out)
		if strings.Contains(s, "Is there a repository at the following location") ||
			strings.Contains(s, "does not exist") {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		return nil, err
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Time.Before(snaps[j].Time)
	})
	return snaps, nil
}

// restoreSnapshot restores a snapshot into a folder inside the destination
// repository. The returned string is the path to the restore location. If
// passwordOverride is provided it is used instead of job.RepoPassword.
func restoreSnapshot(job Job, resticPath, snapshotID string, passwordOverride string) (string, error) {
	if snapshotID == "" {
		return "", errors.New("snapshot ID must be provided")
	}
	pwd := effectivePassword(job)
	if strings.TrimSpace(pwd) == "" && strings.TrimSpace(passwordOverride) != "" {
		pwd = passwordOverride
	}

	restoreRoot := filepath.Join(job.Destination, "restores")
	target := filepath.Join(restoreRoot, "restore_"+snapshotID)
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{"-r", job.Destination}
	if strings.TrimSpace(pwd) == "" {
		args = append(args, "--insecure-no-password")
	}
	args = append(args, "restore", snapshotID, "--target", target)
	_, err := runRestic(ctx, resticPath, pwd, args...)
	if err != nil {
		return "", fmt.Errorf("restore failed: %w", err)
	}
	return target, nil
}
