package main

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
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

type rawConfig struct {
	Debug                  *bool  `json:"debug,omitempty"`
	Interval               string `json:"interval,omitempty"`
	MaxConsecutiveFailures *int   `json:"maxConsecutiveFailures,omitempty"`
	Namespace              string `json:"namespace,omitempty"`
	ServiceName            string `json:"serviceName,omitempty"`
	DashboardSlice         string `json:"dashboardSlice,omitempty"`
	PrometheusSlice        string `json:"prometheusSlice,omitempty"`
}

const defaultInterval = 30 * time.Second

const defaultMaxConsecutiveFailures = 10

type config struct {
	debug           bool
	interval        time.Duration
	maxFailures     int
	namespace       string
	serviceName     string
	dashboardSlice  string
	prometheusSlice string
	cephID          string
	cephKey         string
}

func (c config) LogValue() slog.Value {
	cephKey := ""
	if c.cephKey != "" {
		cephKey = "[redacted]"
	}
	return slog.GroupValue(
		slog.Bool("debug", c.debug),
		slog.Duration("interval", c.interval),
		slog.Int("maxConsecutiveFailures", c.maxFailures),
		slog.String("namespace", c.namespace),
		slog.String("serviceName", c.serviceName),
		slog.String("dashboardSlice", c.dashboardSlice),
		slog.String("prometheusSlice", c.prometheusSlice),
		slog.String("cephID", c.cephID),
		slog.String("cephKey", cephKey),
	)
}

func loadConfig() (config, error) {
	var cephID string
	if data, err := os.ReadFile("/var/run/secrets/ceph/userID"); err == nil {
		cephID = strings.TrimSpace(string(data))
	}

	var cephKey string
	if data, err := os.ReadFile("/var/run/secrets/ceph/userKey"); err == nil {
		cephKey = strings.TrimSpace(string(data))
	}

	path := "/etc/ceph-mgr-endpoint-controller/config.json"
	if v := os.Getenv("CEPH_MGR_CONFIG_PATH"); v != "" {
		path = v
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config{
				interval:    defaultInterval,
				maxFailures: defaultMaxConsecutiveFailures,
				cephID:      cephID,
				cephKey:     cephKey,
			}, nil
		}
		return config{}, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()
	var raw rawConfig
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return config{}, fmt.Errorf("decode config file: %w", err)
	}
	interval := defaultInterval
	if raw.Interval != "" {
		parsed, err := time.ParseDuration(raw.Interval)
		if err != nil {
			return config{}, fmt.Errorf("invalid duration in config: %w", err)
		}
		if parsed <= 0 {
			return config{}, fmt.Errorf("interval must be positive: %s", raw.Interval)
		}
		interval = parsed
	}
	debug := false
	if raw.Debug != nil {
		debug = *raw.Debug
	}
	maxFailures := defaultMaxConsecutiveFailures
	if raw.MaxConsecutiveFailures != nil {
		if *raw.MaxConsecutiveFailures < 1 {
			return config{}, fmt.Errorf("maxConsecutiveFailures must be positive: %d", *raw.MaxConsecutiveFailures)
		}
		maxFailures = *raw.MaxConsecutiveFailures
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
		maxFailures:     maxFailures,
		namespace:       raw.Namespace,
		serviceName:     raw.ServiceName,
		dashboardSlice:  raw.DashboardSlice,
		prometheusSlice: raw.PrometheusSlice,
		cephID:          cephID,
		cephKey:         cephKey,
	}, nil
}

var version = "0.7.2"

