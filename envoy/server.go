package envoy

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/NinesStack/sidecar/catalog"
	"github.com/NinesStack/sidecar/config"
	"github.com/NinesStack/sidecar/envoy/adapter"
	envoy_disco "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/relistan/go-director"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const (
	// LooperUpdateInterval indicates how often to check if the state has changed
	LooperUpdateInterval = 1 * time.Second
)

type xdsCallbacks struct{}

func (*xdsCallbacks) OnStreamOpen(context.Context, int64, string) error  { return nil }
func (*xdsCallbacks) OnStreamClosed(int64)                               {}
func (*xdsCallbacks) OnStreamRequest(int64, *envoy_disco.DiscoveryRequest) error { return nil }
func (*xdsCallbacks) OnStreamResponse(ctx context.Context, _ int64, req *envoy_disco.DiscoveryRequest, _ *envoy_disco.DiscoveryResponse) {
	if req.GetErrorDetail().GetCode() != 0 {
		log.Errorf("Received Envoy error code %d: %s",
			req.GetErrorDetail().GetCode(),
			strings.ReplaceAll(req.GetErrorDetail().GetMessage(), "\n", ""),
		)
	}
}
func (*xdsCallbacks) OnFetchRequest(context.Context, *envoy_disco.DiscoveryRequest) error   { return nil }
func (*xdsCallbacks) OnFetchResponse(*envoy_disco.DiscoveryRequest, *envoy_disco.DiscoveryResponse) {}
func (*xdsCallbacks) OnDeltaStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	log.Info("OnDeltaStreamOpen()");
	return nil
}
func (*xdsCallbacks) OnStreamDeltaRequest(streamID int64, req *envoy_disco.DeltaDiscoveryRequest) error {
	log.Info("OnDeltaStreamRequest()");
	return nil
}
func (*xdsCallbacks) OnStreamDeltaResponse(streamID int64,
	req *envoy_disco.DeltaDiscoveryRequest, resp *envoy_disco.DeltaDiscoveryResponse) {}
func (*xdsCallbacks) OnDeltaStreamClosed(streamID int64) {}

// Server is a wrapper around Envoy's control plane xDS gRPC server and it uses
// the Aggregated Discovery Service (ADS) mechanism.
type Server struct {
	config        config.EnvoyConfig
	state         *catalog.ServicesState
	snapshotCache cache.SnapshotCache
	xdsServer     xds.Server
}

// newSnapshotVersion returns a unique version for Envoy cache snapshots
func newSnapshotVersion() string {
	// When triggering watches after a cache snapshot is set, the go-control-plane
	// only sends resources which have a different version to Envoy.
	// `time.Now().UnixNano()` should always return a unique number.
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// Run starts the Envoy update looper and the Envoy gRPC server
func (s *Server) Run(ctx context.Context, looper director.Looper, grpcListener net.Listener) {
	// The local hostname needs to match the value passed via `--service-node` to Envoy
	// See https://github.com/envoyproxy/envoy/issues/144#issuecomment-267401271
	// This never changes, so we don't need to lock the state here
	hostname := s.state.Hostname

	// prevStateLastChanged caches the state.LastChanged timestamp when we send an
	// update to Envoy
	prevStateLastChanged := time.Unix(0, 0)
	go looper.Loop(func() error {
		s.state.RLock()
		lastChanged := s.state.LastChanged

		// Do nothing if the state hasn't changed
		if lastChanged == prevStateLastChanged {
			s.state.RUnlock()
			return nil
		}
		resources := adapter.EnvoyResourcesFromState(s.state, s.config.BindIP, s.config.UseHostnames)
		s.state.RUnlock()

		prevStateLastChanged = lastChanged

		// Set the computed listeners and clusters in the current snapshot to
		// send them to Envoy.
		// See the eventual consistency considerations in the documentation for
		// details about how Envoy updates these resources:
		// https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol#eventual-consistency-considerations

		// Create a new snapshot version and send the listeners and clusters to Envoy
		snapshotVersion := newSnapshotVersion()
		snap, err := cache.NewSnapshot(snapshotVersion, resources.AsMap())
		if err != nil {
			log.Errorf("Failed to create new Envoy cache snapshot: %s", err)
			return nil
		}

		log.Infof("New Snapshot: %#v", snap)
		err = s.snapshotCache.SetSnapshot(ctx, hostname, snap)
		if err != nil {
			log.Errorf("Failed to set new Envoy cache snapshot: %s", err)
			return nil
		}

		log.Infof("Sent %d endpoints, %d listeners and %d clusters to Envoy with version %s",
			len(resources.Endpoints), len(resources.Listeners), len(resources.Clusters), snapshotVersion,
		)

		return nil
	})

	grpcServer := grpc.NewServer()
	envoy_disco.RegisterAggregatedDiscoveryServiceServer(grpcServer, s.xdsServer)

	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("Failed to start Envoy gRPC server: %s", err)
		}
	}()

	// Currently, this will block forever
	<-ctx.Done()
	grpcServer.GracefulStop()
}

// NewServer creates a new Server instance
func NewServer(ctx context.Context, state *catalog.ServicesState, config config.EnvoyConfig) *Server {
	// Instruct the snapshot cache to use Aggregated Discovery Service (ADS)
	logger := log.New()
	logger.SetLevel(log.DebugLevel)
	snapshotCache := cache.NewSnapshotCache(true, cache.IDHash{}, logger)

	return &Server{
		config:        config,
		state:         state,
		snapshotCache: snapshotCache,
		xdsServer:     xds.NewServer(ctx, snapshotCache, &xdsCallbacks{}),
	}
}
