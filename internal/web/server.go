package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"vuek8/internal/cluster"
	"vuek8/internal/kube"
	"vuek8/internal/update"
)

//go:embed static
var StaticFiles embed.FS

var DevMode bool

func NewServer(mgr *cluster.Manager) *http.Server {
	mux := http.NewServeMux()

	getCache := func() *kube.Cache { return mgr.GetCache() }

	// Data API — served from cache
	mux.HandleFunc("/api/namespaces", kube.HandleCachedNamespaces(getCache))
	mux.HandleFunc("/api/nodes", kube.HandleCachedNodes(getCache))
	mux.HandleFunc("/api/pods", kube.HandleCachedPods(getCache))
	mux.HandleFunc("/api/progress", kube.HandleProgress(getCache))
	mux.HandleFunc("/api/metrics", kube.HandleCachedMetrics(getCache))

	// Cluster management API
	mux.HandleFunc("/api/clusters", cluster.HandleListClusters(mgr))
	mux.HandleFunc("/api/clusters/switch", cluster.HandleSwitchCluster(mgr))
	mux.HandleFunc("/api/clusters/rename", cluster.HandleRenameCluster(mgr))
	mux.HandleFunc("/api/clusters/hide", cluster.HandleHideCluster(mgr))
	mux.HandleFunc("/api/settings", cluster.HandleGetSettings(mgr))
	mux.HandleFunc("/api/settings/update", cluster.HandleUpdateSettings(mgr))

	// Logs
	mux.HandleFunc("/api/logs", kube.HandleLogs(getCache))
	mux.HandleFunc("/api/logs/stream", kube.HandleLogsStream(getCache))
	mux.HandleFunc("/api/logs/download", kube.HandleLogsDownload(getCache))

	// Terminal
	mux.HandleFunc("/api/terminal/logs", kube.HandleOpenTerminal("logs"))
	mux.HandleFunc("/api/terminal/exec", kube.HandleOpenTerminal("exec"))

	// Version / update check
	mux.HandleFunc("/api/version", update.HandleVersion)

	// Static files — from disk in dev mode, embedded otherwise
	if DevMode {
		mux.Handle("/", http.FileServer(http.Dir("internal/web/static")))
	} else {
		staticFS, _ := fs.Sub(StaticFiles, "static")
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}

	return &http.Server{Handler: mux}
}

func init() {
	if _, err := os.Stat("internal/web/static"); err == nil {
		DevMode = true
	}
}
