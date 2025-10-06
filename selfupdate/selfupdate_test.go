package selfupdate

import (
	"bytes"
	"io"
	"runtime"
	"testing"
	"time"
)

// Test helpers
func setTestRandSource(value int64) func() {
	original := randSource
	randSource = func() int64 { return value }
	return func() {
		randSource = original
	}
}


func TestUpdaterSchedulers(t *testing.T) {
	tests := []struct {
		name         string
		scheduler    UpdateScheduler
		expectUpdate bool
	}{
		{"interval no random", NewIntervalScheduler(1, 0), true},
		{"interval with random", NewIntervalScheduler(100, 100), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := &mockRequester{}
			mr.handleRequest(
				func(url string) (io.ReadCloser, error) {
					expectedURL := getExpectedURL()
					if url != expectedURL {
						t.Errorf("unexpected URL: got %s, want %s", url, expectedURL)
					}
					return newTestReaderCloser(`{}`), nil
				})
			updater := createUpdater(mr)
			updater.Scheduler = tt.scheduler
			updater.ForceCheck = false
			updater.Info.Sha256 = []byte("Q2vvTOW0p69A37StVANN+/ko1ZQDTElomq7fVcex/02=")
			updater.UpdateIfNeeded()

			nextUpdate := updater.NextUpdate()
			if !nextUpdate.After(time.Now()) {
				t.Error("NextUpdate should be after current time")
			}

			// For interval scheduler, check max time
			if is, ok := tt.scheduler.(*IntervalScheduler); ok {
				maxHrs := time.Duration(is.checkTime+is.randomizeTime) * time.Hour
				maxTime := time.Now().Add(maxHrs)
				if !nextUpdate.Before(maxTime) {
					t.Errorf("NextUpdate should be less than %s from now; got %s", maxHrs, nextUpdate)
				}
			}
		})
	}
}

func TestFetchInfo(t *testing.T) {
	mr := &mockRequester{}
	mr.handleRequest(
		func(url string) (io.ReadCloser, error) {
			expectedURL := getExpectedURL()
			equals(t, expectedURL, url)
			return newTestReaderCloser(`{
    "Version": "2023-07-09-66c6c12",
    "Sha256": "Q2vvTOW0p69A37StVANN+/ko1ZQDTElomq7fVcex/02=",
	"Date": "2023-07-09T00:00:00Z"
}`), nil
		})
	updater := createUpdater(mr)
	updater.Scheduler = NewIntervalScheduler(24, 0)

	err := updater.fetchInfo()
	if err != nil {
		t.Errorf("Error occurred: %#v", err)
	}
	equals(t, "2023-07-09-66c6c12", updater.Info.Version)
	equals(t, time.Date(2023, 7, 9, 0, 0, 0, 0, time.UTC), updater.Info.Date)
}

func getExpectedURL() string {
	return "http://updates.yourdomain.com/myapp/" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
}

func createUpdater(mr *mockRequester) *Updater {
	return &Updater{
		CurrentVersion: "1.2",
		ApiURL:         "http://updates.yourdomain.com/",
		BinURL:         "http://updates.yourdownmain.com/",
		DiffURL:        "http://updates.yourdomain.com/",
		Dir:            "update/",
		CmdName:        "myapp", // app name
		Requester:      mr,
		Info:           UpdateInfo{},
	}
}

func equals(t *testing.T, expected, actual interface{}) {
	if expected != actual {
		t.Logf("Expected: %#v got %#v\n", expected, actual)
		t.Fail()
	}
}

type testReadCloser struct {
	buffer *bytes.Buffer
}

func newTestReaderCloser(payload string) io.ReadCloser {
	return &testReadCloser{buffer: bytes.NewBufferString(payload)}
}

func (trc *testReadCloser) Read(p []byte) (n int, err error) {
	return trc.buffer.Read(p)
}

func (trc *testReadCloser) Close() error {
	return nil
}

func TestIntervalScheduler(t *testing.T) {
	t.Run("should skip dev version", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0)
		if s.ShouldUpdate("dev", false) {
			t.Error("Should skip dev version")
		}
	})

	t.Run("should update on force check", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0)
		if !s.ShouldUpdate("1.0", true) {
			t.Error("Should update on force check")
		}
	})

	t.Run("should schedule with exact interval when no randomization", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0)
		start := time.Now()
		s.SetNextUpdate()
		next := s.NextUpdate()

		expectedTime := start.Add(24 * time.Hour)
		diff := next.Sub(expectedTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Next update should be ~24 hours from now, got diff of %v", diff)
		}
	})

	t.Run("should schedule within randomization window", func(t *testing.T) {
		// Set deterministic random source
		restore := setTestRandSource(3) // Use 3 as a fixed random value
		defer restore()

		checkTime := 24
		randomizeTime := 6
		s := NewIntervalScheduler(checkTime, randomizeTime)
		start := time.Now()
		s.SetNextUpdate()
		next := s.NextUpdate()

		// With our fixed random value of 3, we expect the next update to be at checkTime + 3 hours
		expectedTime := start.Add(time.Duration(checkTime+3) * time.Hour)
		diff := next.Sub(expectedTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Next update should be at expected time, got diff of %v", diff)
		}
	})

	t.Run("should not update before scheduled time", func(t *testing.T) {
		restore := setTestRandSource(0) // Use 0 to get predictable timing
		defer restore()

		s := NewIntervalScheduler(24, 0)
		start := time.Now()
		s.SetNextUpdate()

		if !s.NextUpdate().After(start) {
			t.Error("Next update should be after start time")
		}

		if s.ShouldUpdate("1.0", false) {
			t.Error("Should not update before scheduled time")
		}
	})
}

