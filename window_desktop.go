//go:build darwin || windows || linux

package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// proxyHandler serves static files from the HTTP server and proxies API calls to it.
// This ensures SSE streaming works properly in the native app.
type proxyHandler struct {
	proxy *httputil.ReverseProxy
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}

func runNativeWindow(srv *http.Server, apiURL string) {
	target, _ := url.Parse(apiURL)
	proxy := httputil.NewSingleHostReverseProxy(target)

	err := wails.Run(&options.App{
		Title:  "KGlance",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Handler: &proxyHandler{proxy: proxy},
		},
	})
	if err != nil {
		log.Fatalf("Wails error: %v", err)
	}
}
