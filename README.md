# Vue.k8

A fast, lightweight Kubernetes dashboard. A minimal alternative to Lens.

vuek8 auto-discovers your kubeconfig files, connects to your clusters, and gives you a real-time view of your nodes and pods — without the bloat.

![Vue.k8 screenshot](website/screenshot.png)

## Download

| Platform | Download |
|----------|----------|
| **macOS (Apple Silicon)** | [Vue.k8-0.4.8.dmg](https://github.com/amelki/vuek8/releases/latest/download/Vue.k8-0.4.8.dmg) |
| **macOS (Intel)** | [vuek8-0.4.8-macos-amd64](https://github.com/amelki/vuek8/releases/latest/download/vuek8-0.4.8-macos-amd64) |
| **Linux** | [vuek8-0.4.8-linux-amd64](https://github.com/amelki/vuek8/releases/latest/download/vuek8-0.4.8-linux-amd64) |
| **Windows** | [vuek8-0.4.8-windows-amd64.exe](https://github.com/amelki/vuek8/releases/latest/download/vuek8-0.4.8-windows-amd64.exe) |

> macOS Apple Silicon: mount the DMG, drag to Applications. All others: run with `--browser` flag.

## Features

- **Topology view** — Visual map of your cluster: nodes grouped by pool, pods as colored dots (green = running, red = error, yellow = pending). Hover for details, click to inspect.
- **List view** — Pods in a sortable table with resizable columns. Group by node, workload, or both. Filter by namespace, workload, or text search.
- **Multi-cluster** — Auto-discovers all kubeconfigs in `~/.kube/`. Switch clusters from the sidebar. Rename and hide clusters you don't need.
- **Real-time** — Background cache refreshes every 3 seconds. Watch rollouts happen live.
- **Container details** — See per-container status, image, and tag. Click any pod to open the detail panel.
- **Native desktop app** — Runs as a macOS app (via Wails/WebKit). No Electron, no Chrome. 12MB DMG.
- **Browser mode** — Also works in any browser for development or headless environments.
- **Fast** — API responses in ~13ms from server-side cache. No lag, no spinners.

## Quick Start

### From source

```bash
# Clone
git clone https://github.com/amelki/vuek8.git
cd vuek8

# Run in browser (development)
make dev

# Or build the native macOS app
make dmg
open dist/Vue.k8.app
```

### Requirements

- Go 1.21+ (use `conda create -n vuek8 go -c conda-forge` if needed)
- `~/.kube/config` or any kubeconfig files in `~/.kube/`
- macOS, Linux, or Windows

## Usage

```bash
# Auto-discover kubeconfigs, open in browser
vuek8 --browser

# Use a specific kubeconfig
vuek8 --browser --kubeconfig ~/.kube/my-cluster.yaml

# Native desktop app (default when built with make build)
vuek8
```

## Views

### Topology

Nodes are displayed as cards grouped by pool (inferred from naming conventions). Each pod is a colored square inside its node. Hover to see pod details, click to open the detail panel.

### List

A table view with columns: Namespace, Name, Containers, Status, Ready, Restarts, Age, Tag. Columns are resizable by dragging. Group by:

- **Flat** — All pods in a single list
- **Node** — Pods grouped under their node
- **Workload** — Pods grouped by Deployment/StatefulSet/DaemonSet/Job
- **Node / Workload** — Two-level grouping
- **Workload / Node** — Two-level grouping

## Settings

Click the gear icon to access settings:

- **Show all contexts** — By default, only the default context from each kubeconfig is shown. Enable this to see all contexts (control planes, etc.)

Cluster display names and visibility are managed from the sidebar (right-click menu).

## Build

```bash
make dev        # Development: fast build + browser
make build      # Native desktop binary (requires CGO)
make app        # macOS .app bundle
make dmg        # macOS .dmg installer
make clean      # Clean build artifacts
```

## Architecture

- **Go backend** — Uses `client-go` to talk to Kubernetes. Server-side cache refreshes every 3s in the background.
- **Vanilla frontend** — Plain HTML, CSS, and JavaScript. No React, no npm, no build step.
- **Wails** — Native desktop window via macOS WebKit. No Electron.
- **Single binary** — Static files embedded with `go:embed`. Everything ships as one file.

## Telemetry

Vue.k8 sends an anonymous ping on startup to help us understand usage. This includes:

- A random install ID (UUID, not tied to you)
- App version
- OS and architecture (e.g. `darwin`, `arm64`)

**No cluster data, no personal information, no IP tracking.**

To opt out, run with `--no-telemetry`:

```bash
vuek8 --no-telemetry
```

## License

[Business Source License 1.1](LICENSE) — free for personal and non-commercial use. Converts to Apache 2.0 on 2030-03-22.
