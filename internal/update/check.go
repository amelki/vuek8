package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var Version = "dev" // set at build time via -ldflags

const repo = "amelki/vuek8"

type ReleaseInfo struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	UpdateURL   string `json:"updateUrl,omitempty"`
	HasUpdate   bool   `json:"hasUpdate"`
}

func Check() UpdateInfo {
	info := UpdateInfo{Current: Version}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	if err != nil {
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return info
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return info
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	info.Latest = latest
	info.UpdateURL = release.HTMLURL
	info.HasUpdate = latest != current && current != "dev"

	return info
}

func HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Check())
}
