package update

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var Version = "dev" // set at build time via -ldflags

// BaseURL is the root URL for the release infrastructure (S3/CloudFront).
// Override at build time via -ldflags if needed.
var BaseURL = "https://releases.vuek8.app"

type LatestRelease struct {
	Version  string `json:"version"`
	MacARM   string `json:"macArm"`
	MacIntel string `json:"macIntel"`
	Linux    string `json:"linux"`
	Windows  string `json:"windows"`
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
	resp, err := client.Get(BaseURL + "/latest.json")
	if err != nil {
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return info
	}

	var release LatestRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return info
	}

	latest := strings.TrimPrefix(release.Version, "v")
	current := strings.TrimPrefix(Version, "v")

	info.Latest = latest
	info.UpdateURL = BaseURL
	info.HasUpdate = latest != current && current != "dev"

	return info
}

func HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Check())
}
