package main

import (
	"context"
	"encoding/json"
	"flag"
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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeconfig    string
	namespace     string
	dashboardSvc  string
	prometheusSvc string
	interval      time.Duration
	debug         bool
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file (uses in-cluster config if not set)")
	flag.StringVar(&namespace, "namespace", "ceph", "Kubernetes namespace for Endpoints")
	flag.StringVar(&dashboardSvc, "dashboard-service", "", "Service name for dashboard Endpoints")
	flag.StringVar(&prometheusSvc, "prometheus-service", "", "Service name for prometheus Endpoints")
	flag.DurationVar(&interval, "interval", 0, "polling interval (e.g. 30s, 1m); runs once if not set")
	flag.BoolVar(&debug, "debug", false, "enable debug logging")
}

func main() {
	flag.Parse()

	if debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
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
	if dashboardSvc != "" || prometheusSvc != "" {
		clientset, err = getKubeClient(kubeconfig)
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

	if dashboardSvc == "" && prometheusSvc == "" {
		return nil
	}

	if dashboardSvc != "" {
		if services.Dashboard == "" {
			return fmt.Errorf("dashboard service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Dashboard)
		if err != nil {
			return fmt.Errorf("failed to parse dashboard URL: %w", err)
		}
		if err := updateEndpoints(ctx, clientset, namespace, dashboardSvc, addr); err != nil {
			return fmt.Errorf("failed to update dashboard endpoints: %w", err)
		}
	}

	if prometheusSvc != "" {
		if services.Prometheus == "" {
			return fmt.Errorf("prometheus service URL not found in ceph mgr services")
		}
		addr, err := parseServiceURL(services.Prometheus)
		if err != nil {
			return fmt.Errorf("failed to parse prometheus URL: %w", err)
		}
		if err := updateEndpoints(ctx, clientset, namespace, prometheusSvc, addr); err != nil {
			return fmt.Errorf("failed to update prometheus endpoints: %w", err)
		}
	}

	return nil
}

type MgrServices struct {
	Dashboard  string `json:"dashboard"`
	Prometheus string `json:"prometheus"`
}

type EndpointAddress struct {
	IP   string
	Port int32
}

func getMgrServices(conn *rados.Conn) (*MgrServices, error) {
	cmd, err := json.Marshal(map[string]string{
		"prefix": "mgr services",
		"format": "json",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	buf, _, err := conn.MonCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("mon command: %w", err)
	}

	var services MgrServices
	if err := json.Unmarshal(buf, &services); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &services, nil
}

func parseServiceURL(rawURL string) (*EndpointAddress, error) {
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

	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("resolve hostname %s: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no IPs found for hostname: %s", host)
		}
		for _, resolvedIP := range ips {
			if resolvedIP.To4() != nil {
				ip = resolvedIP
				break
			}
		}
		if ip == nil {
			ip = ips[0]
		}
	}

	return &EndpointAddress{
		IP:   ip.String(),
		Port: int32(port),
	}, nil
}

func getKubeClient(kubeconfigPath string) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("build config from kubeconfig: %w", err)
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	return clientset, nil
}

func updateEndpoints(ctx context.Context, clientset *kubernetes.Clientset,
	namespace, serviceName string, addr *EndpointAddress) error {

	endpointsClient := clientset.CoreV1().Endpoints(namespace)

	existing, err := endpointsClient.Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("get endpoints: %w", err)
	}
	if err == nil && endpointsMatch(existing, addr) {
		slog.Debug("endpoints already up-to-date", "namespace", namespace, "service", serviceName)
		return nil
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: addr.IP,
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Port:     addr.Port,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		},
	}

	_, err = endpointsClient.Update(ctx, endpoints, metav1.UpdateOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = endpointsClient.Create(ctx, endpoints, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("create endpoints: %w", err)
			}
			slog.Info("created endpoints", "namespace", namespace, "service", serviceName, "ip", addr.IP, "port", addr.Port)
			return nil
		}
		return fmt.Errorf("update endpoints: %w", err)
	}

	slog.Info("updated endpoints", "namespace", namespace, "service", serviceName, "ip", addr.IP, "port", addr.Port)
	return nil
}

func endpointsMatch(ep *corev1.Endpoints, addr *EndpointAddress) bool {
	if len(ep.Subsets) != 1 {
		return false
	}
	subset := ep.Subsets[0]
	if len(subset.Addresses) != 1 || len(subset.Ports) != 1 {
		return false
	}
	return subset.Addresses[0].IP == addr.IP &&
		subset.Ports[0].Port == addr.Port &&
		subset.Ports[0].Protocol == corev1.ProtocolTCP
}
