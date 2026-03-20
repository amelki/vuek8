package cluster

import (
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type DiscoveredCluster struct {
	ID             string // kubeconfigPath#contextName
	KubeconfigPath string
	ContextName    string
	ClusterServer  string
	IsDefault      bool // true if this is the file's current-context
}

func Discover() []DiscoveredCluster {
	kubeDir := filepath.Join(homedir.HomeDir(), ".kube")
	entries, err := os.ReadDir(kubeDir)
	if err != nil {
		return nil
	}

	var results []DiscoveredCluster
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".old") {
			continue
		}

		path := filepath.Join(kubeDir, name)
		clusters := parseKubeconfig(path)
		results = append(results, clusters...)
	}
	return results
}

func parseKubeconfig(path string) []DiscoveredCluster {
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil
	}

	var results []DiscoveredCluster
	for ctxName, ctx := range config.Contexts {
		server := ""
		if cl, ok := config.Clusters[ctx.Cluster]; ok {
			server = cl.Server
		}
		results = append(results, DiscoveredCluster{
			ID:             path + "#" + ctxName,
			KubeconfigPath: path,
			ContextName:    ctxName,
			ClusterServer:  server,
			IsDefault:      ctxName == config.CurrentContext,
		})
	}
	return results
}
