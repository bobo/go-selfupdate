package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	timeFile      = "cktime"                            // path to timestamp file relative to u.Dir
	platform      = runtime.GOOS + "-" + runtime.GOARCH // ex: linux-amd64
	stableChannel = "stable"
)

// UpdateInfo contains metadata about an available update
type UpdateInfo struct {
	Version string
	Sha256  []byte
	Channel string
	Date    time.Time
}

// Updater handles the self-update process
type Updater struct {
	CurrentVersion     string
	ApiURL             string
	CmdName            string
	BinURL             string
	DiffURL            string
	Dir                string
	ForceCheck         bool
	CheckTime          int // Hours between update checks
	RandomizeTime      int // Random hours to add to CheckTime
	Requester          Requester
	Channel            string
	ScheduledHour      *int // Hour to perform updates (0-23)
	Info               UpdateInfo
	OnSuccessfulUpdate func()
}

// BackgroundRun starts the update check and apply cycle
func (u *Updater) BackgroundRun(ctx context.Context) error {
	if err := os.MkdirAll(u.getExecRelativeDir(u.Dir), 0755); err != nil {
		return fmt.Errorf("failed to create update directory: %w", err)
	}

	if !u.shouldUpdate() {
		return nil
	}

	if err := canUpdate(); err != nil {
		return fmt.Errorf("update not possible: %w", err)
	}

	u.setNextUpdateTime()

	if err := u.Update(ctx); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	return nil
}

func (u *Updater) shouldUpdate() bool {
	if u.CurrentVersion == "dev" {
		slog.Info("skipping update for dev version")
		return false
	}

	if u.ForceCheck {
		slog.Info("force update check requested")
		return true
	}

	nextUpdate := u.NextUpdate()
	if nextUpdate.After(time.Now()) {
		slog.Info("next update scheduled for later",
			"next_update", nextUpdate.Format(time.RFC3339))
		return false
	}

	return true
}

// Update performs the self-update process
func (u *Updater) Update(ctx context.Context) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	if resolvedPath, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolvedPath
	}

	if err := u.fetchInfo(ctx); err != nil {
		return fmt.Errorf("failed to fetch update info: %w", err)
	}

	if u.Info.Version == u.CurrentVersion {
		slog.Info("already at latest version", "version", u.CurrentVersion)
		return nil
	}

	bin, err := u.fetchAndVerifyFullBin(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch update binary: %w", err)
	}

	if err := u.applyUpdate(execPath, bin); err != nil {
		return fmt.Errorf("failed to apply update: %w", err)
	}

	if u.OnSuccessfulUpdate != nil {
		u.OnSuccessfulUpdate()
	}

	return nil
}

func (u *Updater) applyUpdate(execPath string, newBin []byte) error {
	updateDir := filepath.Dir(execPath)
	filename := filepath.Base(execPath)

	newPath := filepath.Join(updateDir, fmt.Sprintf(".%s.new", filename))
	oldPath := filepath.Join(updateDir, fmt.Sprintf(".%s.old", filename))

	// Clean up any existing files
	os.Remove(newPath)
	os.Remove(oldPath)

	// Write new binary
	if err := os.WriteFile(newPath, newBin, 0755); err != nil {
		return err
	}

	// Swap files
	if err := os.Rename(execPath, oldPath); err != nil {
		return err
	}

	if err := os.Rename(newPath, execPath); err != nil {
		if rerr := os.Rename(oldPath, execPath); rerr != nil {
			return fmt.Errorf("failed to recover from update error: %v (original error: %w)", rerr, err)
		}
		return err
	}

	// Try to remove old binary
	if err := os.Remove(oldPath); err != nil {
		slog.Warn("failed to remove old binary", "error", err)
	}

	return nil
}

// Helper functions remain mostly unchanged but with improved error handling...

func (u *Updater) getExecRelativeDir(dir string) string {
	filename, _ := os.Executable()
	return filepath.Join(filepath.Dir(filename), dir)
}

func canUpdate() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}

	fileDir := filepath.Dir(path)
	fileName := filepath.Base(path)

	// Try to create a test file to verify write permissions
	newPath := filepath.Join(fileDir, fmt.Sprintf(".%s.new", fileName))
	fp, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	fp.Close()

	return os.Remove(newPath)
}

func (u *Updater) setNextUpdateTime() {
	if u.ScheduledHour != nil {
		next := u.calculateScheduledTime()
		writeTime(u.getExecRelativeDir(u.Dir+timeFile), next)
		return
	}

	if u.CheckTime > 0 {
		next := time.Now().Add(time.Duration(u.CheckTime) * time.Hour)
		writeTime(u.getExecRelativeDir(u.Dir+timeFile), next)
	}
}

func (u *Updater) NextUpdate() time.Time {
	path := u.getExecRelativeDir(u.Dir + timeFile)
	return readTime(path)
}

func (u *Updater) fetchInfo(ctx context.Context) error {
	// Placeholder for now - will implement in next part
	return nil
}

func (u *Updater) fetchAndVerifyFullBin(ctx context.Context) ([]byte, error) {
	// Placeholder for now - will implement in next part
	return nil, nil
}

// Helper functions

func readTime(path string) time.Time {
	p, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return time.Time{}
	}
	if err != nil {
		return time.Now().Add(1000 * time.Hour)
	}
	t, err := time.Parse(time.RFC3339, string(p))
	if err != nil {
		return time.Now().Add(1000 * time.Hour)
	}
	return t
}

func writeTime(path string, t time.Time) bool {
	return os.WriteFile(path, []byte(t.Format(time.RFC3339)), 0644) == nil
}

func (u *Updater) calculateScheduledTime() time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), *u.ScheduledHour, 0, 0, 0, time.Local)

	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
