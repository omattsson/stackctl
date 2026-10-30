package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/spf13/cobra"
)

const (
	// portForwardMinLocal is the lowest explicit LOCAL port (no privileged
	// ports).
	portForwardMinLocal = 1025
	// portForwardTries is how many ports after the default LOCAL port the
	// command tries before it asks the operating system for a free port.
	portForwardTries = 20
	// portForwardShutdownWait bounds the wait for open tunnels at the end.
	portForwardShutdownWait = 3 * time.Second
)

var stackPortForwardCmd = &cobra.Command{
	Use:   "port-forward <name|id> <service> [LOCAL:]REMOTE",
	Short: "Forward a local port to a service of a stack",
	Long: `Open a local port that forwards to a Kubernetes service of a stack,
through the k8s-stack-manager server (GET
/api/v1/stack-instances/:id/port-forward). You need no kubeconfig and no
cluster access: the server checks your access to the stack.

REMOTE is the port of the service (the service port, not the container
port). LOCAL is the local port:
  - LOCAL must be 0 or from 1025 to 65535. 0 selects any free port.
  - Without LOCAL, the local port is REMOTE when REMOTE is above 1024, else
    REMOTE + 10000 (for example 80 becomes 10080). When that port is in
    use, the command tries the next 20 ports, then any free port.
  - An explicit LOCAL port that is in use stops the command.

Each local connection opens its own tunnel, so one failed connection does
not stop the others. stdout shows the address, for example
"Forwarding from 127.0.0.1:3306 -> mysql:3306"; -q prints only
"host:port". stderr shows one line when a connection opens and one when it
closes (bytes, duration, close reason). -o is ignored. Ctrl-C closes all
connections and exits 0.

Errors that stop the command (exit 1): no access (401 after a session
renewal, 403), an unknown stack, service or port, the feature or the
cluster switch turned off (404, 403), a stack that is not deployed (409),
and a revoked session or permission during a tunnel (close 1008). API keys
are not allowed by default: run 'stackctl login' and use a login session.
Errors that close only one connection: the tunnel limit (429), no ready pod
(503), a failed pod dial (502), the server time limit (4001), the idle
timeout (4002), a pod stream error (4502) and network errors.

Security:
  - Anyone who can reach the local port uses your access to the stack.
    On a shared machine, other local users can connect to it.
  - A local port that forwards to an HTTP service can be reached by web
    pages in your browser through DNS rebinding. Stop the command when you
    do not need it.
  - --address with an address that is not loopback (for example 0.0.0.0)
    opens the port to the network. The command writes a warning.
  - --insecure is refused: without TLS verification, a man in the middle
    can read the tunnel data (for example database passwords). Add
    --allow-insecure-stream to accept that risk.

Examples:
  # Forward a database, then connect with the client
  stackctl stack port-forward my-stack mysql 3306
  mysql -h 127.0.0.1 -P 3306 -u root -p

  # A web service on port 80 (local port 10080)
  stackctl stack port-forward my-stack frontend 80

  # Any free local port; a script reads host:port from stdout
  stackctl stack port-forward my-stack mysql 0:3306 -q`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		if parent == nil {
			parent = context.Background()
		}
		ctx, stop := signal.NotifyContext(parent, os.Interrupt)
		defer stop()
		return runPortForward(ctx, cmd, args)
	},
}

func init() {
	stackPortForwardCmd.Flags().String("address", "127.0.0.1", "Local addresses to listen on (comma-separated IP addresses or localhost)")
	stackPortForwardCmd.Flags().Bool("allow-insecure-stream", false, "Allow port-forward with --insecure (the tunnel data is not protected against a man in the middle)")
	stackCmd.AddCommand(stackPortForwardCmd)
}

// portSpec is the parsed [LOCAL:]REMOTE argument.
type portSpec struct {
	local  int
	remote int
	// explicit is true when LOCAL was given (also 0).
	explicit bool
}