const fieldManager = "ceph-mgr-endpoint-controller"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		major, minor, patch := rados.Version()
		fmt.Printf("ceph-mgr-endpoint-controller: %s\n", version)
		fmt.Printf("librados: %d.%d.%d\n", major, minor, patch)
		return
	}

	cfg, err := loadConfig()
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

	conn, err := connectRados(cfg)
	if err != nil {
		slog.Error("failed to set up ceph connection", "error", err)
		os.Exit(1)
	}
	defer func() { conn.Shutdown() }()
	connCephID, connCephKey := cfg.cephID, cfg.cephKey

	clientset, err := getKubeClient()
	if err != nil {
		slog.Error("failed to connect to kubernetes", "error", err)
		os.Exit(1)
	}

	consecutiveFailures := 0
	if err := run(ctx, cfg, conn, clientset); err != nil {
		consecutiveFailures++
		slog.Error("run failed", "error", err, "consecutiveFailures", consecutiveFailures)
		if consecutiveFailures >= cfg.maxFailures {
			slog.Error("too many consecutive failures, exiting", "failures", consecutiveFailures)
			os.Exit(1)
		}
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
					ticker.Reset(interval)
					slog.Info("interval changed", "interval", interval)
				}
				cfg = newCfg
			}

			credsChanged := cfg.cephID != connCephID || cfg.cephKey != connCephKey
			credsPresent := cfg.cephID != "" || cfg.cephKey != ""
			if credsChanged && credsPresent {
				slog.Info("ceph credentials changed, reconnecting")
				newConn, err := connectRados(cfg)
				if err != nil {
					slog.Error("failed to reconnect with new credentials, keeping existing connection", "error", err)
				} else {
					conn.Shutdown()
					conn = newConn
					connCephID, connCephKey = cfg.cephID, cfg.cephKey
				}
			}

			if err := run(ctx, cfg, conn, clientset); err != nil {
				consecutiveFailures++
				slog.Error("run failed", "error", err, "consecutiveFailures", consecutiveFailures)
				if consecutiveFailures >= cfg.maxFailures {
					slog.Error("too many consecutive failures, exiting", "failures", consecutiveFailures)
					os.Exit(1)
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

func connectRados(cfg config) (*rados.Conn, error) {
	var conn *rados.Conn
	var err error
	if cfg.cephID != "" {
		conn, err = rados.NewConnWithUser(cfg.cephID)
	} else {
		conn, err = rados.NewConn()
	}
	if err != nil {
		return nil, fmt.Errorf("create rados connection: %w", err)
	}
	connected := false
	defer func() {
		if !connected {
			conn.Shutdown()
		}
	}()

	if err := conn.ReadDefaultConfigFile(); err != nil {
		return nil, fmt.Errorf("read ceph config: %w", err)
	}

	if err := conn.ParseDefaultConfigEnv(); err != nil {
		return nil, fmt.Errorf("parse ceph args env: %w", err)
	}

	if cfg.cephKey != "" {
		if err := conn.SetConfigOption("key", cfg.cephKey); err != nil {
			return nil, fmt.Errorf("set ceph key: %w", err)
		}
	}

	slog.Debug("rados config", radosConfigAttrs(conn)...)

	if err := conn.Connect(); err != nil {
		slog.Error("failed to connect to cluster", append([]any{"error", err}, radosConfigAttrs(conn)...)...)
		return nil, fmt.Errorf("connect to cluster: %w", err)
	}

	connected = true
	return conn, nil
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

func run(ctx context.Context, cfg config, conn *rados.Conn, clientset *kubernetes.Clientset) error {
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

	var errs []error

	if cfg.dashboardSlice != "" {
		if err := syncEndpointSlice(ctx, cfg, clientset, cfg.dashboardSlice, "dashboard", services.Dashboard); err != nil {
			errs = append(errs, err)
		}
	}

	if cfg.prometheusSlice != "" {
		if err := syncEndpointSlice(ctx, cfg, clientset, cfg.prometheusSlice, "prometheus", services.Prometheus); err != nil {
			errs = append(errs, err)
		}
	}

	return stderrors.Join(errs...)
}

func syncEndpointSlice(ctx context.Context, cfg config, clientset *kubernetes.Clientset, sliceName, portName, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%s service URL not found in ceph mgr services", portName)
	}
	addr, err := parseServiceURL(rawURL)
	if err != nil {
		return fmt.Errorf("failed to parse %s URL: %w", portName, err)
	}
	if err := updateEndpointSlice(ctx, cfg, clientset, sliceName, portName, addr); err != nil {
		return fmt.Errorf("failed to update %s EndpointSlice: %w", portName, err)
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
	ip   net.IP
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
		ip:   ip,
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

var lastAppliedResourceVersion = map[string]string{}

func updateEndpointSlice(ctx context.Context, cfg config, clientset *kubernetes.Clientset, sliceName, portName string, addr *endpointAddress) error {
	svc, err := clientset.CoreV1().Services(cfg.namespace).Get(ctx, cfg.serviceName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("get Service for owner reference: %w", err)
		}
		slog.Warn("service not found, applying EndpointSlice without owner reference", "namespace", cfg.namespace, "service", cfg.serviceName)
		svc = nil
	}

	addressType := discoveryv1.AddressTypeIPv4
	if addr.ip.To4() == nil {
		addressType = discoveryv1.AddressTypeIPv6
	}

	slice := discoveryv1apply.EndpointSlice(sliceName, cfg.namespace).
		WithLabels(map[string]string{
			"kubernetes.io/service-name":             cfg.serviceName,
			"endpointslice.kubernetes.io/managed-by": fieldManager,
		}).
		WithAddressType(addressType).
		WithEndpoints(
			discoveryv1apply.Endpoint().
				WithAddresses(addr.ip.String()),
		).
		WithPorts(
			discoveryv1apply.EndpointPort().
				WithName(portName).
				WithPort(addr.port).
				WithProtocol(corev1.ProtocolTCP),
		)

	if svc != nil {
		slice = slice.WithOwnerReferences(
			applyconfigmetav1.OwnerReference().
				WithAPIVersion("v1").
				WithKind("Service").
				WithName(svc.Name).
				WithUID(svc.UID),
		)
	}

	applied, err := clientset.DiscoveryV1().EndpointSlices(cfg.namespace).Apply(ctx, slice, metav1.ApplyOptions{FieldManager: fieldManager})
	if err != nil {
		return fmt.Errorf("apply EndpointSlice: %w", err)
	}

	if applied.ResourceVersion != lastAppliedResourceVersion[sliceName] {
		slog.Info("applied EndpointSlice", "namespace", cfg.namespace, "name", sliceName, "ip", addr.ip, "port", addr.port)
	} else {
		slog.Debug("EndpointSlice already up-to-date", "namespace", cfg.namespace, "name", sliceName)
	}
	lastAppliedResourceVersion[sliceName] = applied.ResourceVersion
	return nil
}
