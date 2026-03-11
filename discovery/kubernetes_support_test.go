package discovery

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	log "github.com/sirupsen/logrus"
	. "github.com/smartystreets/goconvey/convey"
)

func Test_NewKubeAPIDiscoveryCommand(t *testing.T) {
	Convey("NewKubeAPIDiscoveryCommand()", t, func() {

		Convey("returns a properly configured struct", func() {
			cmd := NewKubeAPIDiscoveryCommand("beowulf.example.com", 443, "namespace", 10*time.Millisecond, credsPath)

			So(cmd, ShouldNotBeNil)
			So(cmd.Namespace, ShouldEqual, "namespace")
			So(cmd.Timeout, ShouldEqual, 10*time.Millisecond)
			So(cmd.KubeHost, ShouldEqual, "beowulf.example.com")
			So(cmd.KubePort, ShouldEqual, 443)
			So(cmd.token, ShouldContainSubstring, "this would be a token")
			So(cmd.client, ShouldNotBeNil)
		})

		Convey("logs when it can't read the token", func() {
			var cmd *KubeAPIDiscoveryCommand

			capture := LogCapture(func() {
				cmd = NewKubeAPIDiscoveryCommand("beowulf.example.com", 443, "namespace", 10*time.Millisecond, "/tmp/does-not-exist")
			})

			So(cmd, ShouldBeNil)
			So(capture, ShouldContainSubstring, "Failed to read serviceaccount token")
		})

		Convey("logs when it can't read the CA.crt", func() {
			var cmd *KubeAPIDiscoveryCommand

			capture := LogCapture(func() {
				cmd = NewKubeAPIDiscoveryCommand("beowulf.example.com", 443, "namespace", 10*time.Millisecond, credsPath+"/bad-fixture")
			})

			So(cmd, ShouldNotBeNil)
			So(capture, ShouldContainSubstring, "No certs appended!")

			So(cmd.Namespace, ShouldEqual, "namespace")
			So(cmd.Timeout, ShouldEqual, 10*time.Millisecond)
			So(cmd.KubeHost, ShouldEqual, "beowulf.example.com")
			So(cmd.KubePort, ShouldEqual, 443)
			So(cmd.token, ShouldContainSubstring, "this would be a token")
			So(cmd.client, ShouldNotBeNil)
		})
	})
}

func Test_makeRequest(t *testing.T) {
	Convey("makeRequest()", t, func() {
		Reset(func() { httpmock.DeactivateAndReset() })

		cmd := NewKubeAPIDiscoveryCommand("beowulf.example.com", 80, "namespace", 10*time.Millisecond, credsPath)
		httpmock.ActivateNonDefault(cmd.client)

		Convey("makes a request with the right headers and auth", func() {
			var auth string
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/nowhere",
				func(req *http.Request) (*http.Response, error) {
					auth = req.Header.Get("Authorization")
					return httpmock.NewJsonResponse(200, map[string]interface{}{"success": "yeah"})
				},
			)

			body, err := cmd.makeRequest("/nowhere", "")
			So(err, ShouldBeNil)
			So(auth, ShouldStartWith, "Bearer ")
			So(auth, ShouldContainSubstring, "this would be a token")

			So(body, ShouldNotBeEmpty)
		})

		Convey("handles non-200 status code", func() {
			var auth string
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/nowhere",
				func(req *http.Request) (*http.Response, error) {
					auth = req.Header.Get("Authorization")
					return httpmock.NewJsonResponse(403, map[string]interface{}{"bad": "times"})
				},
			)

			body, err := cmd.makeRequest("/nowhere", "")
			So(err, ShouldNotBeNil)
			So(auth, ShouldStartWith, "Bearer ")
			So(auth, ShouldContainSubstring, "this would be a token")

			So(err.Error(), ShouldContainSubstring, "got unexpected response code from /nowhere: 403")
			So(body, ShouldBeEmpty)
		})

		Convey("refreshes token and retries on 401", func() {
			callCount := 0
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/nowhere",
				func(req *http.Request) (*http.Response, error) {
					callCount++
					if callCount == 1 {
						return httpmock.NewJsonResponse(401, map[string]interface{}{"error": "unauthorized"})
					}
					return httpmock.NewJsonResponse(200, map[string]interface{}{"success": "yeah"})
				},
			)

			capture := LogCapture(func() {
				body, err := cmd.makeRequest("/nowhere", "")
				So(err, ShouldBeNil)
				So(body, ShouldNotBeEmpty)
			})

			So(callCount, ShouldEqual, 2)
			So(capture, ShouldContainSubstring, "Got 401 from K8s API")
		})

		Convey("returns error when retry after 401 also fails", func() {
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/nowhere",
				httpmock.NewJsonResponderOrPanic(401, map[string]interface{}{"error": "unauthorized"}),
			)

			capture := LogCapture(func() {
				body, err := cmd.makeRequest("/nowhere", "")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "got unexpected response code from /nowhere: 401")
				So(body, ShouldBeEmpty)
			})

			So(capture, ShouldContainSubstring, "Got 401 from K8s API")
		})

		Convey("handles error back from http call", func() {
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/nowhere",
				httpmock.NewErrorResponder(errors.New("intentional test error")),
			)

			body, err := cmd.makeRequest("/nowhere", "")

			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "intentional test error")
			So(body, ShouldBeEmpty)
		})
	})
}

func Test_GetServices(t *testing.T) {
	Convey("GetServices()", t, func() {
		Reset(func() { httpmock.DeactivateAndReset() })

		cmd := NewKubeAPIDiscoveryCommand("beowulf.example.com", 80, "namespace", 10*time.Millisecond, credsPath)
		httpmock.ActivateNonDefault(cmd.client)

		Convey("makes a request with the right headers and auth", func() {
			var auth string
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/api/v1/services/",
				func(req *http.Request) (*http.Response, error) {
					auth = req.Header.Get("Authorization")
					return httpmock.NewJsonResponse(200, map[string]interface{}{"success": "yeah"})
				},
			)

			body, err := cmd.GetServices()
			So(err, ShouldBeNil)
			So(auth, ShouldStartWith, "Bearer ")
			So(auth, ShouldContainSubstring, "this would be a token")

			So(body, ShouldNotBeEmpty)
		})
	})
}

func Test_GetNodes(t *testing.T) {
	Convey("GetNodes()", t, func() {
		Reset(func() { httpmock.DeactivateAndReset() })

		cmd := NewKubeAPIDiscoveryCommand("beowulf.example.com", 80, "namespace", 10*time.Millisecond, credsPath)
		httpmock.ActivateNonDefault(cmd.client)

		Convey("makes a request with the right headers and auth", func() {
			var auth string
			httpmock.RegisterResponder("GET", "http://beowulf.example.com:80/api/v1/nodes/",
				func(req *http.Request) (*http.Response, error) {
					auth = req.Header.Get("Authorization")
					return httpmock.NewJsonResponse(200, map[string]interface{}{"success": "yeah"})
				},
			)

			body, err := cmd.GetNodes()
			So(err, ShouldBeNil)
			So(auth, ShouldStartWith, "Bearer ")
			So(auth, ShouldContainSubstring, "this would be a token")

			So(body, ShouldNotBeEmpty)
		})
	})
}

// LogCapture logs for async testing where we can't get a nice handle on thigns
func LogCapture(fn func()) string {
	capture := &bytes.Buffer{}
	log.SetOutput(capture)
	fn()
	log.SetOutput(os.Stdout)

	return capture.String()
}
