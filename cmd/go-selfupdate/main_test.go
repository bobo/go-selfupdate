package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "selfupdate-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name           string
		channel        string
		expectedSubdir string
	}{
		{
			name:           "stable channel",
			channel:        "stable",
			expectedSubdir: "",
		},
		{
			name:           "beta channel",
			channel:        "beta",
			expectedSubdir: "beta",
		},
		{
			name:           "custom channel",
			channel:        "custom",
			expectedSubdir: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			genDir = tmpDir
			if tt.channel != "stable" {
				genDir = filepath.Join(tmpDir, tt.channel)
			}

			expectedPath := filepath.Join(tmpDir, tt.expectedSubdir)
			if genDir != expectedPath {
				t.Errorf("Expected output dir %s, got %s", expectedPath, genDir)
			}
		})
	}
}
