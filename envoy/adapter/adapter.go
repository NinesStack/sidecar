package adapter

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/NinesStack/sidecar/catalog"
	"github.com/NinesStack/sidecar/service"
	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	router "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcpp "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	cache_types "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/golang/protobuf/ptypes/wrappers"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	anypb "google.golang.org/protobuf/types/known/anypb"
)

const (

	// ServiceNameSeparator is used to join service name and port. Must not
	// occur in service names.
	ServiceNameSeparator = ":"

	// PortCollisionLoggingBackoff is how long we wait between logging about
	// port collisions.
	PortCollisionLoggingBackoff = 1 * time.Minute
)

var (
	LastLoggedPortCollision time.Time
)

// EnvoyResources is a collection of Enovy API resource definitions
type EnvoyResources struct {
	Endpoints []cache_types.Resource
	Clusters  []cache_types.Resource
	Listeners []cache_types.Resource
}

// AsMap returns the resources as a map, how it's now preferred in V3 API
func (e *EnvoyResources) AsMap() map[string][]cache_types.Resource {
	mapping := map[string][]cache_types.Resource{
		resource.EndpointType: e.Endpoints,
		resource.ClusterType:  e.Clusters,
		resource.ListenerType: e.Listeners,
	}

	log.Debugf("%#v", mapping)
	return mapping
}

// SvcName formats an Envoy service name from our service name and port
func SvcName(name string, port int64) string {
	return fmt.Sprintf("%s%s%d", name, ServiceNameSeparator, port)
}

// SvcNameSplit an Enovy service name into our service name and port
func SvcNameSplit(name string) (string, int64, error) {
	parts := strings.Split(name, ServiceNameSeparator)
	if len(parts) < 2 {
		return "", -1, fmt.Errorf("%s", "Unable to split service name and port!")
	}

	svcName := parts[0]
	svcPort, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", -1, fmt.Errorf("%s", "Unable to parse port!")
	}

	return svcName, svcPort, nil
}

// LookupHost does a vv slow lookup of the DNS host for a service. Totally not
// optimized for high throughput. You should only do this in development
// scenarios.
func LookupHost(hostname string) (string, error) {
	addrs, err := net.LookupHost(hostname)

	if err != nil {
		return "", err
	}
	return addrs[0], nil
}

// isPortCollision will make sure we don't tell Envoy about more than one
// service on the same port. This leads to it going completely apeshit both
// with CPU usage and logging.
func isPortCollision(portsMap map[int64]string, svc *service.Service, port service.Port) bool {
	registeredName, ok := portsMap[port.ServicePort]
	// See if we already know about this port
	if ok {
		// If it is the same service, then no collision and we return false.
		// Otherwise, this is not the service assigned to this port.
		return registeredName != svc.Name
	}

	// We don't know about it, so assign it.
	portsMap[port.ServicePort] = svc.Name
	return false
}

