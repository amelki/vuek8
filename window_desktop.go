//go:build darwin || windows || linux

package main

import (
	"log"
	"net/http"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func runNativeWindow(srv *http.Server) {
	err := wails.Run(&options.App{
		Title:  "KubeUI",
		Width:  1400,
		Height: 900,
		AssetServer: &assetserver.Options{
			Handler: srv.Handler,
		},
	})
	if err != nil {
		log.Fatalf("Wails error: %v", err)
	}
}
