package selfupdate

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Common errors
var (
	ErrHashMismatch    = errors.New("new file hash mismatch after patch")
	ErrInvalidHash     = errors.New("invalid hash in update info")
	ErrChannelMismatch = errors.New("update channel mismatch")
	ErrNoRequester     = errors.New("no HTTP requester configured")
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
	if err := os.MkdirAll(getExecRelativeDir(u.Dir), 0755); err != nil {
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

// Helper functions

func (u *Updater) setNextUpdateTime() {
	if u.ScheduledHour != nil {
		next := u.calculateScheduledTime()
		writeTime(getExecRelativeDir(u.Dir+timeFile), next)
		return
	}

	if u.CheckTime > 0 {
		next := time.Now().Add(time.Duration(u.CheckTime) * time.Hour)
		writeTime(getExecRelativeDir(u.Dir+timeFile), next)
	}
}

func (u *Updater) NextUpdate() time.Time {
	path := getExecRelativeDir(u.Dir + timeFile)
	return readTime(path)
}

func (u *Updater) fetchInfo(ctx context.Context) error {
	channel := u.Channel
	if channel == "" {
		channel = stableChannel
	}

	// Build URL path
	urlPath := url.PathEscape(u.CmdName)
	if channel != stableChannel {
		urlPath = filepath.Join(urlPath, url.PathEscape(channel))
	}
	urlPath = filepath.Join(urlPath, url.PathEscape(platform)) + ".json"

	if u.Requester == nil {
		return ErrNoRequester
	}

	r, err := u.Requester.Fetch(u.ApiURL + urlPath)
	if err != nil {
		return fmt.Errorf("failed to fetch update info: %w", err)
	}
	defer r.Close()

	var info UpdateInfo
	if err := json.NewDecoder(r).Decode(&info); err != nil {
		return fmt.Errorf("failed to decode update info: %w", err)
	}

	if len(info.Sha256) != sha256.Size {
		return ErrInvalidHash
	}

	if info.Channel != channel {
		return fmt.Errorf("%w: expected %s, got %s",
			ErrChannelMismatch, channel, info.Channel)
	}

	u.Info = info
	return nil
}

func (u *Updater) fetchAndVerifyFullBin(ctx context.Context) ([]byte, error) {
	channel := u.Channel
	if channel == "" {
		channel = stableChannel
	}

	// Build URL path
	urlPath := url.PathEscape(u.CmdName)
	if channel != stableChannel {
		urlPath = filepath.Join(urlPath, url.PathEscape(channel))
	}
	urlPath = filepath.Join(urlPath,
		url.PathEscape(u.Info.Version),
		url.PathEscape(platform)) + ".gz"

	if u.Requester == nil {
		return nil, ErrNoRequester
	}

	r, err := u.Requester.Fetch(u.BinURL + urlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch binary: %w", err)
	}
	defer r.Close()

	// Decompress gzip
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	// Read and verify binary
	bin, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("failed to read binary: %w", err)
	}

	if !verifyHash(bin, u.Info.Sha256) {
		return nil, ErrHashMismatch
	}

	return bin, nil
}

func (u *Updater) calculateScheduledTime() time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), *u.ScheduledHour, 0, 0, 0, time.Local)

	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