// EnvoyResourcesFromState creates a set of Enovy API resource definitions from
// all the ServicePorts in the Sidecar state. The Sidecar state needs to be
// locked by the caller before calling this function.
func EnvoyResourcesFromState(state *catalog.ServicesState, bindIP string,
	useHostnames bool) EnvoyResources {

	endpointMap := make(map[string]*endpoint.ClusterLoadAssignment)
	clusterMap := make(map[string]*cluster.Cluster)
	listenerMap := make(map[string]cache_types.Resource)

	// Used to make sure we don't map the same port to more than one service
	portsMap := make(map[int64]string)

	// We use the more expensive EachServiceSorted to make sure we make a stable
	// port mapping allocation in the event of port collisions.
	state.EachServiceSorted(func(hostname *string, id *string, svc *service.Service) {
		if svc == nil || !svc.IsAlive() {
			return
		}

		// Loop over the ports and generate a named listener for each port
		for _, port := range svc.Ports {
			// Only listen on ServicePorts
			if port.ServicePort < 1 {
				continue
			}

			// Make sure we don't make Envoy go nuts by reporting the same port twice
			if isPortCollision(portsMap, svc, port) {
				// This happens A LOT when it happens, so let's back off to once a minute-ish
				if time.Now().UTC().Sub(LastLoggedPortCollision) > PortCollisionLoggingBackoff {
					log.Warnf(
						"Port collision! %s is attempting to squat on port %d owned by %s",
						svc.Name, port.ServicePort, portsMap[port.ServicePort],
					)
					LastLoggedPortCollision = time.Now().UTC()
				}
				continue
			}

			envoyServiceName := SvcName(svc.Name, port.ServicePort)

			if assignment, ok := endpointMap[envoyServiceName]; ok {
				assignment.Endpoints[0].LbEndpoints =
					append(assignment.Endpoints[0].LbEndpoints,
						envoyServiceFromService(svc, port.ServicePort, useHostnames)...)
			} else {
				endpointMap[envoyServiceName] = &endpoint.ClusterLoadAssignment{
					ClusterName: envoyServiceName,
					Endpoints: []*endpoint.LocalityLbEndpoints{{
						LbEndpoints: envoyServiceFromService(svc, port.ServicePort, useHostnames),
					}},
				}

				clusterMap[envoyServiceName] = &cluster.Cluster{
					Name:                 envoyServiceName,
					ConnectTimeout:       &duration.Duration{Nanos: 500000000}, // 500ms
					ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
					EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
						EdsConfig: &core.ConfigSource{
							ConfigSourceSpecifier: &core.ConfigSource_Ads{
								Ads: &core.AggregatedConfigSource{},
							},
							ResourceApiVersion: core.ApiVersion_V3,
						},
					},
					// Contour believes the IdleTimeout should be set to 60s. Not sure if we also need to enable these.
					// See here: https://github.com/projectcontour/contour/blob/2858fec20d26f56cc75a19d91b61d625a86f36de/internal/envoy/listener.go#L102-L106
					// CommonHttpProtocolOptions: &core.HttpProtocolOptions{
					// 	IdleTimeout:           &duration.Duration{Seconds: 60},
					// 	MaxConnectionDuration: &duration.Duration{Seconds: 60},
					// },
					// If this needs to be enabled, we might also need to set `ProtocolSelection: api.USE_DOWNSTREAM_PROTOCOL`.
					// Http2ProtocolOptions: &core.Http2ProtocolOptions{},
				}
			}

			if _, ok := listenerMap[envoyServiceName]; !ok {
				listener, err := envoyListenerFromService(svc, envoyServiceName, port.ServicePort, bindIP)
				if err != nil {
					log.Errorf("Failed to create Envoy listener for service %q and port %d: %s", svc.Name, port.ServicePort, err)
					continue
				}
				listenerMap[envoyServiceName] = listener
			}
		}
	})

	endpoints := make([]cache_types.Resource, 0, len(endpointMap))
	for _, endpoint := range endpointMap {
		endpoints = append(endpoints, endpoint)
	}

	clusters := make([]cache_types.Resource, 0, len(clusterMap))
	for _, cluster := range clusterMap {
		clusters = append(clusters, cluster)
	}

	listeners := make([]cache_types.Resource, 0, len(listenerMap))
	for _, listener := range listenerMap {
		listeners = append(listeners, listener)
	}

	return EnvoyResources{
		Endpoints: endpoints,
		Clusters:  clusters,
		Listeners: listeners,
	}
}

