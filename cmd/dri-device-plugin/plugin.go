package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// plugin implements v1beta1.DevicePluginServer. The embedded Unimplemented
// server answers GetPreferredAllocation and PreStartContainer.
type plugin struct {
	v1beta1.UnimplementedDevicePluginServer

	paths  []string          // host device paths, in glob order
	ids    map[string]string // advertised device ID -> host path; read-only after newPlugin
	count  int
	prefix string

	mu      sync.Mutex
	healthy map[string]bool // host path -> last stat succeeded
	changed chan struct{}   // closed and replaced whenever health changes
}

func deviceID(path string, i int) string {
	return fmt.Sprintf("%s-%d", filepath.Base(path), i)
}

func newPlugin(paths []string, count int, prefix string) (*plugin, error) {
	p := &plugin{
		paths:   paths,
		ids:     map[string]string{},
		count:   count,
		prefix:  prefix,
		healthy: map[string]bool{},
		changed: make(chan struct{}),
	}
	for _, path := range paths {
		for i := range count {
			id := deviceID(path, i)
			if other, dup := p.ids[id]; dup {
				return nil, fmt.Errorf("devices %s and %s both map to ID %s", other, path, id)
			}
			p.ids[id] = path
		}
		p.healthy[path] = true // the glob just found it
	}
	return p, nil
}

// devices returns the current device list and a channel closed on the next
// health change.
func (p *plugin) devices() ([]*v1beta1.Device, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	devs := make([]*v1beta1.Device, 0, len(p.paths)*p.count)
	for _, path := range p.paths {
		health := v1beta1.Unhealthy
		if p.healthy[path] {
			health = v1beta1.Healthy
		}
		for i := range p.count {
			devs = append(devs, &v1beta1.Device{ID: deviceID(path, i), Health: health})
		}
	}
	return devs, p.changed
}

func (p *plugin) checkHealth() {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	for _, path := range p.paths {
		_, err := os.Stat(path)
		ok := err == nil
		if ok != p.healthy[path] {
			p.healthy[path] = ok
			changed = true
			logger.Warn().Err(err).Str("device", path).Bool("healthy", ok).Msg("device health changed")
		}
	}
	if changed {
		close(p.changed)
		p.changed = make(chan struct{})
	}
}

func (p *plugin) GetDevicePluginOptions(context.Context, *v1beta1.Empty) (*v1beta1.DevicePluginOptions, error) {
	return &v1beta1.DevicePluginOptions{PreStartRequired: false, GetPreferredAllocationAvailable: false}, nil
}

func (p *plugin) ListAndWatch(_ *v1beta1.Empty, stream grpc.ServerStreamingServer[v1beta1.ListAndWatchResponse]) error {
	for {
		devs, changed := p.devices()
		if err := stream.Send(&v1beta1.ListAndWatchResponse{Devices: devs}); err != nil {
			return err
		}
		select {
		case <-changed:
		case <-stream.Context().Done():
			return nil
		}
	}
}

func (p *plugin) Allocate(_ context.Context, req *v1beta1.AllocateRequest) (*v1beta1.AllocateResponse, error) {
	resp := &v1beta1.AllocateResponse{}
	for _, creq := range req.ContainerRequests {
		cresp := &v1beta1.ContainerAllocateResponse{}
		seen := map[string]bool{}
		for _, id := range creq.DevicesIds {
			path, ok := p.ids[id]
			if !ok {
				return nil, status.Errorf(codes.InvalidArgument, "unknown device ID %q", id)
			}
			// Several shared IDs can point at one node; mount it once.
			if seen[path] {
				continue
			}
			seen[path] = true
			cresp.Devices = append(cresp.Devices, &v1beta1.DeviceSpec{
				HostPath:      path,
				ContainerPath: filepath.Join(p.prefix, path),
				Permissions:   "rw",
			})
		}
		resp.ContainerResponses = append(resp.ContainerResponses, cresp)
	}
	return resp, nil
}
