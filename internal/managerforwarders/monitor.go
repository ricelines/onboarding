package managerforwarders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Logger func(format string, args ...any)

type Config struct {
	ManagerContainerName string
	ForwarderImage       string
	ForwarderNamePrefix  string
	PollInterval         time.Duration
	Logger               Logger
}

type Monitor struct {
	cfg    Config
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	forwarders  map[int]string
	lastErr     error
	healthyOnce bool
}

type inspectContainer struct {
	ID              string `json:"Id"`
	NetworkSettings struct {
		Ports map[string][]inspectPortBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

type inspectPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

func Start(parent context.Context, cfg Config) (*Monitor, error) {
	if strings.TrimSpace(cfg.ManagerContainerName) == "" {
		return nil, errors.New("manager container name is required")
	}
	if strings.TrimSpace(cfg.ForwarderImage) == "" {
		return nil, errors.New("forwarder image is required")
	}
	if strings.TrimSpace(cfg.ForwarderNamePrefix) == "" {
		return nil, errors.New("forwarder name prefix is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 200 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(parent)
	monitor := &Monitor{
		cfg:        cfg,
		cancel:     cancel,
		done:       make(chan struct{}),
		forwarders: make(map[int]string),
	}
	go monitor.run(ctx)
	return monitor, nil
}

func (m *Monitor) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	<-m.done
	return m.LastError()
}

func (m *Monitor) Healthy() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healthyOnce && m.lastErr == nil
}

func (m *Monitor) LastError() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

func (m *Monitor) run(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	for {
		if err := m.reconcile(); err != nil {
			m.setError(err)
		}

		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) reconcile() error {
	ports, err := amberRouterPublishedPorts()
	if err != nil {
		return err
	}

	wanted := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		wanted[port] = struct{}{}
		if err := m.ensureForwarder(port); err != nil {
			return err
		}
	}
	m.removeUnwanted(wanted)

	m.mu.Lock()
	m.healthyOnce = true
	m.lastErr = nil
	m.mu.Unlock()
	return nil
}

func (m *Monitor) ensureForwarder(port int) error {
	m.mu.Lock()
	containerName, ok := m.forwarders[port]
	m.mu.Unlock()

	if ok {
		running, err := dockerContainerRunning(containerName)
		if err == nil && running {
			return nil
		}
		if err == nil {
			_ = removeContainer(containerName)
		}
		m.mu.Lock()
		delete(m.forwarders, port)
		m.mu.Unlock()
	}

	containerName = fmt.Sprintf("%s-%d", m.cfg.ForwarderNamePrefix, port)
	_ = removeContainer(containerName)
	if err := startForwarderContainer(m.cfg, containerName, port); err != nil {
		return err
	}
	if m.cfg.Logger != nil {
		m.cfg.Logger("manager forwarder started for port %d (%s)", port, containerName)
	}

	m.mu.Lock()
	m.forwarders[port] = containerName
	m.mu.Unlock()
	return nil
}

func (m *Monitor) removeUnwanted(wanted map[int]struct{}) {
	m.mu.Lock()
	var stale []string
	for port, containerName := range m.forwarders {
		if _, ok := wanted[port]; ok {
			continue
		}
		stale = append(stale, containerName)
		delete(m.forwarders, port)
	}
	m.mu.Unlock()

	for _, containerName := range stale {
		_ = removeContainer(containerName)
		if m.cfg.Logger != nil {
			m.cfg.Logger("manager forwarder removed (%s)", containerName)
		}
	}
}

func (m *Monitor) closeAll() {
	m.mu.Lock()
	current := make([]string, 0, len(m.forwarders))
	for _, containerName := range m.forwarders {
		current = append(current, containerName)
	}
	m.forwarders = make(map[int]string)
	m.mu.Unlock()

	for _, containerName := range current {
		_ = removeContainer(containerName)
	}
}

func (m *Monitor) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastErr != nil && m.lastErr.Error() == err.Error() {
		return
	}
	m.lastErr = err
	if m.cfg.Logger != nil {
		m.cfg.Logger("manager forwarder reconcile failed: %v", err)
	}
}

func amberRouterPublishedPorts() ([]int, error) {
	output, err := runDockerCommand(20*time.Second, "ps", "--filter", "label=com.docker.compose.service=amber-router", "--format", "{{.ID}}")
	if err != nil {
		return nil, fmt.Errorf("list amber-router containers: %w: %s", err, strings.TrimSpace(output))
	}
	containerIDs := splitNonEmptyLines(output)
	if len(containerIDs) == 0 {
		return nil, nil
	}

	args := append([]string{"inspect"}, containerIDs...)
	output, err = runDockerCommand(20*time.Second, args...)
	if err != nil {
		return nil, fmt.Errorf("inspect amber-router containers: %w: %s", err, strings.TrimSpace(output))
	}

	var containers []inspectContainer
	if err := json.Unmarshal([]byte(output), &containers); err != nil {
		return nil, fmt.Errorf("decode amber-router inspect output: %w", err)
	}

	seen := make(map[int]struct{})
	var ports []int
	for _, container := range containers {
		for _, bindings := range container.NetworkSettings.Ports {
			for _, binding := range bindings {
				port, err := strconv.Atoi(strings.TrimSpace(binding.HostPort))
				if err != nil {
					return nil, fmt.Errorf("parse host port %q for container %s: %w", binding.HostPort, container.ID, err)
				}
				if _, ok := seen[port]; ok {
					continue
				}
				seen[port] = struct{}{}
				ports = append(ports, port)
			}
		}
	}
	slices.Sort(ports)
	return ports, nil
}

func startForwarderContainer(cfg Config, containerName string, port int) error {
	args := forwarderDockerArgs(cfg, containerName, port)
	output, err := runDockerCommand(30*time.Second, args...)
	if err != nil {
		return fmt.Errorf("start forwarder container %s for port %d: %w: %s", containerName, port, err, strings.TrimSpace(output))
	}
	return nil
}

func forwarderDockerArgs(cfg Config, containerName string, port int) []string {
	return []string{
		"run", "--rm", "-d",
		"--name", containerName,
		"--network", "container:" + cfg.ManagerContainerName,
		"--entrypoint", "/app/onboarding-tcp-forwarder",
		cfg.ForwarderImage,
		"--listen", fmt.Sprintf("127.0.0.1:%d", port),
		"--target", fmt.Sprintf("host.docker.internal:%d", port),
	}
}

func dockerContainerRunning(containerName string) (bool, error) {
	output, err := runDockerCommand(20*time.Second, "inspect", "-f", "{{.State.Running}}", containerName)
	if err != nil {
		if strings.Contains(output, "No such object") || strings.Contains(output, "No such container") {
			return false, nil
		}
		return false, fmt.Errorf("inspect container %s: %w: %s", containerName, err, strings.TrimSpace(output))
	}
	return strings.TrimSpace(output) == "true", nil
}

func removeContainer(containerName string) error {
	output, err := runDockerCommand(20*time.Second, "rm", "-f", containerName)
	if err != nil && !strings.Contains(output, "No such container") {
		return fmt.Errorf("remove container %s: %w: %s", containerName, err, strings.TrimSpace(output))
	}
	return nil
}

func splitNonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func runDockerCommand(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}
