// Command dri-device-plugin advertises DRM render nodes as a Kubernetes
// extended resource so a non-privileged pod can request GPU access.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

var logger = zerolog.New(os.Stderr).With().Timestamp().Logger()

var (
	dnsSubdomain      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	qualifiedName     = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)
	unsafeSocketChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

func validateResource(r string) error {
	domain, name, ok := strings.Cut(r, "/")
	if !ok || strings.Contains(name, "/") {
		return fmt.Errorf("resource %q must be <dns-subdomain>/<name>", r)
	}
	if len(domain) > 253 || !dnsSubdomain.MatchString(domain) {
		return fmt.Errorf("resource %q: %q is not a valid DNS subdomain", r, domain)
	}
	if len(name) > 63 || !qualifiedName.MatchString(name) {
		return fmt.Errorf("resource %q: %q is not a valid resource name", r, name)
	}
	return nil
}

func socketName(resource string) string {
	return "rake-pro-dri-" + unsafeSocketChars.ReplaceAllString(resource, "_") + ".sock"
}

func main() {
	resource := flag.String("resource", "rake-pro.github.io/dri", "extended resource name, <dns-subdomain>/<name>")
	devices := flag.String("devices", "/dev/dri/renderD*", "glob of device nodes to advertise")
	count := flag.Int("count", 4, "how many pods may share each device")
	pluginDir := flag.String("plugin-dir", "/var/lib/kubelet/device-plugins", "kubelet device plugin directory")
	prefix := flag.String("container-path-prefix", "", "prefix prepended to the host path to form the container path")
	interval := flag.Duration("health-interval", 30*time.Second, "device health and kubelet restart check interval")
	flag.Parse()

	if err := validateResource(*resource); err != nil {
		logger.Fatal().Err(err).Msg("invalid -resource")
	}
	if *count < 1 {
		logger.Fatal().Int("count", *count).Msg("-count must be at least 1")
	}
	if *interval <= 0 {
		logger.Fatal().Dur("health_interval", *interval).Msg("-health-interval must be positive")
	}
	if _, err := filepath.Match(*devices, ""); err != nil {
		logger.Fatal().Err(err).Str("devices", *devices).Msg("invalid -devices glob")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	paths := waitForDevices(ctx, *devices, *interval)
	if paths == nil {
		return
	}
	p, err := newPlugin(paths, *count, *prefix)
	if err != nil {
		logger.Fatal().Err(err).Msg("build device list")
	}
	if err := run(ctx, p, *pluginDir, *resource, *interval); err != nil {
		logger.Fatal().Err(err).Msg("device plugin failed")
	}
	logger.Info().Msg("shut down")
}

// waitForDevices globs until at least one device matches. It returns nil if
// ctx is cancelled first.
func waitForDevices(ctx context.Context, pattern string, interval time.Duration) []string {
	for {
		paths, _ := filepath.Glob(pattern) // pattern was validated in main
		if len(paths) > 0 {
			logger.Info().Strs("devices", paths).Msg("found devices")
			return paths
		}
		logger.Warn().Str("devices", pattern).Dur("retry_in", interval).Msg("no devices match, retrying")
		if !sleep(ctx, interval) {
			return nil
		}
	}
}

// run serves the plugin and keeps it registered until ctx is cancelled.
func run(ctx context.Context, p *plugin, pluginDir, resource string, interval time.Duration) error {
	sockName := socketName(resource)
	sockPath := filepath.Join(pluginDir, sockName)
	kubeletSock := filepath.Join(pluginDir, "kubelet.sock")
	for {
		srv, err := serve(p, sockPath)
		if err != nil {
			return err
		}
		if err := register(ctx, kubeletSock, sockName, resource); err != nil {
			logger.Error().Err(err).Str("kubelet_socket", kubeletSock).Dur("retry_in", interval).Msg("kubelet registration failed")
			sleep(ctx, interval)
		} else {
			logger.Info().Str("resource", resource).Str("socket", sockPath).Int("device_ids", len(p.ids)).Msg("registered with kubelet")
			watch(ctx, p, sockPath, interval)
		}
		srv.Stop()
		if ctx.Err() != nil {
			if err := os.Remove(sockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				logger.Error().Err(err).Str("socket", sockPath).Msg("remove socket")
			}
			return nil
		}
	}
}

func serve(p *plugin, sockPath string) (*grpc.Server, error) {
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", sockPath, err)
	}
	lis, err := net.Listen("unix", sockPath)
	if errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("create socket %s: %w (the kubelet plugin dir is root-owned 0755 by default: run as root or make the dir group-writable)", sockPath, err)
	}
	if err != nil {
		return nil, fmt.Errorf("create socket %s: %w", sockPath, err)
	}
	srv := grpc.NewServer()
	v1beta1.RegisterDevicePluginServer(srv, p)
	go func() {
		if err := srv.Serve(lis); err != nil {
			logger.Error().Err(err).Msg("grpc server exited")
		}
	}()
	return srv, nil
}

func register(ctx context.Context, kubeletSock, endpoint, resource string) error {
	conn, err := grpc.NewClient("unix:"+kubeletSock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = v1beta1.NewRegistrationClient(conn).Register(ctx, &v1beta1.RegisterRequest{
		Version:      v1beta1.Version,
		Endpoint:     endpoint,
		ResourceName: resource,
	})
	return err
}

// watch checks device health every interval and returns when ctx is
// cancelled or our socket disappears (kubelet wipes the dir on restart).
func watch(ctx context.Context, p *plugin, sockPath string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p.checkHealth()
		if _, err := os.Stat(sockPath); errors.Is(err, fs.ErrNotExist) {
			logger.Warn().Str("socket", sockPath).Msg("socket removed, kubelet likely restarted; re-registering")
			return
		}
	}
}

// sleep waits for d and reports false if ctx was cancelled first.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
