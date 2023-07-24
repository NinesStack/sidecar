package discovery

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NinesStack/sidecar/service"
	"github.com/relistan/go-director"
	log "github.com/sirupsen/logrus"
	. "github.com/smartystreets/goconvey/convey"
)

var credsPath string = "fixtures"

type mockK8sDiscoveryCommand struct {
	GetServicesShouldError      bool
	GetServicesShouldReturnJunk bool
	GetServicesWasCalled        bool

	GetNodesShouldError      bool
	GetNodesShouldReturnJunk bool
	GetNodesWasCalled        bool

	GetPodsShouldError      bool
	GetPodsShouldReturnJunk bool
	GetPodsWasCalled        bool
}

func (m *mockK8sDiscoveryCommand) GetServices() ([]byte, error) {
	m.GetServicesWasCalled = true

	if m.GetServicesShouldError {
		return nil, errors.New("intentional test error")
	}

	if m.GetServicesShouldReturnJunk {
		return []byte(`asdfasdf`), nil
	}

	jsonStr := `
	{
	   "items" : [
	      {
	         "metadata" : {
	            "creationTimestamp" : "2022-11-07T13:18:03Z",
	            "labels" : {
	               "Environment" : "dev",
	               "ServiceName" : "chopper"
	            },
	            "name" : "chopper",
	            "uid" : "107b5bbf-9640-4fd0-b5de-1e898e8ae9f7"
	         },
	         "spec" : {
	            "ports" : [
	               {
	                  "port" : 10007,
	                  "protocol" : "TCP",
	                  "targetPort" : 8088,
					  "nodePort": 38088
	               },
	               {
	                  "port" : 10008,
	                  "protocol" : "TCP",
	                  "targetPort" : 8089
	               }
	            ]
	         }
	      }
	   ]
	}

	`
	return []byte(jsonStr), nil
}

func (m *mockK8sDiscoveryCommand) GetNodes() ([]byte, error) {
	m.GetNodesWasCalled = true

	if m.GetNodesShouldError {
		return nil, errors.New("intentional test error")
	}

	if m.GetNodesShouldReturnJunk {
		return []byte(`asdfasdf`), nil
	}

	jsonStr := `
		{
		   "items" : [
		      {
		         "status" : {
		            "addresses" : [
		               {
		                  "address" : "10.100.69.136",
		                  "type" : "InternalIP"
		               },
		               {
		                  "address" : "beowulf.example.com",
		                  "type" : "Hostname"
		               },
		               {
		                  "address" : "beowulf.example.com",
		                  "type" : "InternalDNS"
		               }
		            ]
		         }
		      },
		      {
		         "status" : {
		            "addresses" : [
		               {
		                  "address" : "10.100.69.147",
		                  "type" : "InternalIP"
		               },
		               {
		                  "address" : "heorot.example.com",
		                  "type" : "Hostname"
		               },
		               {
		                  "address" : "heorot.example.com",
		                  "type" : "InternalDNS"
		               }
		            ]
		         }
		      }
		   ]
		}
	`
	return []byte(jsonStr), nil
}

