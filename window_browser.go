//go:build !darwin && !windows && !linux

package main

import (
	"net/http"
)

func runNativeWindow(srv *http.Server, apiURL string) {
	// No native window — browser mode is handled in main.go
	select {}
}
