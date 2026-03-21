package kube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
)

type TerminalRequest struct {
	Namespace      string `json:"namespace"`
	Pod            string `json:"pod"`
	Container      string `json:"container"`
	KubeconfigPath string `json:"kubeconfigPath"`
	ContextName    string `json:"contextName"`
}

func HandleOpenTerminal(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req TerminalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var cmd string
		kubectlBase := fmt.Sprintf("kubectl --kubeconfig %s --context %s", req.KubeconfigPath, req.ContextName)

		switch action {
		case "logs":
			cmd = fmt.Sprintf("%s logs -f --tail=100 %s -n %s", kubectlBase, req.Pod, req.Namespace)
			if req.Container != "" {
				cmd += fmt.Sprintf(" -c %s", req.Container)
			}
		case "exec":
			cmd = fmt.Sprintf("%s exec -it %s -n %s", kubectlBase, req.Pod, req.Namespace)
			if req.Container != "" {
				cmd += fmt.Sprintf(" -c %s", req.Container)
			}
			cmd += " -- sh -c 'if command -v bash >/dev/null; then bash; else sh; fi'"
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}

		dryRun := r.URL.Query().Get("dryRun") == "true"
		if !dryRun {
			if err := openTerminal(cmd); err != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"error":   err.Error(),
					"command": cmd,
				})
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "command": cmd})
	}
}

func openTerminal(cmd string) error {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal"
			activate
			do script "%s"
		end tell`, cmd)
		return exec.Command("osascript", "-e", script).Start()
	case "linux":
		// Try common terminal emulators
		for _, term := range []string{"gnome-terminal", "xterm", "konsole"} {
			if path, err := exec.LookPath(term); err == nil {
				if term == "gnome-terminal" {
					return exec.Command(path, "--", "bash", "-c", cmd+"; exec bash").Start()
				}
				return exec.Command(path, "-e", "bash", "-c", cmd+"; exec bash").Start()
			}
		}
		return fmt.Errorf("no terminal emulator found")
	case "windows":
		return exec.Command("cmd", "/c", "start", "cmd", "/k", cmd).Start()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}
