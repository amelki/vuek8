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

	// Find current app path
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// exe is like /Applications/Vue.k8.app/Contents/MacOS/vuek8
	appPath := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // → /Applications/Vue.k8.app
	if !strings.HasSuffix(appPath, ".app") {
		http.Error(w, "not running from a .app bundle", http.StatusBadRequest)
		return
	}

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

	// Mount DMG
	mountOut, err := exec.Command("hdiutil", "attach", tmpDMG, "-nobrowse", "-quiet").Output()
	if err != nil {
		http.Error(w, "mount failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Find mount point
	mountPoint := ""
	for _, line := range strings.Split(string(mountOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			mountPoint = strings.Join(fields[2:], " ")
		}
	}
	if mountPoint == "" {
		mountPoint = "/Volumes/Vue.k8"
	}
	defer exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()

	// Replace app
	srcApp := filepath.Join(mountPoint, "Vue.k8.app")
	if _, err := os.Stat(srcApp); err != nil {
		http.Error(w, "Vue.k8.app not found in DMG", http.StatusInternalServerError)
		return
	}
	// Remove old app and copy new one
	if err := os.RemoveAll(appPath); err != nil {
		http.Error(w, "failed to remove old app: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := exec.Command("cp", "-R", srcApp, appPath).Run(); err != nil {
		http.Error(w, "failed to copy new app: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Clean up
	os.Remove(tmpDMG)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// HandleRestart relaunches the app.
func HandleRestart(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	appPath := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})

	// Launch new app and quit current
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("open", appPath).Start()
		os.Exit(0)
	}()
}
