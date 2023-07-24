package discovery

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	cleanhttp "github.com/hashicorp/go-cleanhttp"
	log "github.com/sirupsen/logrus"
)

// K8sServices represents the payload that is returned by `kubectl get services -o json`
type K8sServices struct {
	APIVersion string       `json:"apiVersion"`
	Items      []K8sService `json:"items"`
	Kind       string       `json:"kind"`
	Metadata   struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
}

// A K8sService represents a single service from the K8sServices response
type K8sService struct {
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
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			// This field mutates types when served from the K8s API. This is bad
			// API design, and means this won't parse with Go's stdlib JSON support.
			// Commenting out so that this reason is clear.
			// TargetPort int    `json:"targetPort"`
			NodePort int `json:"nodePort"`
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
}

// ServiceName extracts and returns the service name
func (svc *K8sService) ServiceName() string {
	return svc.Metadata.Labels.ServiceName
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
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
}

type K8sNodeAddress struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

// A K8sPods is what the API returns for a list of pods. We parse out the
// fields that we care about.
type K8sPods struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Metadata   struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Items []K8sPod `json:"items"`
}

// A K8sPod is an API response representing a single Pod
type K8sPod struct {
	Metadata struct {
		Name              string    `json:"name"`
		Namespace         string    `json:"namespace"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
		Labels            struct {
			Environment string `json:"Environment"`
			ServiceName string `json:"ServiceName"`
			App         string `json:"app"`
			Release     string `json:"Release"`
		} `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
			Ports []struct {
				Name          string `json:"name"`
				ContainerPort int    `json:"containerPort"`
				Protocol      string `json:"protocol"`
			} `json:"ports,omitempty"`
		} `json:"containers"`
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type               string      `json:"type"`
			Status             string      `json:"status"`
			LastProbeTime      interface{} `json:"lastProbeTime"`
			LastTransitionTime time.Time   `json:"lastTransitionTime"`
		} `json:"conditions"`
		HostIP string `json:"hostIP"`
		PodIP  string `json:"podIP"`
		PodIPs []struct {
			IP string `json:"ip"`
		} `json:"podIPs"`
		StartTime         time.Time               `json:"startTime"`
		ContainerStatuses []K8sPodContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

func (p *K8sPod) ServiceName() string {
	return p.Metadata.Labels.ServiceName
}

func (p *K8sPod) Image() string {
	if len(p.Spec.Containers) < 1 {
		return ""
	}

	// In theory the first one should be the main container image. There is no
	// other real way to tell if a side car container is running in the pod.
	return p.Spec.Containers[0].Image
}

type K8sPodContainerStatus struct {
	Name  string `json:"name"`
	State struct {
		Running struct {
			StartedAt time.Time `json:"startedAt"`
		} `json:"running"`
		Terminated struct {
			ExitCode    int       `json:"exitCode"`
			Reason      string    `json:"reason"`
			StartedAt   time.Time `json:"startedAt"`
			FinishedAt  time.Time `json:"finishedAt"`
			ContainerID string    `json:"containerID"`
		} `json:"terminated"`
	} `json:"state"`
	LastState struct {
	} `json:"lastState"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	Image        string `json:"image"`
	ImageID      string `json:"imageID"`
	ContainerID  string `json:"containerID"`
}

// A K8sDiscoveryAdapter wraps a call to an external command that can be used
// to discover services running on a Kubernetes cluster. This is normally
// `kubectl` but for tests, this allows mocking out the underlying call.
type K8sDiscoveryAdapter interface {
	GetServices() ([]byte, error)
	GetNodes() ([]byte, error)
	GetPods() ([]byte, error)
}

// KubeAPIDiscoveryCommand is the main implementation for K8sDiscoveryCommand
type KubeAPIDiscoveryCommand struct {
	Namespace string
	Timeout   time.Duration

	KubeHost string
	KubePort int

	token  string
	client *http.Client
}

// NewKubeAPIDiscoveryCommand returns a properly configured KubeAPIDiscoveryCommand
func NewKubeAPIDiscoveryCommand(kubeHost string, kubePort int, namespace string, timeout time.Duration, credsPath string) *KubeAPIDiscoveryCommand {
	d := &KubeAPIDiscoveryCommand{
		Namespace: namespace,
		Timeout:   timeout,
		KubeHost:  kubeHost,
		KubePort:  kubePort,
	}
	// Cache the secret from the file
	data, err := ioutil.ReadFile(credsPath + "/token")
	if err != nil {
		log.Errorf("Failed to read serviceaccount token: %s", err)
		return nil
	}

	// New line is illegal in tokens
	d.token = strings.Replace(string(data), "\n", "", -1)

	// Set up the timeout on a clean HTTP client
	d.client = cleanhttp.DefaultClient()
	d.client.Timeout = d.Timeout

	// Get the SystemCertPool — on error we have empty pool
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	certs, err := ioutil.ReadFile(credsPath + "/ca.crt")
	if err != nil {
		log.Warnf("Failed to load CA cert file: %s", err)
	}

	if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
		log.Warn("No certs appended! Using system certs only")
	}

	// Add the pool to the TLS config we'll use in the client.
	config := &tls.Config{
		RootCAs: rootCAs,
	}

	d.client.Transport = &http.Transport{TLSClientConfig: config}

	return d
}

func (d *KubeAPIDiscoveryCommand) makeRequest(path string) ([]byte, error) {
	var scheme = "http"
	if d.KubePort == 443 {
		scheme = "https"
	}

	apiURL := url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", d.KubeHost, d.KubePort),
		Path:   path,
	}

	req, err := http.NewRequest("GET", apiURL.String(), nil)
	if err != nil {
		return []byte{}, err
	}

	req.Header.Set("Authorization", "Bearer "+d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to fetch from K8s API '%s': %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 || resp.StatusCode < 200 {
		return []byte{}, fmt.Errorf("got unexpected response code from %s: %d", path, resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to read from K8s API '%s' response body: %w", path, err)
	}

	return body, nil
}

func (d *KubeAPIDiscoveryCommand) GetServices() ([]byte, error) {
	return d.makeRequest("/api/v1/services/")
}

func (d *KubeAPIDiscoveryCommand) GetNodes() ([]byte, error) {
	return d.makeRequest("/api/v1/nodes/")
}

func (d *KubeAPIDiscoveryCommand) GetPods() ([]byte, error) {
	return d.makeRequest("/api/v1/namespaces/" + d.Namespace + "/pods?limit=10000")

}
