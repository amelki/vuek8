//go:build !darwin && !windows && !linux

package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

func runNativeWindow(srv *http.Server) {
	// No native window support — fall back to browser
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", listener.Addr().String())
	fmt.Printf("kubeui running at %s\n", url)
	go openBrowser(url)
	log.Fatal(srv.Serve(listener))
}
