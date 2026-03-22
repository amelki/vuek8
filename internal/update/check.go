package update

import (
	"encoding/json"
	"net/http"
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
