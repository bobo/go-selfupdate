package selfupdate

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"
	"time"
)

func TestUpdaterSchedulers(t *testing.T) {
	tests := []struct {
		name         string
		scheduler    UpdateScheduler
		expectUpdate bool
	}{
		{"daily scheduler", NewDailyScheduler((time.Now().Hour()+1)%24, getExecRelativeDir("update/"+timeFile)), true},
		{"interval no random", NewIntervalScheduler(1, 0, getExecRelativeDir("update/"+timeFile)), true},
		{"interval with random", NewIntervalScheduler(100, 100, getExecRelativeDir("update/"+timeFile)), true},
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
			updater.BackgroundRun(context.Background())

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

			// For daily scheduler, check that it's within 24 hours
			if ds, ok := tt.scheduler.(*DailyScheduler); ok {
				maxTime := time.Now().Add(24 * time.Hour)
				if !nextUpdate.Before(maxTime) {
					t.Errorf("NextUpdate should be less than 24 hours from now; got %s", nextUpdate)
				}

				// Check that the hour matches
				if nextUpdate.Hour() != ds.hour {
					t.Errorf("NextUpdate hour should be %d; got %d", ds.hour, nextUpdate.Hour())
				}
			}
		})
	}
}

func TestUpdaterWithEmptyPayloadNoErrorNoUpdateEscapedPath(t *testing.T) {
	mr := &mockRequester{}
	mr.handleRequest(
		func(url string) (io.ReadCloser, error) {
			expectedURL := getExpectedURL()
			equals(t, expectedURL, url)
			return newTestReaderCloser("{}"), nil
		})
	updater := createUpdaterWithEscapedCharacters(mr)
	updater.Scheduler = NewIntervalScheduler(24, 0, getExecRelativeDir("update/"+timeFile))

	err := updater.BackgroundRun(context.Background())
	if err != nil {
		t.Errorf("Error occurred: %#v", err)
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
	"Channel": "stable",
	"Date": "2023-07-09T00:00:00Z"
}`), nil
		})
	updater := createUpdater(mr)
	updater.Scheduler = NewIntervalScheduler(24, 0, getExecRelativeDir("update/"+timeFile))

	err := updater.fetchInfo()
	if err != nil {
		t.Errorf("Error occurred: %#v", err)
	}
	equals(t, "2023-07-09-66c6c12", updater.Info.Version)
	equals(t, "stable", updater.Info.Channel)
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

func createUpdaterWithEscapedCharacters(mr *mockRequester) *Updater {
	return &Updater{
		CurrentVersion: "1.2+foobar",
		ApiURL:         "http://updates.yourdomain.com/",
		BinURL:         "http://updates.yourdownmain.com/",
		DiffURL:        "http://updates.yourdomain.com/",
		Dir:            "update/",
		CmdName:        "myapp+foo", // app name
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

func TestDailyScheduler(t *testing.T) {
	tempFile := t.TempDir() + "/cktime"

	t.Run("should skip dev version", func(t *testing.T) {
		s := NewDailyScheduler(3, tempFile)
		if s.ShouldUpdate("dev", false) {
			t.Error("Should skip dev version")
		}
	})

	t.Run("should update on force check", func(t *testing.T) {
		s := NewDailyScheduler(3, tempFile)
		if !s.ShouldUpdate("1.0", true) {
			t.Error("Should update on force check")
		}
	})

	t.Run("should schedule for next day if current hour passed", func(t *testing.T) {
		currentHour := time.Now().Hour()
		s := NewDailyScheduler(currentHour-1, tempFile)
		s.SetNextUpdate()
		next := s.NextUpdate()

		if next.Day() != time.Now().Add(24*time.Hour).Day() {
			t.Error("Should schedule for next day")
		}
		if next.Hour() != currentHour-1 {
			t.Errorf("Should maintain scheduled hour, got %d want %d", next.Hour(), currentHour-1)
		}
	})

	t.Run("should schedule for today if hour not passed", func(t *testing.T) {
		currentHour := time.Now().Hour()
		s := NewDailyScheduler((currentHour+1)%24, tempFile)
		s.SetNextUpdate()
		next := s.NextUpdate()

		if next.Day() != time.Now().Day() {
			t.Error("Should schedule for today")
		}
		if next.Hour() != (currentHour+1)%24 {
			t.Errorf("Should maintain scheduled hour, got %d want %d", next.Hour(), (currentHour+1)%24)
		}
	})
}

func TestIntervalScheduler(t *testing.T) {
	tempFile := t.TempDir() + "/cktime"

	t.Run("should skip dev version", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0, tempFile)
		if s.ShouldUpdate("dev", false) {
			t.Error("Should skip dev version")
		}
	})

	t.Run("should update on force check", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0, tempFile)
		if !s.ShouldUpdate("1.0", true) {
			t.Error("Should update on force check")
		}
	})

	t.Run("should schedule with exact interval when no randomization", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0, tempFile)
		now := time.Now()
		s.SetNextUpdate()
		next := s.NextUpdate()

		expectedTime := now.Add(24 * time.Hour)
		diff := next.Sub(expectedTime)
		if diff < -time.Minute || diff > time.Minute {
			t.Errorf("Next update should be ~24 hours from now, got diff of %v", diff)
		}
	})

	t.Run("should schedule within randomization window", func(t *testing.T) {
		checkTime := 24
		randomizeTime := 6
		s := NewIntervalScheduler(checkTime, randomizeTime, tempFile)
		now := time.Now()
		s.SetNextUpdate()
		next := s.NextUpdate()

		minExpected := now.Add(time.Duration(checkTime) * time.Hour)
		maxExpected := now.Add(time.Duration(checkTime+randomizeTime) * time.Hour)

		if next.Before(minExpected) {
			t.Errorf("Next update too early, got %v, want after %v", next, minExpected)
		}
		if next.After(maxExpected) {
			t.Errorf("Next update too late, got %v, want before %v", next, maxExpected)
		}
	})

	t.Run("should not update before scheduled time", func(t *testing.T) {
		s := NewIntervalScheduler(24, 0, tempFile)
		s.SetNextUpdate()
		if s.ShouldUpdate("1.0", false) {
			t.Error("Should not update before scheduled time")
		}
	})
}
