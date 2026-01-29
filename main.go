package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"syscall"
	"time"

	"github.com/ceph/go-ceph/rados"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryv1apply "k8s.io/client-go/applyconfigurations/discovery/v1"
	applyconfigmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

type rawConfig struct {
	Debug           *bool  `json:"debug,omitempty"`
	Interval        string `json:"interval,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	ServiceName     string `json:"serviceName,omitempty"`
	DashboardSlice  string `json:"dashboardSlice,omitempty"`
	PrometheusSlice string `json:"prometheusSlice,omitempty"`
}

type config struct {
	debug           bool
	interval        time.Duration
	namespace       string
	serviceName     string
	dashboardSlice  string
	prometheusSlice string
}

func loadConfig() (config, error) {
	path := getEnv("CEPH_MGR_CONFIG_PATH", "/etc/ceph-mgr-endpoint-controller.json")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config{}, nil
		}
		return config{}, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()
	var raw rawConfig
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return config{}, fmt.Errorf("decode config file: %w", err)
	}
	var interval time.Duration
	if raw.Interval != "" {
		parsed, err := time.ParseDuration(raw.Interval)
		if err != nil {
			return config{}, fmt.Errorf("invalid duration in config: %w", err)
		}
		if parsed < 0 {
			return config{}, fmt.Errorf("interval must be positive: %s", raw.Interval)
		}
		if parsed != 0 {
			interval = parsed
		}
	}
	debug := false
	if raw.Debug != nil {
		debug = *raw.Debug
	}
	if (raw.DashboardSlice != "" || raw.PrometheusSlice != "") && raw.Namespace == "" {
		return config{}, fmt.Errorf("namespace is required when creating EndpointSlices")
	}
	if (raw.DashboardSlice != "" || raw.PrometheusSlice != "") && raw.ServiceName == "" {
		return config{}, fmt.Errorf("service name is required when creating EndpointSlices")
	}
	return config{
		debug:           debug,
		interval:        interval,
		namespace:       raw.Namespace,
		serviceName:     raw.ServiceName,
		dashboardSlice:  raw.DashboardSlice,
		prometheusSlice: raw.PrometheusSlice,
	}, nil
}

var (
	cephID = getEnv("CEPH_ID", "admin")
	cfg    config
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "build-check":
			os.Exit(runBuildCheck())
		case "check":
			os.Exit(runCheck())
		}
	}

	var err error
	cfg, err = loadConfig()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if cfg.debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	interval := cfg.interval

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	conn, err := rados.NewConnWithUser(cephID)
	if err != nil {
		slog.Error("failed to create rados connection", "error", err)
		os.Exit(1)
	}
	defer conn.Shutdown()

	if err := conn.ReadDefaultConfigFile(); err != nil {
		slog.Error("failed to read ceph config", "error", err)
		os.Exit(1)
	}

	if err := conn.ParseDefaultConfigEnv(); err != nil {
		slog.Error("failed to parse ceph args env", "error", err)
		os.Exit(1)
	}

	if cephKey := os.Getenv("CEPH_KEY"); cephKey != "" {
		if err := conn.SetConfigOption("key", cephKey); err != nil {
			slog.Error("failed to set ceph key", "error", err)
			os.Exit(1)
		}
	}

	slog.Debug("rados config", radosConfigAttrs(conn)...)

	if err := conn.Connect(); err != nil {
		slog.Error("failed to connect to cluster", append([]any{"error", err}, radosConfigAttrs(conn)...)...)
		os.Exit(1)
	}

	var clientset *kubernetes.Clientset
	if cfg.dashboardSlice != "" || cfg.prometheusSlice != "" {
		var err error
		clientset, err = getKubeClient()
		if err != nil {
			slog.Error("failed to connect to kubernetes", "error", err)
			os.Exit(1)
		}
	}

	if err := run(ctx, conn, clientset); err != nil {
		slog.Error("run failed", "error", err)
		if interval == 0 {
			os.Exit(1)
		}
	}

	if interval == 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newCfg, err := loadConfig()
			if err != nil {
				slog.Error("failed to reload config, using previous configuration", "error", err)
			} else if !reflect.DeepEqual(cfg, newCfg) {
				slog.Debug("configuration changed", "from", cfg, "to", newCfg)
				if newCfg.debug != cfg.debug {
					slog.Info("log level changed", "debug", newCfg.debug)
					if newCfg.debug {
						slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
					} else {
						slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})))
					}
				}
				if newCfg.interval != cfg.interval {
					interval = newCfg.interval
					if interval == 0 {
						slog.Info("interval disabled")
						return
					}
					ticker.Reset(interval)
					slog.Info("interval changed", "interval", interval)
				}
				cfg = newCfg
			}

			if err := run(ctx, conn, clientset); err != nil {
				slog.Error("run failed", "error", err)
			}
		}
	}
}

func radosConfigAttrs(conn *rados.Conn) []any {
	var attrs []any
	for _, key := range []string{"name", "keyring", "mon_host"} {
		if val, err := conn.GetConfigOption(key); err == nil {
			attrs = append(attrs, key, val)
		}
	}
	return attrs
}

func runBuildCheck() int {
	fmt.Println("Build check:")

	major, minor, patch := rados.Version()
	fmt.Printf("  [PASS] librados: %d.%d.%d\n", major, minor, patch)

	return 0
}

func runCheck() int {
	fmt.Println("Deployment check:")

	passed := 0
	failed := 0
	skipped := 0

	major, minor, patch := rados.Version()
	fmt.Printf("  [PASS] librados: %d.%d.%d\n", major, minor, patch)
	passed++

	conn, err := rados.NewConnWithUser(cephID)
	if err != nil {
		fmt.Printf("  [FAIL] Ceph config readable: %v\n", err)
		failed++
		fmt.Printf("\nResult: %d/%d checks passed, %d failed, %d skipped\n",
			passed, passed+failed+skipped, failed, skipped)
		return 1
	}
	defer conn.Shutdown()

	if err := conn.ReadDefaultConfigFile(); err != nil {
		fmt.Printf("  [FAIL] Ceph config readable: %v\n", err)
		failed++
	} else {
		fmt.Println("  [PASS] Ceph config readable")
		passed++
	}

	var cephConnected bool
	if failed > 0 {
		fmt.Println("  [SKIP] Ceph cluster connection (no config)")
		skipped++
	} else if err := conn.Connect(); err != nil {
		fmt.Printf("  [FAIL] Ceph cluster connection: %v\n", err)
		for _, key := range []string{"name", "keyring", "mon_host"} {
			if val, err := conn.GetConfigOption(key); err == nil {
				fmt.Printf("         %s = %s\n", key, val)
			}
		}
		failed++
	} else {
		fmt.Println("  [PASS] Ceph cluster connection")
		passed++
		cephConnected = true
	}

	if !cephConnected {
		fmt.Println("  [SKIP] Mon command execution (no connection)")
		skipped++
	} else if _, err := getMgrServices(conn); err != nil {
		fmt.Printf("  [FAIL] Mon command execution: %v\n", err)
		failed++
	} else {
		fmt.Println("  [PASS] Mon command execution")
		passed++
	}

	var k8sConfig *rest.Config
	k8sConfig, err = rest.InClusterConfig()
	if err != nil {
		fmt.Printf("  [FAIL] Kubernetes in-cluster config: %v\n", err)
		failed++
	} else {
		fmt.Println("  [PASS] Kubernetes in-cluster config")
		passed++
	}

	if k8sConfig == nil {
		fmt.Println("  [SKIP] Kubernetes API (no config)")
		skipped++
	} else {
		clientset, err := kubernetes.NewForConfig(k8sConfig)
		if err != nil {
			fmt.Printf("  [FAIL] Kubernetes API: %v\n", err)
			failed++
		} else if _, err := clientset.Discovery().ServerVersion(); err != nil {
			fmt.Printf("  [FAIL] Kubernetes API: %v\n", err)
			failed++
		} else {
			fmt.Println("  [PASS] Kubernetes API")
			passed++
		}
	}

	fmt.Printf("\nResult: %d/%d checks passed, %d failed, %d skipped\n",
		passed, passed+failed+skipped, failed, skipped)

	if failed > 0 {
		return 1
	}
	return 0
}

func run(ctx context.Context, conn *rados.Conn, clientset *kubernetes.Clientset) error {
	services, err := getMgrServices(conn)
	if err != nil {
		return fmt.Errorf("failed to get mgr services: %w", err)
	}

	if services.Dashboard != "" {
		slog.Debug("discovered service", "service", "dashboard", "url", services.Dashboard)
	}
	if services.Prometheus != "" {
		slog.Debug("discovered service", "service", "prometheus", "url", services.Prometheus)
	}

	if cfg.dashboardSlice == "" && cfg.prometheusSlice == "" {
		return nil
	}

	if cfg.dashboardSlice != "" {
		if services.Dashboard == "" {
			return fmt.Errorf("dashboard service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Dashboard)
		if err != nil {
			return fmt.Errorf("failed to parse dashboard URL: %w", err)
		}
		if err := updateEndpointSlice(ctx, clientset, cfg.dashboardSlice, "dashboard", addr); err != nil {
			return fmt.Errorf("failed to update dashboard EndpointSlice: %w", err)
		}
	}

	if cfg.prometheusSlice != "" {
		if services.Prometheus == "" {
			return fmt.Errorf("prometheus service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Prometheus)
		if err != nil {
			return fmt.Errorf("failed to parse prometheus URL: %w", err)
		}
		if err := updateEndpointSlice(ctx, clientset, cfg.prometheusSlice, "prometheus", addr); err != nil {
			return fmt.Errorf("failed to update prometheus EndpointSlice: %w", err)
		}
	}

	return nil
}

type monCommand struct {
	Prefix string `json:"prefix"`
	Format string `json:"format"`
}

type mgrServices struct {
	Dashboard  string `json:"dashboard"`
	Prometheus string `json:"prometheus"`
}

type endpointAddress struct {
	ip   string
	port int32
}

var mgrServicesCommand = monCommand{Prefix: "mgr services", Format: "json"}

func getMgrServices(conn *rados.Conn) (*mgrServices, error) {
	cmd, err := json.Marshal(mgrServicesCommand)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	buf, info, err := conn.MonCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("mon command: %w", err)
	}
	if info != "" {
		slog.Debug("mon command info", "info", info)
	}

	var services mgrServices
	if err := json.Unmarshal(buf, &services); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &services, nil
}

func parseServiceURL(rawURL string) (*endpointAddress, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	host := u.Hostname()
	portStr := u.Port()

	if portStr == "" {
		switch u.Scheme {
		case "https":
			portStr = "443"
		case "http":
			portStr = "80"
		default:
			return nil, fmt.Errorf("no port specified and unknown scheme: %s", u.Scheme)
		}
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port out of range: %d", port)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("expected IP address, got hostname: %s", host)
	}

	return &endpointAddress{
		ip:   ip.String(),
		port: int32(port),
	}, nil
}

func getKubeClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	return clientset, nil
}

func updateEndpointSlice(ctx context.Context, clientset *kubernetes.Clientset, sliceName, portName string, addr *endpointAddress) error {
	sliceClient := clientset.DiscoveryV1().EndpointSlices(cfg.namespace)

	existing, err := sliceClient.Get(ctx, sliceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("get EndpointSlice: %w", err)
	}
	if err == nil && endpointSliceMatches(existing, portName, addr) {
		slog.Debug("EndpointSlice already up-to-date", "namespace", cfg.namespace, "name", sliceName)
		return nil
	}

	slice := discoveryv1apply.EndpointSlice(sliceName, cfg.namespace).
		WithLabels(map[string]string{
			"kubernetes.io/service-name": cfg.serviceName,
		}).
		WithAddressType(discoveryv1.AddressTypeIPv4).
		WithEndpoints(
			discoveryv1apply.Endpoint().
				WithAddresses(addr.ip),
		).
		WithPorts(
			discoveryv1apply.EndpointPort().
				WithName(portName).
				WithPort(addr.port).
				WithProtocol(corev1.ProtocolTCP),
		)

	if svc, err := clientset.CoreV1().Services(cfg.namespace).Get(ctx, cfg.serviceName, metav1.GetOptions{}); err != nil {
		slog.Warn("failed to get service for owner reference", "namespace", cfg.namespace, "service", cfg.serviceName, "error", err)
	} else {
		slice = slice.WithOwnerReferences(
			applyconfigmetav1.OwnerReference().
				WithAPIVersion("v1").
				WithKind("Service").
				WithName(svc.Name).
				WithUID(svc.UID),
		)
	}

	_, err = sliceClient.Apply(ctx, slice, metav1.ApplyOptions{FieldManager: "ceph-mgr-endpoint-controller"})
	if err != nil {
		return fmt.Errorf("apply EndpointSlice: %w", err)
	}

	slog.Info("applied EndpointSlice", "namespace", cfg.namespace, "name", sliceName, "ip", addr.ip, "port", addr.port)
	return nil
}

func endpointSliceMatches(slice *discoveryv1.EndpointSlice, portName string, addr *endpointAddress) bool {
	if slice.Labels["kubernetes.io/service-name"] != cfg.serviceName {
		return false
	}
	if slice.AddressType != discoveryv1.AddressTypeIPv4 {
		return false
	}
	if len(slice.Endpoints) != 1 || len(slice.Endpoints[0].Addresses) != 1 {
		return false
	}
	if slice.Endpoints[0].Addresses[0] != addr.ip {
		return false
	}
	if len(slice.Ports) != 1 {
		return false
	}
	port := slice.Ports[0]
	if port.Name == nil || *port.Name != portName {
		return false
	}
	if port.Port == nil || *port.Port != addr.port {
		return false
	}
	if port.Protocol == nil || *port.Protocol != corev1.ProtocolTCP {
		return false
	}
	return true
}
