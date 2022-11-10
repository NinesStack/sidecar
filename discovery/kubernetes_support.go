package discovery

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// K8sServices represents the payload that is returned by `kubectl get services -o json`
type K8sServices struct {
	APIVersion string `json:"apiVersion"`
	Items      []struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Annotations struct {
				KubectlKubernetesIoLastAppliedConfiguration string `json:"kubectl.kubernetes.io/last-applied-configuration"`
			} `json:"annotations"`
			CreationTimestamp time.Time `json:"creationTimestamp"`
			Labels            struct {
				Environment string `json:"Environment"`
				ServiceName string `json:"ServiceName"`
			} `json:"labels"`
			Name            string `json:"name"`
			Namespace       string `json:"namespace"`
			ResourceVersion string `json:"resourceVersion"`
			UID             string `json:"uid"`
		} `json:"metadata"`
		Spec struct {
			ClusterIP             string   `json:"clusterIP"`
			ClusterIPs            []string `json:"clusterIPs"`
			InternalTrafficPolicy string   `json:"internalTrafficPolicy"`
			IPFamilies            []string `json:"ipFamilies"`
			IPFamilyPolicy        string   `json:"ipFamilyPolicy"`
			Ports                 []struct {
				Port       int    `json:"port"`
				Protocol   string `json:"protocol"`
				TargetPort int    `json:"targetPort"`
				NodePort   int    `json:"nodePort"`
			} `json:"ports"`
			Selector struct {
				Environment string `json:"Environment"`
				ServiceName string `json:"ServiceName"`
			} `json:"selector"`
			SessionAffinity string `json:"sessionAffinity"`
			Type            string `json:"type"`
		} `json:"spec"`
		Status struct {
			LoadBalancer struct {
			} `json:"loadBalancer"`
		} `json:"status"`
	} `json:"items"`
	Kind     string `json:"kind"`
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
}

// K8sServices represents a cut-down version of the payload that is returned by
// `kubectl get services -o json`
type K8sNodes struct {
	APIVersion string    `json:"apiVersion"`
	Items      []K8sNode `json:"items"`
	Kind       string    `json:"kind"`
	Metadata   struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
}

// A K8sNode represents a single node from the K8sNodes wrapper structure
type K8sNode struct {
	Status struct {
		Addresses []K8sNodeAddress `json:"addresses"`
	} `json:"status"`
}

type K8sNodeAddress struct {
			Address string `json:"address"`
			Type    string `json:"type"`
}

// A K8sDiscoveryAdapter wraps a call to an external command that can be used
// to discover services running on a Kubernetes cluster. This is normally
// `kubectl` but for tests, this allows mocking out the underlying call.
type K8sDiscoveryAdapter interface {
	GetServices() ([]byte, error)
	GetNodes() ([]byte, error)
}

// KubectlDiscoveryCommand is the main implementation for K8sDiscoveryCommand
type KubectlDiscoveryCommand struct {
	Path      string
	Namespace string
	Timeout   time.Duration

	KubeHost string
	KubePort int
}

func (d *KubectlDiscoveryCommand) addVars(varlist []string) []string {
	addedEnvVars := []string{
		"KUBERNETES_SERVICE_HOST=" + d.KubeHost, fmt.Sprintf("KUBERNETES_SERVICE_PORT=%d", d.KubePort),
	}

	return append(varlist, addedEnvVars...)
}

func (d *KubectlDiscoveryCommand) GetServices() ([]byte, error) {
	// Run `kubectl` from the specific path, and namespace, and return data as
	// JSON, to be parsed by the Discoverer
	cmd := exec.Command(d.Path, "-n", d.Namespace, "get", "services", "-o", "json", "--request-timeout", d.Timeout.String())
	cmd.Env = d.addVars(os.Environ()) // Pass through the environment variables
	return cmd.CombinedOutput()
}

func (d *KubectlDiscoveryCommand) GetNodes() ([]byte, error) {
	// Run `kubectl` from the specific path, and namespace, and return data as
	// JSON, to be parsed by the Discoverer
	cmd := exec.Command(d.Path, "-n", d.Namespace, "get", "nodes", "-o", "json", "--request-timeout", d.Timeout.String())
	cmd.Env = d.addVars(os.Environ()) // Pass through the environment variables
	return cmd.CombinedOutput()
}