// parsePortSpec parses [LOCAL:]REMOTE (see the help of stack port-forward).
func parsePortSpec(s string) (portSpec, error) {
	localStr, remoteStr, hasLocal := strings.Cut(strings.TrimSpace(s), ":")
	if !hasLocal {
		remoteStr, localStr = localStr, ""
	}
	remote, err := strconv.Atoi(remoteStr)
	if err != nil || remote < 1 || remote > 65535 {
		return portSpec{}, fmt.Errorf("invalid port %q: REMOTE must be a number from 1 to 65535", s)
	}
	if !hasLocal {
		local := remote
		if remote < portForwardMinLocal {
			local = remote + 10000
		}
		return portSpec{local: local, remote: remote}, nil
	}
	local, err := strconv.Atoi(localStr)
	if err != nil || (local != 0 && (local < portForwardMinLocal || local > 65535)) {
		return portSpec{}, fmt.Errorf("invalid port %q: LOCAL must be 0 (any free port) or a number from %d to 65535", s, portForwardMinLocal)
	}
	return portSpec{local: local, remote: remote, explicit: true}, nil
}

// parseAddresses splits the --address value. It accepts IP addresses and
// "localhost".
func parseAddresses(raw string) ([]string, error) {
	var addrs []string
	for _, a := range strings.Split(raw, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a != "localhost" && net.ParseIP(a) == nil {
			return nil, fmt.Errorf("invalid --address %q: use an IP address or localhost", a)
		}
		addrs = append(addrs, a)
	}
	if len(addrs) == 0 {
		return nil, errors.New("--address must name at least one address")
	}
	return addrs, nil
}

func isLoopback(addr string) bool {
	if addr == "localhost" {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

// isAddrInUse reports a bind error because the port is in use.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || isAddrInUseOS(err)
}

// bindError is a failed bind of one address.
type bindError struct {
	addr string
	err  error
}

func (e *bindError) Error() string { return "listening on " + e.addr + ": " + e.err.Error() }
func (e *bindError) Unwrap() error { return e.err }

// listenAll binds every address to the same port. It closes what it opened
// when one bind fails.
func listenAll(addrs []string, port int) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(addrs))
	for _, a := range addrs {
		hostPort := net.JoinHostPort(a, strconv.Itoa(port))
		l, err := net.Listen("tcp", hostPort)
		if err != nil {
			closeListeners(listeners)
			return nil, &bindError{addr: hostPort, err: err}
		}
		listeners = append(listeners, l)
		if port == 0 {
			// The other addresses take the port of the first one.
			port = l.Addr().(*net.TCPAddr).Port
		}
	}
	return listeners, nil
}

func closeListeners(listeners []net.Listener) {
	for _, l := range listeners {
		_ = l.Close()
	}
}

// bindPortForward opens the local listeners (see the help for the port
// rules). It binds directly: a check before the bind can be wrong by the
// time of the bind.
func bindPortForward(addrs []string, spec portSpec) ([]net.Listener, error) {
	if spec.explicit && spec.local != 0 {
		ls, err := listenAll(addrs, spec.local)
		var be *bindError
		if errors.As(err, &be) && isAddrInUse(err) {
			return nil, fmt.Errorf("%s is in use; choose another LOCAL port, or leave it out to let stackctl choose one", be.addr)
		}
		return ls, err
	}
	if !spec.explicit {
		for p := spec.local; p <= spec.local+portForwardTries && p <= 65535; p++ {
			ls, err := listenAll(addrs, p)
			if err == nil {
				return ls, nil
			}
			if !isAddrInUse(err) {
				return nil, err
			}
		}
	}
	// Port 0: with several addresses, the port of the first one can be taken
	// on another address, so try a few times.
	var err error
	for i := 0; i < 5; i++ {
		var ls []net.Listener
		if ls, err = listenAll(addrs, 0); err == nil {
			return ls, nil
		}
		if !isAddrInUse(err) {
			return nil, err
		}
	}
	return nil, err
}

// syncWriter serializes writes from the connection goroutines.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// streamInsecure reports whether TLS verification is off (flag or context).
func streamInsecure() bool {
	if flagInsecure {
		return true
	}
	ctx := cfg.CurrentCtx()
	return ctx != nil && ctx.Insecure
}

