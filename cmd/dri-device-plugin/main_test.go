package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestDeviceList(t *testing.T) {
	p, err := newPlugin([]string{"/dev/dri/renderD128", "/dev/dri/renderD129"}, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	devs, _ := p.devices()
	want := []string{
		"renderD128-0", "renderD128-1", "renderD128-2",
		"renderD129-0", "renderD129-1", "renderD129-2",
	}
	if len(devs) != len(want) {
		t.Fatalf("got %d devices, want %d", len(devs), len(want))
	}
	for i, d := range devs {
		if d.ID != want[i] || d.Health != v1beta1.Healthy {
			t.Errorf("device %d = %s/%s, want %s/Healthy", i, d.ID, d.Health, want[i])
		}
	}
	if got := p.ids["renderD129-2"]; got != "/dev/dri/renderD129" {
		t.Errorf("renderD129-2 maps to %q", got)
	}
}

func TestDuplicateIDs(t *testing.T) {
	if _, err := newPlugin([]string{"/a/renderD128", "/b/renderD128"}, 1, ""); err == nil {
		t.Fatal("expected duplicate ID error")
	}
}

func TestAllocate(t *testing.T) {
	p, err := newPlugin([]string{"/dev/dri/renderD128", "/dev/dri/renderD129"}, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Allocate(context.Background(), &v1beta1.AllocateRequest{
		ContainerRequests: []*v1beta1.ContainerAllocateRequest{
			{DevicesIds: []string{"renderD128-0", "renderD128-3", "renderD129-1"}},
			{DevicesIds: []string{"renderD129-2"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ContainerResponses) != 2 {
		t.Fatalf("got %d container responses", len(resp.ContainerResponses))
	}
	first := resp.ContainerResponses[0].Devices
	if len(first) != 2 || first[0].HostPath != "/dev/dri/renderD128" || first[1].HostPath != "/dev/dri/renderD129" {
		t.Fatalf("container 0 not deduped: %v", first)
	}
	for _, d := range first {
		if d.ContainerPath != d.HostPath || d.Permissions != "rw" {
			t.Errorf("bad spec %v", d)
		}
	}
	if second := resp.ContainerResponses[1].Devices; len(second) != 1 || second[0].HostPath != "/dev/dri/renderD129" {
		t.Errorf("container 1 = %v", second)
	}

	if _, err := p.Allocate(context.Background(), &v1beta1.AllocateRequest{
		ContainerRequests: []*v1beta1.ContainerAllocateRequest{{DevicesIds: []string{"renderD130-0"}}},
	}); err == nil {
		t.Error("expected error for unknown ID")
	}
}

func TestAllocatePrefix(t *testing.T) {
	p, err := newPlugin([]string{"/dev/dri/renderD128"}, 1, "/host")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Allocate(context.Background(), &v1beta1.AllocateRequest{
		ContainerRequests: []*v1beta1.ContainerAllocateRequest{{DevicesIds: []string{"renderD128-0"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.ContainerResponses[0].Devices[0].ContainerPath; got != "/host/dev/dri/renderD128" {
		t.Errorf("container path = %q", got)
	}
}

func TestHealth(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := newPlugin([]string{dev}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	_, changed := p.devices()
	p.checkHealth()
	select {
	case <-changed:
		t.Fatal("unchanged health signalled a change")
	default:
	}

	if err := os.Remove(dev); err != nil {
		t.Fatal(err)
	}
	p.checkHealth()
	select {
	case <-changed:
	default:
		t.Fatal("health change not signalled")
	}
	devs, _ := p.devices()
	for _, d := range devs {
		if d.Health != v1beta1.Unhealthy {
			t.Errorf("%s = %s, want Unhealthy", d.ID, d.Health)
		}
	}
}

func TestValidateResource(t *testing.T) {
	valid := []string{"rake-pro.github.io/dri", "example.com/gpu", "a/b", "x.y/render_node.v2"}
	invalid := []string{"", "dri", "/dri", "example.com/", "Example.com/dri", "-bad.com/dri", "example.com/a/b", "example.com/-dri", "exa_mple.com/dri"}
	for _, r := range valid {
		if err := validateResource(r); err != nil {
			t.Errorf("%q: unexpected error %v", r, err)
		}
	}
	for _, r := range invalid {
		if err := validateResource(r); err == nil {
			t.Errorf("%q: expected error", r)
		}
	}
}

func TestSocketName(t *testing.T) {
	cases := map[string]string{
		"rake-pro.github.io/dri": "rake-pro-dri-rake-pro.github.io_dri.sock",
		"example.com/gpu_x":      "rake-pro-dri-example.com_gpu_x.sock",
		"a b/c:d":                "rake-pro-dri-a_b_c_d.sock",
	}
	for in, want := range cases {
		if got := socketName(in); got != want {
			t.Errorf("socketName(%q) = %q, want %q", in, got, want)
		}
	}
}
