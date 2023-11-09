package discovery

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/NinesStack/sidecar/service"
	"github.com/relistan/go-director"
	log "github.com/sirupsen/logrus"
)

// A K8sAPIDiscoverer is a discovery mechanism that assumes that a K8s cluster
// with be fronted by a load balancer and that all the ports exposed will match
// up on both the load balancer and the backing pods. It relies on an underlying
// command to run the discovery. This is normally `kubectl`.
type K8sAPIDiscoverer struct {
	Namespace string

	Command K8sDiscoveryAdapter

	discoveredSvcs   map[string]*K8sService
	discoveredNodes  *K8sNodes
	discoveredPods   map[string][]*K8sPod
	lock             sync.RWMutex
	hostname         string
}

// NewK8sAPIDiscoverer returns a properly configured K8sAPIDiscoverer
func NewK8sAPIDiscoverer(kubeHost string, kubePort int, namespace string, timeout time.Duration,
	credsPath string, hostname string) *K8sAPIDiscoverer {

	cmd := NewKubeAPIDiscoveryCommand(kubeHost, kubePort, namespace, timeout, credsPath)

	return &K8sAPIDiscoverer{
		discoveredSvcs:   make(map[string]*K8sService),
		discoveredPods:   make(map[string][]*K8sPod),
		discoveredNodes:  &K8sNodes{},
		Namespace:        namespace,
		Command:          cmd,
		hostname:         hostname,
	}
}

// servicesForNode will emit all the services that we previously discovered.
// This means we will attempt to hit the NodePort for each of the nodes when
// looking for this service over HTTP/TCP.
func (k *K8sAPIDiscoverer) servicesForNode(hostname, ip string) []service.Service {
	var services []service.Service

	for svcName, podList := range k.discoveredPods {
		for _, pod := range podList {
			// We require an annotation called 'ServiceName' to make sure this is
			// a service we want to announce.
			if pod.ServiceName() == "" {
				continue
			}

			if pod.Spec.NodeName != hostname {
				continue
			}

			// If we don't have a service from K8s, then there are no ports to expose
			if k.discoveredPods[pod.ServiceName()] == nil {
				continue
			}

			svc := service.Service{
				ID:        pod.Metadata.UID,
				Name:      svcName,
				Image:     pod.Image(),
				Created:   pod.Metadata.CreationTimestamp,
				Hostname:  pod.Spec.NodeName,
				ProxyMode: "http",
				Status:    service.ALIVE,
				Updated:   time.Now().UTC(),
			}

			for _, port := range k.discoveredSvcs[pod.ServiceName()].Spec.Ports {
				// We only support entries with NodePort defined
				if port.NodePort < 1 {
					continue
				}
				svc.Ports = append(svc.Ports, service.Port{
					Type:        "tcp",
					Port:        int64(port.NodePort),
					ServicePort: int64(port.Port),
					IP:          ip,
				})
			}
			services = append(services, svc)
		}
	}

	return services
}

// Services implements part of the Discoverer interface and looks at the last
// cached data from the Command (`kubectl`) and returns services in a format
// that Sidecar can manage.
func (k *K8sAPIDiscoverer) Services() []service.Service {
	k.lock.RLock()
	defer k.lock.RUnlock()

	// Enumerate all the K8s nodes we discovered, and for each one, enumerate
	// all the services.
	var services []service.Service
	for _, node := range k.discoveredNodes.Items {
		hostname, ip := getIPHostForNode(&node)

		// Don't discover all nodes, only this one. Short circuit if we found it
		// since we'll only be in the list once.
		if hostname == k.hostname {
			services = k.servicesForNode(hostname, ip)
			break
		}
	}

	return services
}

func getIPHostForNode(node *K8sNode) (hostname string, ip string) {
	for _, address := range node.Status.Addresses {
		if address.Type == "InternalIP" {
			ip = address.Address
		}

		if address.Type == "Hostname" {
			hostname = address.Address
		}
	}

	return hostname, ip
}

// HealthCheck implements part of the Discoverer interface and returns the
// built-in AlwaysSuccessful check, on the assumption that the underlying load
// balancer we are pointing to will have already health checked the service.
func (k *K8sAPIDiscoverer) HealthCheck(svc *service.Service) (string, string) {
	return "AlwaysSuccessful", ""
}

// Listeners implements part of the Discoverer interface and always returns
// an empty list because it doesn't make sense in this context.
func (k *K8sAPIDiscoverer) Listeners() []ChangeListener {
	return []ChangeListener{}
}

// Run is part of the Discoverer interface and calls the Command in a loop,
// which is injected as a Looper.
func (k *K8sAPIDiscoverer) Run(looper director.Looper) {
	var (
		data []byte
		err  error
	)
	looper.Loop(func() error {
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			data, err := k.getServices()
			if err != nil {
				log.Errorf("Failed to unmarshal services json: %s, %s", err, string(data))
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			data, err = k.getNodes()
			if err != nil {
				log.Errorf("Failed to unmarshal nodes json: %s, %s", err, string(data))
			}
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			data, err = k.getPods()
			if err != nil {
				log.Errorf("Failed to unmarshal pods json: %s, %s", err, string(data))
			}
			wg.Done()
		}()

		wg.Wait()
		return nil
	})
}

func (k *K8sAPIDiscoverer) getServices() ([]byte, error) {
	data, err := k.Command.GetServices()
	if err != nil {
		log.Errorf("Failed to invoke K8s API discovery: %s", err)
	}

	var svcs K8sServices

	err = json.Unmarshal(data, &svcs)
	if err != nil {
		return data, err
	}

	k.lock.Lock()
	for _, svc := range svcs.Items {
		k.discoveredSvcs[svc.ServiceName()] = &svc
	}
	k.lock.Unlock()
	return data, err
}

func (k *K8sAPIDiscoverer) getNodes() ([]byte, error) {
	data, err := k.Command.GetNodes()
	if err != nil {
		log.Errorf("Failed to invoke K8s API discovery: %s", err)
	}

	k.lock.Lock()
	err = json.Unmarshal(data, &k.discoveredNodes)
	k.lock.Unlock()
	return data, err
}

func (k *K8sAPIDiscoverer) getPods() ([]byte, error) {
	data, err := k.Command.GetPods()
	if err != nil {
		log.Errorf("Failed to invoke K8s API discovery: %s", err)
	}

	var pods K8sPods

	err = json.Unmarshal(data, &pods)
	if err != nil {
		return data, err
	}

	k.lock.Lock()
	for _, pod := range pods.Items {
		k.discoveredPods[pod.ServiceName()] = append(k.discoveredPods[pod.ServiceName()], &pod)
	}
	k.lock.Unlock()
	return data, err
}