func (m *mockK8sDiscoveryCommand) GetPods() ([]byte, error) {
	m.GetPodsWasCalled = true

	if m.GetPodsShouldError {
		return nil, errors.New("intentional test error")
	}

	if m.GetPodsShouldReturnJunk {
		return []byte(`asdfasdf`), nil
	}

	jsonStr := `
		{
		   "apiVersion" : "v1",
		   "items" : [
		      {
		         "metadata" : {
	                "creationTimestamp" : "2022-11-07T13:18:03Z",
		            "labels" : {
		               "Environment" : "dev",
		               "ServiceName" : "chopper"
		            },
		            "name" : "chopper-64fd6dcf8c-9dd66",
		            "namespace" : "default",
		            "resourceVersion" : "24191869",
		            "uid" : "a9fb2fd7-8f85-4ab2-aae7-ace5b62797dc"
		         },
		         "spec" : {
		            "containers" : [
		               {
		                  "env" : [
		                     {
		                        "name" : "PORT",
		                        "value" : "4000"
		                     }
		                  ],
		                  "image" : "somewhere/chopper:54e623d",
		                  "livenessProbe" : {
		                     "failureThreshold" : 3,
		                     "httpGet" : {
		                        "path" : "/health-check",
		                        "port" : 4000,
		                        "scheme" : "HTTP"
		                     },
		                     "periodSeconds" : 10,
		                     "successThreshold" : 1,
		                     "timeoutSeconds" : 1
		                  },
		                  "name" : "chopper",
		                  "ports" : [
		                     {
		                        "containerPort" : 4000,
		                        "name" : "port-0",
		                        "protocol" : "TCP"
		                     }
		                  ],
		                  "readinessProbe" : {
		                     "failureThreshold" : 3,
		                     "httpGet" : {
		                        "path" : "/health-check",
		                        "port" : 4000,
		                        "scheme" : "HTTP"
		                     },
		                     "initialDelaySeconds" : 3,
		                     "periodSeconds" : 3,
		                     "successThreshold" : 1,
		                     "timeoutSeconds" : 1
		                  }
		               }
		            ],
		            "dnsPolicy" : "ClusterFirst",
		            "nodeName" : "heorot.example.com",
		            "nodeSelector" : {
		               "Role" : "eks-default-node-group"
		            },
		            "restartPolicy" : "Always",
		            "serviceAccount" : "default",
		            "serviceAccountName" : "default",
		            "terminationGracePeriodSeconds" : 30
		         },
		         "status" : {
		            "hostIP" : "10.0.58.178",
		            "podIP" : "10.0.53.11",
		            "podIPs" : [
		               {
		                  "ip" : "10.0.53.11"
		               }
		            ],
		            "startTime" : "2023-06-23T14:58:21Z"
		         }
		      }
		   ],
		   "kind" : "PodList",
		   "metadata" : {
		      "resourceVersion" : "37382860"
		   }
		}
	`
	return []byte(jsonStr), nil
}

func Test_NewK8sAPIDiscoverer(t *testing.T) {
	Convey("NewK8sAPIDiscoverer()", t, func() {
		Convey("returns a properly configured K8sAPIDiscoverer", func() {
			disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")

			So(disco.discoveredSvcs, ShouldNotBeNil)
			So(disco.Namespace, ShouldEqual, "heorot")
			So(disco.hostname, ShouldEqual, "hrothgar")
			So(disco.Command, ShouldNotBeNil)

			command := disco.Command.(*KubeAPIDiscoveryCommand)
			So(command.KubeHost, ShouldEqual, "127.0.0.1")
			So(command.KubePort, ShouldEqual, 443)

		})
	})
}

func Test_K8sHealthCheck(t *testing.T) {
	Convey("HealthCheck() always returns 'AlwaysSuccessful'", t, func() {
		disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
		check, args := disco.HealthCheck(nil)
		So(check, ShouldEqual, "AlwaysSuccessful")
		So(args, ShouldBeEmpty)
	})
}

func Test_K8sListeners(t *testing.T) {
	Convey("Listeners() always returns and empty slice", t, func() {
		disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
		listeners := disco.Listeners()
		So(listeners, ShouldBeEmpty)
	})
}

func Test_K8sGetServices(t *testing.T) {
	Convey("GetServices()", t, func() {
		disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
		mock := &mockK8sDiscoveryCommand{}
		disco.Command = mock

		capture := &bytes.Buffer{}

		Convey("calls the command and unmarshals the result", func() {
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetServicesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldNotContainSubstring, "error")
			So(disco.discoveredSvcs, ShouldNotBeNil)
			So(disco.discoveredSvcs, ShouldNotEqual, &K8sServices{})
			So(len(disco.discoveredSvcs), ShouldEqual, 1)
			// Cheating, there is only one, but this gets it
			for _, svc := range disco.discoveredSvcs {
				So(len(svc.Spec.Ports), ShouldEqual, 2)
			}
		})

		Convey("call the command and logs errors", func() {
			mock.GetServicesShouldError = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetServicesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to invoke")
		})

		Convey("call the command and logs errors from the JSON output", func() {
			mock.GetServicesShouldReturnJunk = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetServicesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to unmarshal services json")
		})
	})
}

