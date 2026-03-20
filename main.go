package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"

	"kglance/internal/cluster"
	"kglance/internal/web"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "path to kubeconfig file (default: auto-discover from ~/.kube/)")
	browserMode := flag.Bool("browser", false, "open in browser instead of native window")
	flag.Parse()

	mgr, err := cluster.NewManager(*kubeconfig)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}

	srv := web.NewServer(mgr)

	if *browserMode {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatalf("Failed to listen: %v", err)
		}
		url := fmt.Sprintf("http://%s", listener.Addr().String())
		fmt.Printf("kubeui running at %s\n", url)
		go openBrowser(url)
		log.Fatal(srv.Serve(listener))
	} else {
		runNativeWindow(srv)
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
