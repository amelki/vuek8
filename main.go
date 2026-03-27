package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"vuek8/internal/cluster"
	"vuek8/internal/demo"
	"vuek8/internal/telemetry"
	"vuek8/internal/web"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "path to kubeconfig file (default: auto-discover from ~/.kube/)")
	browserMode := flag.Bool("browser", false, "open in browser instead of native window")
	demoMode := flag.Bool("demo", false, "run with sample data (no real cluster needed)")
	demoRollout := flag.Bool("demo-rollout", false, "simulate a rollout in demo mode")
	noTelemetry := flag.Bool("no-telemetry", false, "disable anonymous usage telemetry")
	flag.Parse()

	var srv *http.Server
	if *demoMode {
		srv = demo.NewServer(*demoRollout)
	} else {
		mgr, err := cluster.NewManager(*kubeconfig)
		if err != nil {
			log.Fatalf("Failed to initialize: %v", err)
		}

		if !*noTelemetry {
			installID := telemetry.EnsureInstallID(mgr.Config())
			telemetry.Ping(installID)
		}

		srv = web.NewServer(mgr)
	}

	// Always start the HTTP server (needed for SSE streaming in native mode)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	apiURL := fmt.Sprintf("http://%s", listener.Addr().String())
	fmt.Printf("vuek8 API at %s\n", apiURL)
	go srv.Serve(listener)

	if *browserMode {
		fmt.Printf("vuek8 running at %s\n", apiURL)
		go openBrowser(apiURL)
		select {} // block forever
	} else {
		runNativeWindow(srv, apiURL)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}
