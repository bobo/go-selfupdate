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
	"strings"
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

// UpdateScheduler defines how update timing is handled
type UpdateScheduler interface {
	// ShouldUpdate returns true if an update should be performed now
	ShouldUpdate(currentVersion string, forceCheck bool) bool
	// SetNextUpdate schedules the next update time
	SetNextUpdate()
	// NextUpdate returns when the next update is scheduled
	NextUpdate() time.Time
}

// DailyScheduler implements UpdateScheduler for updates at a specific hour
type DailyScheduler struct {
	hour     int
	timeFile string
}

// NewDailyScheduler creates a scheduler that runs once per day at the specified hour
func NewDailyScheduler(hour int) *DailyScheduler {
	return &DailyScheduler{
		hour:     hour,
		timeFile: timeFile,
	}
}

func (s *DailyScheduler) ShouldUpdate(currentVersion string, forceCheck bool) bool {
	if currentVersion == "dev" {
		slog.Info("skipping update for dev version")
		return false
	}
	if forceCheck {
		slog.Info("force update check requested")
		return true
	}
	next := s.NextUpdate()
	if next.After(time.Now()) {
		slog.Info("next update scheduled for later",
			"next_update", next.Format(time.RFC3339))
		return false
	}
	return true
}

func (s *DailyScheduler) SetNextUpdate() {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), s.hour, 0, 0, 0, time.Local)
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	writeTime(s.timeFile, next)
}

func (s *DailyScheduler) NextUpdate() time.Time {
	return readTime(s.timeFile)
}

// IntervalScheduler implements UpdateScheduler for updates at fixed intervals
type IntervalScheduler struct {
	checkTime     int
	randomizeTime int
	timeFile      string
}

// NewIntervalScheduler creates a scheduler that runs at fixed intervals with optional randomization
func NewIntervalScheduler(checkTime, randomizeTime int) *IntervalScheduler {
	return &IntervalScheduler{
		checkTime:     checkTime,
		randomizeTime: randomizeTime,
		timeFile:      timeFile,
	}
}

func (s *IntervalScheduler) ShouldUpdate(currentVersion string, forceCheck bool) bool {
	if currentVersion == "dev" {
		slog.Info("skipping update for dev version")
		return false
	}
	if forceCheck {
		slog.Info("force update check requested")
		return true
	}
	next := s.NextUpdate()
	if next.After(time.Now()) {
		slog.Info("next update scheduled for later",
			"next_update", next.Format(time.RFC3339))
		return false
	}
	return true
}

func (s *IntervalScheduler) SetNextUpdate() {
	next := time.Now().Add(time.Duration(s.checkTime) * time.Hour)
	if s.randomizeTime > 0 {
		next = next.Add(time.Duration(randInt(0, s.randomizeTime)) * time.Hour)
	}
	writeTime(s.timeFile, next)
}

func (s *IntervalScheduler) NextUpdate() time.Time {
	return readTime(s.timeFile)
}

var randSource = func() int64 {
	return time.Now().UnixNano()
}

func randInt(min, max int) int {
	if min == max {
		return min
	}
	return min + int(randSource()%int64(max-min+1))
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
	Scheduler          UpdateScheduler
	Requester          Requester
	Channel            string
	Info               UpdateInfo
	OnSuccessfulUpdate func()
}

// UpdateIfNeeded starts the update check and apply cycle
func (u *Updater) UpdateIfNeeded() error {
	ctx := context.Background()
	if err := os.MkdirAll(getExecRelativeDir(u.Dir), 0755); err != nil {
		return fmt.Errorf("failed to create update directory: %w", err)
	}

	if !u.Scheduler.ShouldUpdate(u.CurrentVersion, u.ForceCheck) {
		return nil
	}

	if err := canUpdate(); err != nil {
		return fmt.Errorf("update not possible: %w", err)
	}

	u.Scheduler.SetNextUpdate()

	if err := u.Update(ctx); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	return nil
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

	if err := u.fetchInfo(); err != nil {
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

func (u *Updater) NextUpdate() time.Time {
	return u.Scheduler.NextUpdate()
}

func (u *Updater) fetchInfo() error {
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
		u.Requester = &HTTPRequester{}
	}
	if !strings.HasSuffix(u.ApiURL, "/") {
		u.ApiURL = u.ApiURL + "/"
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
		u.Requester = &HTTPRequester{}
	}
	if !strings.HasSuffix(u.BinURL, "/") {
		u.BinURL = u.BinURL + "/"
	}
	fmt.Println("fetching binary from", u.BinURL+urlPath)
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
