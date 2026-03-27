package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var Version = "dev" // set at build time via -ldflags

const (
	GitHubRepo       = "amelki/vuek8"
	GitHubReleasesURL = "https://github.com/" + GitHubRepo + "/releases"
)

// BaseURL for the telemetry API (API Gateway). Only used for /api/ping.
var BaseURL = "https://qyxgzswgwc.execute-api.eu-west-3.amazonaws.com"

type ghRelease struct {
	TagName string `json:"tag_name"`
}

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	UpdateURL string `json:"updateUrl,omitempty"`
	HasUpdate bool   `json:"hasUpdate"`
}

func Check() UpdateInfo {
	info := UpdateInfo{Current: Version}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + GitHubRepo + "/releases/latest")
	if err != nil {
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return info
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return info
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	info.Latest = latest
	info.UpdateURL = GitHubReleasesURL + "/tag/" + release.TagName
	info.HasUpdate = latest != current && current != "dev"

	return info
}

func HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Check())
}

// HandleSelfUpdate downloads the latest DMG, mounts it, and replaces the current app.
func HandleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "darwin" {
		http.Error(w, "auto-update only supported on macOS", http.StatusBadRequest)
		return
	}

	info := Check()
	if !info.HasUpdate {
		http.Error(w, "no update available", http.StatusBadRequest)
		return
	}

	// Find current app path — resolve symlinks
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)
	// exe is like /Applications/Vue.k8.app/Contents/MacOS/vuek8
	appPath := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // → /Applications/Vue.k8.app
	if !strings.HasSuffix(appPath, ".app") {
		http.Error(w, "not running from a .app bundle", http.StatusBadRequest)
		return
	}
	appDir := filepath.Dir(appPath) // → /Applications

	dmgName := fmt.Sprintf("Vue.k8-%s.dmg", info.Latest)
	dmgURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", GitHubRepo, info.Latest, dmgName)

	// Download DMG
	tmpDMG := filepath.Join(os.TempDir(), dmgName)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(dmgURL)
	if err != nil {
		http.Error(w, "download failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		http.Error(w, fmt.Sprintf("download failed: HTTP %d", resp.StatusCode), http.StatusInternalServerError)
		return
	}
	f, err := os.Create(tmpDMG)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f.Close()

	// Mount DMG — without -quiet so we get the mount point
	mountOut, err := exec.Command("hdiutil", "attach", tmpDMG, "-nobrowse").CombinedOutput()
	if err != nil {
		http.Error(w, "mount failed: "+err.Error()+": "+string(mountOut), http.StatusInternalServerError)
		return
	}
	// Find mount point from output (last line, last field group)
	mountPoint := ""
	for _, line := range strings.Split(string(mountOut), "\n") {
		if strings.Contains(line, "/Volumes/") {
			idx := strings.Index(line, "/Volumes/")
			mountPoint = strings.TrimSpace(line[idx:])
		}
	}
	if mountPoint == "" {
		http.Error(w, "could not determine mount point", http.StatusInternalServerError)
		return
	}
	defer exec.Command("hdiutil", "detach", mountPoint, "-quiet", "-force").Run()

	// Find the .app in the mounted DMG
	srcApp := ""
	entries, _ := os.ReadDir(mountPoint)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			srcApp = filepath.Join(mountPoint, e.Name())
			break
		}
	}
	if srcApp == "" {
		http.Error(w, "no .app found in DMG", http.StatusInternalServerError)
		return
	}

	// Remove old app and copy new one to the same directory
	newAppPath := filepath.Join(appDir, filepath.Base(srcApp))
	// If the name changed (e.g. VueK8.app → Vue.k8.app), remove old one too
	if newAppPath != appPath {
		os.RemoveAll(appPath)
	}
	os.RemoveAll(newAppPath)
	if out, err := exec.Command("cp", "-R", srcApp, newAppPath).CombinedOutput(); err != nil {
		http.Error(w, "failed to copy new app: "+err.Error()+": "+string(out), http.StatusInternalServerError)
		return
	}
	// Ensure the copy is flushed to disk
	exec.Command("sync").Run()

	// Clean up
	os.Remove(tmpDMG)

	// Store the new app path for restart
	lastUpdatedAppPath = newAppPath

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

var lastUpdatedAppPath string

// HandleRestart relaunches the app.
func HandleRestart(w http.ResponseWriter, r *http.Request) {
	appPath := lastUpdatedAppPath
	if appPath == "" {
		exe, err := os.Executable()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		appPath = filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})

	// Launch new app and quit current
	go func() {
		time.Sleep(500 * time.Millisecond)
		// Use 'open' which returns after the app is launched
		cmd := exec.Command("open", "-n", "-a", appPath)
		cmd.Run() // wait for open to finish launching
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}
