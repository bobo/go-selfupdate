package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// verifyHash checks if a binary matches the expected SHA256 hash
func verifyHash(bin []byte, expectedHash []byte) bool {
	h := sha256.New()
	h.Write(bin)
	return bytes.Equal(h.Sum(nil), expectedHash)
}

// getExecRelativeDir returns a path relative to the executable
func getExecRelativeDir(dir string) string {
	filename, _ := os.Executable()
	return filepath.Join(filepath.Dir(filename), dir)
}

// canUpdate checks if the binary can be updated by attempting to create a test file
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