// portForwardFatal reports whether err stops the command: every later
// connection would fail the same way.
func portForwardFatal(err error) bool {
	var hs *client.WSHandshakeError
	if errors.As(err, &hs) {
		return hs.Fatal()
	}
	var sc *client.StreamClosedError
	if errors.As(err, &sc) {
		return sc.Fatal()
	}
	// A refused session renewal: the server ended the session.
	var apiErr *client.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

// portForwardTunnelOptions are the tunnel timings (tests shorten them).
var portForwardTunnelOptions client.TunnelOptions

func runPortForward(ctx context.Context, cmd *cobra.Command, args []string) error {
	spec, err := parsePortSpec(args[2])
	if err != nil {
		return err
	}
	service := strings.TrimSpace(args[1])
	if service == "" {
		return errors.New("service name must not be empty")
	}
	rawAddr, _ := cmd.Flags().GetString("address")
	addrs, err := parseAddresses(rawAddr)
	if err != nil {
		return err
	}
	stderr := &syncWriter{w: cmd.ErrOrStderr()}

	if streamInsecure() {
		allow, _ := cmd.Flags().GetBool("allow-insecure-stream")
		if !allow {
			return errors.New("port-forward refuses --insecure: without TLS verification a man in the middle can read the tunnel data; add --allow-insecure-stream to accept that risk")
		}
		fmt.Fprintln(stderr, "Warning: --allow-insecure-stream: the tunnel data is not protected against a man in the middle")
	}
	for _, a := range addrs {
		if !isLoopback(a) {
			fmt.Fprintf(stderr, "Warning: %s is not a loopback address: anyone who can reach it uses your access to the stack\n", a)
		}
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	c.WarnWriter = stderr
	id, err := resolveStackID(c, args[0])
	if err != nil {
		return err
	}

	listeners, err := bindPortForward(addrs, spec)
	if err != nil {
		return err
	}
	for _, l := range listeners {
		hostPort := l.Addr().String()
		if printer.Quiet {
			fmt.Fprintln(printer.Writer, hostPort)
		} else {
			fmt.Fprintf(printer.Writer, "Forwarding from %s -> %s:%d\n", hostPort, service, spec.remote)
		}
	}

	pf := &portForwarder{
		client: c, id: id, service: service, port: spec.remote,
		stderr: stderr, fatal: make(chan error, 1),
	}
	return pf.serve(ctx, listeners)
}

// portForwarder runs the accept loops and the tunnels of one command.
type portForwarder struct {
	client  *client.Client
	id      string
	service string
	port    int
	stderr  io.Writer
	fatal   chan error
	conns   atomic.Int64
}

// serve accepts connections until ctx ends or an error stops the command.
func (pf *portForwarder) serve(ctx context.Context, listeners []net.Listener) error {
	tunnelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var loops, tunnels sync.WaitGroup
	for _, l := range listeners {
		loops.Add(1)
		go func(l net.Listener) {
			defer loops.Done()
			pf.acceptLoop(tunnelCtx, l, &tunnels)
		}(l)
	}

	var result error
	select {
	case <-ctx.Done():
	case result = <-pf.fatal:
	}
	cancel()
	closeListeners(listeners)
	loops.Wait()

	done := make(chan struct{})
	go func() {
		tunnels.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(portForwardShutdownWait):
		fmt.Fprintln(pf.stderr, "Warning: some connections did not close in time")
	}
	return result
}

func (pf *portForwarder) acceptLoop(ctx context.Context, l net.Listener, tunnels *sync.WaitGroup) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// For example too many open files: wait, then accept again.
			fmt.Fprintf(pf.stderr, "Warning: accepting a connection on %s: %v\n", l.Addr(), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		tunnels.Add(1)
		go func() {
			defer tunnels.Done()
			pf.handle(ctx, conn)
		}()
	}
}

// stop ends the command with err (the first error wins).
func (pf *portForwarder) stop(err error) {
	select {
	case pf.fatal <- err:
	default:
	}
}

// handle opens one tunnel for one local connection.
func (pf *portForwarder) handle(ctx context.Context, local net.Conn) {
	n := pf.conns.Add(1)
	ws, err := pf.client.DialPortForward(ctx, pf.id, pf.service, pf.port)
	if err != nil {
		_ = local.Close()
		if ctx.Err() != nil {
			return
		}
		if portForwardFatal(err) {
			pf.stop(err)
			return
		}
		fmt.Fprintf(pf.stderr, "Connection %d from %s failed: %v\n", n, local.RemoteAddr(), err)
		return
	}
	fmt.Fprintf(pf.stderr, "Connection %d from %s opened\n", n, local.RemoteAddr())
	stats, err := pf.client.Tunnel(ctx, ws, local, portForwardTunnelOptions)
	reason := stats.CloseReason
	if err != nil {
		reason = err.Error()
	}
	fmt.Fprintf(pf.stderr, "Connection %d closed: %d bytes in, %d bytes out, %s (%s)\n",
		n, stats.BytesIn, stats.BytesOut, stats.Duration.Round(time.Millisecond), reason)
	if err != nil && portForwardFatal(err) {
		pf.stop(err)
	}
}
