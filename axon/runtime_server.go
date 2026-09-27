package axon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DefaultAxonPort is the default port used by a local Axon runtime.
const DefaultAxonPort = 50051

// ServerHandle owns a local runtime process or records a connection to an
// existing runtime. Process is nil when the handle did not spawn the runtime.
type ServerHandle struct {
	Endpoint string
	Process  *os.Process
	Cmd      *exec.Cmd
	LogFile  string
}

// Stop terminates a runtime process owned by this handle.
func (h *ServerHandle) Stop() {
	if h == nil || h.Process == nil {
		return
	}
	_ = h.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = h.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = h.Process.Kill()
		<-done
	}
	h.Process = nil
}

// StartServerOptions configures connection to, or startup of, an Axon runtime.
type StartServerOptions struct {
	Endpoint string
	LogFile  string
	Insecure *bool
	Timeout  time.Duration
}

// StartServer connects to endpoint or starts a local runtime when endpoint is
// empty. AXON_ENDPOINT is consulted before a local process is started.
func StartServer(endpoint string, logFile string, insecure *bool) (*ServerHandle, error) {
	return StartServerWithOptions(StartServerOptions{
		Endpoint: endpoint,
		LogFile:  logFile,
		Insecure: insecure,
	})
}

// StartServerWithOptions connects to an existing runtime or starts one local
// runtime process. Application membership and routing policy remain outside
// this generic process boundary.
func StartServerWithOptions(options StartServerOptions) (*ServerHandle, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("AXON_ENDPOINT"))
	}
	if endpoint != "" {
		host, port := parseRuntimeEndpoint(endpoint)
		if !waitForRuntimePort(host, port, timeout) {
			return nil, DendriteError{
				Message: fmt.Sprintf("cannot reach runtime at %s", endpoint),
				Code:    ErrCodeBridge,
			}
		}
		return &ServerHandle{Endpoint: endpoint}, nil
	}

	port, err := freeRuntimePort()
	if err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	bindAddress := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	endpoint = "http://" + bindAddress
	binary, err := runtimeBinary()
	if err != nil {
		return nil, err
	}

	logFile := strings.TrimSpace(options.LogFile)
	if logFile == "" {
		logFile = filepath.Join(runtimeLogDir(), "axon-runtime.log")
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return nil, DendriteError{
			Message: fmt.Sprintf("create runtime log directory: %v", err),
			Code:    ErrCodeIO,
		}
	}
	logHandle, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, DendriteError{
			Message: fmt.Sprintf("open runtime log %s: %v", logFile, err),
			Code:    ErrCodeIO,
		}
	}

	command := exec.Command(binary)
	enforceMTLS := "false"
	if options.Insecure != nil && !*options.Insecure {
		enforceMTLS = "true"
	}
	command.Env = append(
		os.Environ(),
		"AXON_BIND="+bindAddress,
		"AXON_ENFORCE_MTLS="+enforceMTLS,
	)
	command.Stdout = logHandle
	command.Stderr = logHandle
	if err := command.Start(); err != nil {
		_ = logHandle.Close()
		return nil, DendriteError{
			Message: fmt.Sprintf("start axon-runtime: %v", err),
			Code:    ErrCodeBridge,
		}
	}
	_ = logHandle.Close()

	if !waitForRuntimePort(host, port, timeout) {
		_ = command.Process.Kill()
		return nil, DendriteError{
			Message: fmt.Sprintf(
				"axon-runtime not ready within %s on %s (see %s)",
				timeout,
				bindAddress,
				logFile,
			),
			Code: ErrCodeBridge,
		}
	}
	return &ServerHandle{
		Endpoint: endpoint,
		Process:  command.Process,
		Cmd:      command,
		LogFile:  logFile,
	}, nil
}

func runtimeLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".axon", "logs")
}

func runtimeBinary() (string, error) {
	if binary := strings.TrimSpace(os.Getenv("AXON_RUNTIME_BIN")); binary != "" {
		if _, err := os.Stat(binary); err == nil {
			return binary, nil
		}
	}
	if binary, err := exec.LookPath("axon-runtime"); err == nil {
		return binary, nil
	}
	return "", DendriteError{
		Message: "axon-runtime not found; set AXON_RUNTIME_BIN or add it to PATH",
		Code:    ErrCodeBridge,
	}
}

func freeRuntimePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForRuntimePort(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func parseRuntimeEndpoint(endpoint string) (string, int) {
	raw := strings.TrimSpace(endpoint)
	if strings.HasPrefix(raw, "axon://") {
		raw = strings.TrimPrefix(raw, "axon://")
	} else {
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	}
	authority := strings.SplitN(raw, "/", 2)[0]
	if authority == "" || authority == "localhost" {
		return "127.0.0.1", DefaultAxonPort
	}
	host, portText, err := net.SplitHostPort(authority)
	if err == nil {
		var port int
		if _, scanErr := fmt.Sscanf(portText, "%d", &port); scanErr == nil {
			if host == "localhost" {
				host = "127.0.0.1"
			}
			return host, port
		}
	}
	return authority, DefaultAxonPort
}
