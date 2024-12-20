package selfupdate

import (
	"bytes"
	"io"
	"runtime"
	"testing"
	"time"
)

func TestUpdaterFetch(t *testing.T) {
	t.Run("must return non-nil ReadCloser", func(t *testing.T) {
		mr := &mockRequester{}
		mr.handleRequest(
			func(url string) (io.ReadCloser, error) {
				return nil, nil
			})
		updater := createUpdater(mr)
		updater.CheckTime = 24
		updater.RandomizeTime = 24

		err := updater.BackgroundRun()
		if err == nil {
			t.Error("Expected an error, got nil")
		} else if err.Error() != "Fetch was expected to return non-nil ReadCloser" {
			t.Errorf("Unexpected error: %v", err)
		}
	})
}

func TestUpdaterWithEmptyPayload(t *testing.T) {
	t.Run("no error no update", func(t *testing.T) {
		mr := &mockRequester{}
		mr.handleRequest(
			func(url string) (io.ReadCloser, error) {
				expectedURL := getExpectedURL()
				if url != expectedURL {
					t.Errorf("unexpected URL: got %s, want %s", url, expectedURL)
				}
				return newTestReaderCloser("{}"), nil
			})
		updater := createUpdater(mr)
		updater.CheckTime = 24
		updater.RandomizeTime = 24

		if err := updater.BackgroundRun(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestUpdaterCheckTime(t *testing.T) {
	tests := []struct {
		name          string
		checkTime     int
		randomizeTime int
		expectUpdate  bool
	}{
		//	{"zero times", 0, 0, false},
		{"zero check with random", 0, 5, true},
		{"check without random", 1, 0, true},
		{"both times set", 100, 100, true},
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
			updater.ClearUpdateState()
			updater.CheckTime = tt.checkTime
			updater.RandomizeTime = tt.randomizeTime
			updater.ForceCheck = false
			updater.Info.Sha256 = []byte("Q2vvTOW0p69A37StVANN+/ko1ZQDTElomq7fVcex/02=")
			updater.BackgroundRun()
			if got := updater.WantUpdate(); got != tt.expectUpdate {
				t.Errorf("WantUpdate() = %v, want %v", got, tt.expectUpdate)
			}

			maxHrs := time.Duration(updater.CheckTime+updater.RandomizeTime) * time.Hour
			maxTime := time.Now().Add(maxHrs)

			if !updater.NextUpdate().Before(maxTime) {
				t.Errorf("NextUpdate should be less than %s from now; got %s", maxHrs, updater.NextUpdate())
			}

			if maxHrs > 0 && !updater.NextUpdate().After(time.Now()) {
				t.Error("NextUpdate should be after current time")
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

	err := updater.BackgroundRun()
	if err != nil {
		t.Errorf("Error occurred: %#v", err)
	}
}

func TestUpdateAvailable(t *testing.T) {
	mr := &mockRequester{}
	mr.handleRequest(
		func(url string) (io.ReadCloser, error) {
			expectedURL := getExpectedURL()
			equals(t, expectedURL, url)
			return newTestReaderCloser(`{
    "Version": "2023-07-09-66c6c12",
    "Sha256": "Q2vvTOW0p69A37StVANN+/ko1ZQDTElomq7fVcex/02="
}`), nil
		})
	updater := createUpdater(mr)

	version, err := updater.UpdateAvailable()
	if err != nil {
		t.Errorf("Error occurred: %#v", err)
	}
	equals(t, "2023-07-09-66c6c12", version)
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
