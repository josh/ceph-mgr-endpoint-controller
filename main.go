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
	"strconv"
	"syscall"
	"time"

	"github.com/ceph/go-ceph/rados"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string) bool {
	value := os.Getenv(key)
	return value != "" && value != "0" && value != "false"
}

func getEnvDuration(key string) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		slog.Error("invalid duration", "env", key, "value", value, "error", err)
		return 0
	}
	return d
}

var (
	namespace       = getEnv("CEPH_MGR_NAMESPACE", "")
	serviceName     = getEnv("CEPH_MGR_SERVICE_NAME", "")
	dashboardSlice  = getEnv("CEPH_MGR_DASHBOARD_SLICE", "")
	prometheusSlice = getEnv("CEPH_MGR_PROMETHEUS_SLICE", "")
	interval        = getEnvDuration("CEPH_MGR_INTERVAL")
	debug           = getEnvBool("CEPH_MGR_DEBUG")
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

	if debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	if (dashboardSlice != "" || prometheusSlice != "") && namespace == "" {
		slog.Error("CEPH_MGR_NAMESPACE is required when creating EndpointSlices")
		os.Exit(1)
	}

	if (dashboardSlice != "" || prometheusSlice != "") && serviceName == "" {
		slog.Error("CEPH_MGR_SERVICE_NAME is required when creating EndpointSlices")
		os.Exit(1)
	}

	conn, err := rados.NewConn()
	if err != nil {
		slog.Error("failed to create rados connection", "error", err)
		os.Exit(1)
	}
	defer conn.Shutdown()

	if err := conn.ReadDefaultConfigFile(); err != nil {
		slog.Error("failed to read ceph config", "error", err)
		os.Exit(1)
	}

	if err := conn.Connect(); err != nil {
		slog.Error("failed to connect to cluster", "error", err)
		os.Exit(1)
	}

	var clientset *kubernetes.Clientset
	if dashboardSlice != "" || prometheusSlice != "" {
		clientset, err = getKubeClient()
		if err != nil {
			slog.Error("failed to connect to kubernetes", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
			if err := run(ctx, conn, clientset); err != nil {
				slog.Error("run failed", "error", err)
			}
		}
	}
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

	conn, err := rados.NewConn()
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

	if dashboardSlice == "" && prometheusSlice == "" {
		return nil
	}

	if dashboardSlice != "" {
		if services.Dashboard == "" {
			return fmt.Errorf("dashboard service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Dashboard)
		if err != nil {
			return fmt.Errorf("failed to parse dashboard URL: %w", err)
		}
		if err := updateEndpointSlice(ctx, clientset, dashboardSlice, "dashboard", addr); err != nil {
			return fmt.Errorf("failed to update dashboard EndpointSlice: %w", err)
		}
	}

	if prometheusSlice != "" {
		if services.Prometheus == "" {
			return fmt.Errorf("prometheus service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Prometheus)
		if err != nil {
			return fmt.Errorf("failed to parse prometheus URL: %w", err)
		}
		if err := updateEndpointSlice(ctx, clientset, prometheusSlice, "prometheus", addr); err != nil {
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
	sliceClient := clientset.DiscoveryV1().EndpointSlices(namespace)

	existing, err := sliceClient.Get(ctx, sliceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("get EndpointSlice: %w", err)
	}
	if err == nil && endpointSliceMatches(existing, portName, addr) {
		slog.Debug("EndpointSlice already up-to-date", "namespace", namespace, "name", sliceName)
		return nil
	}

	protocol := corev1.ProtocolTCP
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sliceName,
			Namespace: namespace,
			Labels: map[string]string{
				"kubernetes.io/service-name": serviceName,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{addr.ip},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{
				Name:     &portName,
				Port:     &addr.port,
				Protocol: &protocol,
			},
		},
	}

	_, err = sliceClient.Update(ctx, slice, metav1.UpdateOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = sliceClient.Create(ctx, slice, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("create EndpointSlice: %w", err)
			}
			slog.Info("created EndpointSlice", "namespace", namespace, "name", sliceName, "ip", addr.ip, "port", addr.port)
			return nil
		}
		return fmt.Errorf("update EndpointSlice: %w", err)
	}

	slog.Info("updated EndpointSlice", "namespace", namespace, "name", sliceName, "ip", addr.ip, "port", addr.port)
	return nil
}

func endpointSliceMatches(slice *discoveryv1.EndpointSlice, portName string, addr *endpointAddress) bool {
	if slice.Labels["kubernetes.io/service-name"] != serviceName {
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
