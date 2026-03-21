package web

import (
	"embed"
	"io/fs"
	"net/http"

	"kglance/internal/cluster"
	"kglance/internal/kube"
	"kglance/internal/update"
)

//go:embed static
var staticFiles embed.FS

func NewServer(mgr *cluster.Manager) *http.Server {
	mux := http.NewServeMux()

	getCache := func() *kube.Cache { return mgr.GetCache() }

	// Data API — served from cache
	mux.HandleFunc("/api/namespaces", kube.HandleCachedNamespaces(getCache))
	mux.HandleFunc("/api/nodes", kube.HandleCachedNodes(getCache))
	mux.HandleFunc("/api/pods", kube.HandleCachedPods(getCache))
	mux.HandleFunc("/api/progress", kube.HandleProgress(getCache))

	// Cluster management API
	mux.HandleFunc("/api/clusters", cluster.HandleListClusters(mgr))
	mux.HandleFunc("/api/clusters/switch", cluster.HandleSwitchCluster(mgr))
	mux.HandleFunc("/api/clusters/rename", cluster.HandleRenameCluster(mgr))
	mux.HandleFunc("/api/clusters/hide", cluster.HandleHideCluster(mgr))
	mux.HandleFunc("/api/settings", cluster.HandleGetSettings(mgr))
	mux.HandleFunc("/api/settings/update", cluster.HandleUpdateSettings(mgr))

	// Version / update check
	mux.HandleFunc("/api/version", update.HandleVersion)

	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	return &http.Server{Handler: mux}
}