func Test_K8sGetNodes(t *testing.T) {
	Convey("GetNodes()", t, func() {
		disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
		mock := &mockK8sDiscoveryCommand{}
		disco.Command = mock

		capture := &bytes.Buffer{}

		Convey("calls the command and unmarshals the result", func() {
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetNodesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldNotContainSubstring, "error")
			So(disco.discoveredNodes, ShouldNotBeNil)
			So(disco.discoveredNodes, ShouldNotEqual, &K8sNodes{})
			So(len(disco.discoveredNodes.Items), ShouldEqual, 2)
			So(len(disco.discoveredNodes.Items[0].Status.Addresses), ShouldEqual, 3)
		})

		Convey("call the command and logs errors", func() {
			mock.GetNodesShouldError = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetNodesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to invoke")
		})

		Convey("call the command and logs errors from the JSON output", func() {
			mock.GetNodesShouldReturnJunk = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetNodesWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to unmarshal nodes json")
		})
	})
}

func Test_K8sServices(t *testing.T) {
	Convey("Services()", t, func() {
		mock := &mockK8sDiscoveryCommand{}

		Convey("works on a newly-created Discoverer", func() {
			disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
			disco.Command = mock

			services := disco.Services()
			So(len(services), ShouldEqual, 0)
		})

		Convey("when discovering for a node where services are running", func() {
			Convey("one service is discovered", func() {
				disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "heorot.example.com")
				disco.Command = mock

				disco.Run(director.NewFreeLooper(director.ONCE, nil))
				services := disco.Services()

				So(len(services), ShouldEqual, 1)
				svc := services[0]
				So(svc.ID, ShouldEqual, "107b5bbf-9640-4fd0-b5de-1e898e8ae9f7")
				So(svc.Name, ShouldEqual, "chopper")
				So(svc.Image, ShouldEqual, "somewhere/chopper:54e623d")
				So(svc.Created.String(), ShouldEqual, "2022-11-07 13:18:03 +0000 UTC")
				So(svc.Hostname, ShouldEqual, "heorot.example.com")
				So(svc.ProxyMode, ShouldEqual, "http")
				So(svc.Status, ShouldEqual, service.ALIVE)
				So(svc.Updated.Unix(), ShouldBeGreaterThan, time.Now().UTC().Add(-2*time.Second).Unix())
				So(len(svc.Ports), ShouldEqual, 1)
				So(svc.Ports[0].IP, ShouldEqual, "10.100.69.147")
			})
		})

		Convey("when discovering for a node without any services", func() {
			Convey("there are no services discovered", func() {
				disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "beowulf", 3*time.Second, credsPath, "beowulf.example.com")
				disco.Command = mock

				disco.Run(director.NewFreeLooper(director.ONCE, nil))
				services := disco.Services()

				So(len(services), ShouldEqual, 0)
			})
		})
	})
}

func Test_K8sGetPods(t *testing.T) {
	Convey("GetPods()", t, func() {
		disco := NewK8sAPIDiscoverer("127.0.0.1", 443, "heorot", 3*time.Second, credsPath, "hrothgar")
		mock := &mockK8sDiscoveryCommand{}
		disco.Command = mock

		capture := &bytes.Buffer{}

		Convey("calls the command and unmarshals the result", func() {
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetPodsWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldNotContainSubstring, "error")
			So(disco.discoveredPods, ShouldNotBeNil)
			So(disco.discoveredPods, ShouldNotEqual, &K8sPods{})
			So(len(disco.discoveredPods), ShouldEqual, 1)

			pods := disco.discoveredPods["chopper"]
			So(pods, ShouldNotBeEmpty)
			pod := pods[0]
			So(pod.ServiceName(), ShouldEqual, "chopper")
		})

		Convey("call the command and logs errors", func() {
			mock.GetPodsShouldError = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetPodsWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to invoke")
		})

		Convey("call the command and logs errors from the JSON output", func() {
			mock.GetPodsShouldReturnJunk = true
			log.SetOutput(capture)
			disco.Run(director.NewFreeLooper(director.ONCE, nil))
			log.SetOutput(os.Stdout)

			So(mock.GetPodsWasCalled, ShouldBeTrue)
			So(capture.String(), ShouldContainSubstring, "Failed to unmarshal pods json")
		})
	})
}
