//go:build darwin || windows || linux

package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"vuek8/internal/update"
)

type proxyHandler struct {
	proxy *httputil.ReverseProxy
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}

var appCtx context.Context

func runNativeWindow(srv *http.Server, apiURL string) {
	target, _ := url.Parse(apiURL)
	proxy := httputil.NewSingleHostReverseProxy(target)

	appMenu := menu.NewMenu()

	// App menu (macOS) — custom with About
	kglanceMenu := appMenu.AddSubmenu("Vue.k8")
	kglanceMenu.AddText("About Vue.k8", nil, func(_ *menu.CallbackData) {
		if appCtx != nil {
			wailsruntime.MessageDialog(appCtx, wailsruntime.MessageDialogOptions{
				Type:    wailsruntime.InfoDialog,
				Title:   "About Vue.k8",
				Message: "Vue.k8 v" + update.Version + "\n\nA fast, lightweight Kubernetes dashboard.\nhttps://vuek8.app",
			})
		}
	})
	kglanceMenu.AddSeparator()
	kglanceMenu.AddText("Quit Vue.k8", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		wailsruntime.Quit(appCtx)
	})

	// File menu
	fileMenu := appMenu.AddSubmenu("File")
	fileMenu.AddText("Close Window", keys.CmdOrCtrl("w"), func(_ *menu.CallbackData) {})

	// Edit menu (for copy/paste support)
	appMenu.Append(menu.EditMenu())

	err := wails.Run(&options.App{
		Title:  "Vue.k8",
		Width:  1400,
		Height: 900,
		Menu:   appMenu,
		OnStartup: func(ctx context.Context) {
			appCtx = ctx
		},
		AssetServer: &assetserver.Options{
			Handler: &proxyHandler{proxy: proxy},
		},
	})
	if err != nil {
		log.Fatalf("Wails error: %v", err)
	}
}
