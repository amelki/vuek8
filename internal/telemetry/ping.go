package telemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"vuek8/internal/config"
	"vuek8/internal/update"
)

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// EnsureInstallID returns the install ID from config, generating one if needed.
// On first launch, prints a telemetry notice.
func EnsureInstallID(cfg *config.Config) string {
	if cfg.InstallID != "" {
		return cfg.InstallID
	}
	cfg.InstallID = generateUUID()
	_ = cfg.Save()
	fmt.Println("Telemetry: Vue.k8 sends anonymous usage data (install ID, version, OS, arch).")
	fmt.Println("           No cluster data or personal info is collected.")
	fmt.Println("           To disable: run with --no-telemetry")
	return cfg.InstallID
}

type pingPayload struct {
	InstallID string `json:"installId"`
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Ping sends an anonymous install ping. Fire-and-forget, non-blocking.
func Ping(installID string) {
	go func() {
		payload := pingPayload{
			InstallID: installID,
			Version:   update.Version,
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
		}
		body, _ := json.Marshal(payload)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(update.BaseURL+"/api/ping", "application/json", bytes.NewReader(body))
		if err != nil {
			return
		}
		resp.Body.Close()
	}()
}
