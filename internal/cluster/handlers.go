package cluster

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vuek8/internal/config"
)

func HandleListClusters(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mgr.ListClusters())
	}
}

func HandleSwitchCluster(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := mgr.SwitchTo(req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mgr.ListClusters())
	}
}

func HandleRenameCluster(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := mgr.Rename(req.ID, req.DisplayName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func HandleSetIcon(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID   string `json:"id"`
			Icon string `json:"icon"` // base64 data URL or empty to clear
			URL  string `json:"url"`  // URL to fetch icon from
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		icon := req.Icon

		// If URL provided, fetch it server-side
		if req.URL != "" && icon == "" {
			fetched, err := fetchIconFromURL(req.URL)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			icon = fetched
		}

		if err := mgr.SetIcon(req.ID, icon); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func fetchIconFromURL(rawURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// If it looks like a direct image URL, fetch it
	if strings.HasSuffix(strings.ToLower(rawURL), ".png") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".jpg") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".jpeg") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".ico") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".svg") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".gif") ||
		strings.HasSuffix(strings.ToLower(rawURL), ".webp") {
		if !strings.HasPrefix(rawURL, "http") {
			rawURL = "https://" + rawURL
		}
		return fetchImageAsDataURL(client, rawURL)
	}

	// Otherwise treat as website — fetch favicon via Google
	domain := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		domain = u.Host
	} else if !strings.Contains(rawURL, "/") {
		domain = rawURL
	}
	faviconURL := fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=128", url.QueryEscape(domain))
	return fetchImageAsDataURL(client, faviconURL)
}

func fetchImageAsDataURL(client *http.Client, imgURL string) (string, error) {
	resp, err := client.Get(imgURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d fetching icon", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024)) // max 512KB
	if err != nil {
		return "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func HandleFetchIcon() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		icon, err := fetchIconFromURL(req.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"icon": icon})
	}
}

func HandleHideCluster(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     string `json:"id"`
			Hidden bool   `json:"hidden"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := mgr.SetHidden(req.ID, req.Hidden); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func HandleGetSettings(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mgr.GetSettings())
	}
}

func HandleUpdateSettings(mgr *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s config.Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := mgr.UpdateSettings(s); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)
	}
}