// connectionManagerForService returns a ConnectionManager configured
// appropriately for the Sidecar service
func connectionManagerForService(svc *service.Service, envoyServiceName string) (managerName string, manager protoreflect.ProtoMessage, err error) {
	switch svc.ProxyMode {
	case "ws":
		fallthrough
	case "http":
		managerName = wellknown.HTTPConnectionManager

		routerConfig, _ := anypb.New(&router.Router{})
		manager = &hcm.HttpConnectionManager{
			StatPrefix: "ingress_http",
			HttpFilters: []*hcm.HttpFilter{{
				Name: wellknown.Router,
				ConfigType: &hcm.HttpFilter_TypedConfig{TypedConfig: routerConfig},
			}},
			RouteSpecifier: &hcm.HttpConnectionManager_RouteConfig{
				RouteConfig: &route.RouteConfiguration{
					ValidateClusters: &wrappers.BoolValue{Value: false},
					VirtualHosts: []*route.VirtualHost{{
						Name:    svc.Name,
						Domains: []string{"*"},
						Routes: []*route.Route{{
							Match: &route.RouteMatch{
								PathSpecifier: &route.RouteMatch_Prefix{
									Prefix: "/",
								},
							},
							Action: &route.Route_Route{
								Route: &route.RouteAction{
									ClusterSpecifier: &route.RouteAction_Cluster{
										Cluster: envoyServiceName,
									},
									Timeout: &duration.Duration{},
								},
							},
						}},
					}},
				},
			},
		}
		if svc.ProxyMode == "ws" {
			manager.(*hcm.HttpConnectionManager).UpgradeConfigs = []*hcm.HttpConnectionManager_UpgradeConfig{
				{
					UpgradeType: "websocket",
				},
			}
		}
	case "tcp":
		managerName = wellknown.TCPProxy

		manager = &tcpp.TcpProxy{
			StatPrefix: "ingress_tcp",
			ClusterSpecifier: &tcpp.TcpProxy_Cluster{
				Cluster: envoyServiceName,
			},
		}
	default:
		return "", nil, fmt.Errorf("unrecognised proxy mode: %s", svc.ProxyMode)
	}

	// If it was a supported type, return the result
	return managerName, manager, nil
}

// filterChainsForService returns a filter chain configured appropriately for
// the Sidecar service
func filterChainsForService(svc *service.Service, managerName string, serializedManager *anypb.Any) []*listener.FilterChain {
	return []*listener.FilterChain{{
		Filters: []*listener.Filter{{
			Name: managerName,
			ConfigType: &listener.Filter_TypedConfig{
				TypedConfig: serializedManager,
			},
		}},
	}}
}

// envoyListenerFromService creates an Envoy listener from a service instance
func envoyListenerFromService(svc *service.Service, envoyServiceName string,
	servicePort int64, bindIP string) (cache_types.Resource, error) {

	managerName, manager, err := connectionManagerForService(svc, envoyServiceName)
	if err != nil {
		return nil, fmt.Errorf("failed to create the connection manager: %w", err)
	}

	var serializedManager anypb.Any
	anypb.MarshalFrom(&serializedManager, manager, proto.MarshalOptions{})
	// serializedManager, err := ptypes.MarshalAny(manager)
	if err != nil {
		return nil, fmt.Errorf("failed to create the connection manager: %w", err)
	}

	filterChains := filterChainsForService(svc, managerName, &serializedManager)

	return &listener.Listener{
		Name: envoyServiceName,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Address: bindIP,
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: uint32(servicePort),
					},
				},
			},
		},
		FilterChains: filterChains,
	}, nil
}

// envoyServiceFromService converts a Sidecar service to an Envoy API service for
// reporting to the proxy
func envoyServiceFromService(svc *service.Service, svcPort int64, useHostnames bool) []*endpoint.LbEndpoint {
	var endpoints []*endpoint.LbEndpoint
	for _, port := range svc.Ports {
		// No sense worrying about unexposed ports
		if port.ServicePort == svcPort {
			address := port.IP

			// NOT recommended... this is very slow. Useful in dev modes where you
			// need to resolve to a different IP address only.
			if useHostnames {
				if host, err := LookupHost(svc.Hostname); err == nil {
					address = host
				} else {
					log.Warnf("Unable to resolve %s, using IP address", svc.Hostname)
				}
			}

			endpoints = append(endpoints, &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Address: address,
									PortSpecifier: &core.SocketAddress_PortValue{
										PortValue: uint32(port.Port),
									},
								},
							},
						},
					},
				},
			})
		}
	}

	return endpoints
}
