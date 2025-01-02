package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var version, genDir string

type current struct {
	Version string
	Sha256  []byte
	Channel string
	Date    time.Time
}

func generateSha256(path string) []byte {
	h := sha256.New()
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
	}
	h.Write(b)
	sum := h.Sum(nil)
	return sum
	//return base64.URLEncoding.EncodeToString(sum)
}

func createUpdate(path string, platform string, channel string) {
	c := current{Version: version, Sha256: generateSha256(path), Channel: channel, Date: time.Now()}

	b, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		fmt.Println("error:", err)
	}
	err = os.WriteFile(filepath.Join(genDir, platform+".json"), b, 0755)
	if err != nil {
		panic(err)
	}

	os.MkdirAll(filepath.Join(genDir, version), 0755)

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	f, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	w.Write(f)
	w.Close() // You must close this first to flush the bytes to the buffer.
	err = os.WriteFile(filepath.Join(genDir, version, platform+".gz"), buf.Bytes(), 0755)

	if err != nil {
		panic(err)
	}

}

func printUsage() {
	fmt.Println("")
	fmt.Println("Positional arguments:")
	fmt.Println("\tSingle platform: go-selfupdate myapp channel 1.2")
	fmt.Println("\tCross platform: go-selfupdate /tmp/mybinares/ channel 1.2")
}

func createBuildDir() {
	os.MkdirAll(genDir, 0755)
}

func main() {
	outputDirFlag := flag.String("o", "public", "Output directory for writing updates")

	var defaultPlatform string
	goos := os.Getenv("GOOS")
	goarch := os.Getenv("GOARCH")
	if goos != "" && goarch != "" {
		defaultPlatform = goos + "-" + goarch
	} else {
		defaultPlatform = runtime.GOOS + "-" + runtime.GOARCH
	}
	platformFlag := flag.String("platform", defaultPlatform,
		"Target platform in the form OS-ARCH. Defaults to running os/arch or the combination of the environment variables GOOS and GOARCH if both are set.")

	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		printUsage()
		os.Exit(0)
	}

	platform := *platformFlag
	appPath := flag.Arg(0)
	channel := flag.Arg(1)
	version = flag.Arg(2)
	genDir = *outputDirFlag

	if channel != "stable" {
		genDir = filepath.Join(genDir, channel)
	}

	fmt.Println("platform", platform)
	fmt.Println("appPath", appPath)
	fmt.Println("channel", channel)
	fmt.Println("version", version)
	fmt.Println("genDir", genDir)
	createBuildDir()

	// If dir is given create update for each file
	fi, err := os.Stat(appPath)
	if err != nil {
		panic(err)
	}

	if fi.IsDir() {
		files, err := os.ReadDir(appPath)
		if err == nil {
			for _, file := range files {
				createUpdate(filepath.Join(appPath, file.Name()), file.Name(), channel)
			}
			os.Exit(0)
		}
	}

	createUpdate(appPath, platform, channel)
}
