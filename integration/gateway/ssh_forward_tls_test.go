//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestFramedTLSAuthenticatedSendAndBoundedResumeThroughSSHLocalForward(t *testing.T) {
	if os.Getenv("AIRC_TEST_SSH_FORWARD") != "1" {
		t.Skip("set AIRC_TEST_SSH_FORWARD=1 to run the isolated sshd acceptance fixture")
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal("AIRC_TEST_SSH_FORWARD=1 but OpenSSH client is unavailable")
	}
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal("AIRC_TEST_SSH_FORWARD=1 but OpenSSH server is unavailable")
	}

	conn, credentials, socket, cert, gatewayStop := framedTLSFixtureForSSH(t)
	defer gatewayStop()
	gatewayAddress := conn.RemoteAddr().String()
	if err := conn.Close(); err != nil {
		t.Fatal("close direct fixture connection", err)
	}

	forward := sshLocalForward(t, ssh, sshd, gatewayAddress)
	defer forward.stop(t)
	tlsConn := connectForwardedTLS(t, forward, cert)
	defer tlsConn.Close()
	state := tlsConn.ConnectionState()
	if state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != adapter.FramedALPN {
		t.Fatal("forwarded connection did not verify the framed TLS endpoint", state)
	}

	writeFrame(t, tlsConn, adapter.FrameRequest{Version: 1, CorrelationID: "ssh-forward-bad-auth", Profile: "signed", Envelope: "invalid"})
	if response := readFrame(t, tlsConn); response.CorrelationID != "ssh-forward-bad-auth" || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
		t.Fatal("forwarded TLS connector accepted invalid authentication", response)
	}
	// The shared framed witness checks durable authenticated sends, stable
	// request replay, and one-record cursor pages observed by native IRC.
	framedAuthenticatedSendAndBoundedResume(t, tlsConn, credentials, socket, false)
}

func framedTLSFixtureForSSH(t *testing.T) (net.Conn, *detachedCredentials, string, testtls.Certificate, func()) {
	t.Helper()
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 2, 1
	cert := testtls.New(t)
	listener := config.Listener{ID: "stream", Protocol: "framed", Address: "127.0.0.1:0", Enabled: true, Operations: dispatch.ImplementedOperations(), TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	framedExample(t, address, cert.CAPath, credentials)
	clientTLS := cert.Client.Clone()
	clientTLS.NextProtos = []string{adapter.FramedALPN}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, clientTLS)
	if err != nil {
		t.Fatal("connect framed TLS fixture", err)
	}
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("framed gateway did not stop after SSH forwarding acceptance")
		}
	}
	return conn, credentials, socket, cert, stop
}

type sshForwardProcess struct {
	command      *exec.Cmd
	done         chan struct{}
	err          error
	localAddress string
	logPath      string
	stopped      bool
}

func (p *sshForwardProcess) stop(t *testing.T) {
	t.Helper()
	if p.command == nil || p.command.Process == nil {
		return
	}
	select {
	case <-p.done:
		if !p.stopped {
			t.Errorf("SSH local forward exited before cleanup: %v", p.err)
		}
		return
	default:
	}
	p.stopped = true
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Errorf("interrupt SSH local forward: %v", err)
	}
	select {
	case <-p.done:
		if !wasInterrupted(p.err) {
			t.Errorf("SSH local forward exited unexpectedly during shutdown: %v", p.err)
		}
	case <-time.After(5 * time.Second):
		if err := p.command.Process.Kill(); err != nil {
			t.Errorf("kill stuck SSH local forward: %v", err)
		}
		<-p.done
		t.Errorf("SSH local forward required forced termination")
	}
}

func wasInterrupted(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	if exitErr.ExitCode() == 255 {
		return true // OpenSSH uses 255 when -N is interrupted.
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
}

func runSSHProbe(binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, binary, args...).CombinedOutput()
}

func sshLocalForward(t *testing.T, ssh, sshd, gatewayAddress string) *sshForwardProcess {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatal("identify local SSH account", err)
	}
	gatewayHost, gatewayPortText, err := net.SplitHostPort(gatewayAddress)
	if err != nil {
		t.Fatal(err)
	}
	gatewayPort, err := strconv.Atoi(gatewayPortText)
	if err != nil || gatewayHost != "127.0.0.1" {
		t.Fatalf("fixture gateway is not loopback IPv4: %q", gatewayAddress)
	}

	root := t.TempDir()
	hostKey, clientKey, deniedKey := filepath.Join(root, "host_key"), filepath.Join(root, "client_key"), filepath.Join(root, "denied_key")
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatal("AIRC_TEST_SSH_FORWARD=1 but ssh-keygen is unavailable")
	}
	for _, key := range []string{hostKey, clientKey, deniedKey} {
		output, err := runSSHProbe(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key)
		if err != nil {
			t.Fatalf("generate ephemeral SSH key: %v: %s", err, output)
		}
	}
	authorized := filepath.Join(root, "authorized_keys")
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorized, publicKey, 0600); err != nil {
		t.Fatal(err)
	}
	sshdPort := reserveLoopbackPort(t)
	localPort := reserveLoopbackPort(t)
	configPath := filepath.Join(root, "sshd_config")
	configText := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nPermitRootLogin no\nAllowUsers %s\nAllowTcpForwarding local\nGatewayPorts no\nPermitOpen 127.0.0.1:%d\nUsePAM no\nPrintMotd no\nLogLevel VERBOSE\n", sshdPort, hostKey, filepath.Join(root, "sshd.pid"), authorized, current.Username, gatewayPort)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := runSSHProbe(sshd, "-t", "-f", configPath); err != nil {
		t.Fatalf("validate isolated sshd config: %v: %s", err, output)
	}
	logFile, err := os.Create(filepath.Join(root, "sshd.log"))
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	server.Stdout, server.Stderr = logFile, logFile
	if err := server.Start(); err != nil {
		t.Fatal("start unprivileged isolated sshd", err)
	}
	serverDone := make(chan struct{})
	var serverErr error
	go func() { serverErr = server.Wait(); close(serverDone) }()
	closeServer := func() {
		select {
		case <-serverDone:
			t.Errorf("isolated sshd exited before cleanup: %v", serverErr)
			_ = logFile.Close()
			return
		default:
		}
		if err := server.Process.Signal(os.Interrupt); err != nil {
			t.Errorf("interrupt isolated sshd: %v", err)
		}
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			if err := server.Process.Kill(); err != nil {
				t.Errorf("kill stuck isolated sshd: %v", err)
			}
			<-serverDone
			t.Error("isolated sshd required forced termination")
		}
		if serverErr != nil && !wasInterrupted(serverErr) {
			t.Errorf("isolated sshd exited unexpectedly during shutdown: %v", serverErr)
		}
		_ = logFile.Close()
	}
	t.Cleanup(closeServer)
	waitTCP(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(sshdPort)), serverDone)

	hostPublic, err := runSSHProbe(keygen, "-y", "-f", hostKey)
	if err != nil {
		t.Fatalf("read ephemeral SSH host public key: %v: %s", err, hostPublic)
	}
	hostFields := strings.Fields(string(hostPublic))
	if len(hostFields) < 2 {
		t.Fatalf("invalid ephemeral SSH host public key: %q", hostPublic)
	}
	knownHosts := filepath.Join(root, "known_hosts")
	knownHostEntry := fmt.Sprintf("[127.0.0.1]:%d %s %s\n", sshdPort, hostFields[0], hostFields[1])
	if err := os.WriteFile(knownHosts, []byte(knownHostEntry), 0600); err != nil {
		t.Fatal(err)
	}
	baseArgs := []string{"-F", "/dev/null", "-p", strconv.Itoa(sshdPort), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "StrictHostKeyChecking=yes", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "UserKnownHostsFile=" + knownHosts, "-o", "ConnectTimeout=2"}
	deniedArgs := append(append([]string(nil), baseArgs...), "-i", deniedKey, current.Username+"@127.0.0.1", "true")
	if output, err := runSSHProbe(ssh, deniedArgs...); err == nil {
		t.Fatalf("isolated sshd accepted an unlisted client key: %s", output)
	}
	forwardArgs := append(append([]string(nil), baseArgs...), "-N", "-o", "ExitOnForwardFailure=yes", "-i", clientKey, "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, gatewayPort), current.Username+"@127.0.0.1")
	command := exec.Command(ssh, forwardArgs...)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatal("start authenticated SSH local forward", err)
	}
	logPath := filepath.Join(root, "sshd.log")
	forward := &sshForwardProcess{command: command, done: make(chan struct{}), localAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)), logPath: logPath}
	go func() { forward.err = command.Wait(); close(forward.done) }()
	t.Cleanup(func() { forward.stop(t) })
	waitTCP(t, forward.localAddress, forward.done)
	return forward
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitTCP(t *testing.T, address string, stopped <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-stopped:
			t.Fatal("SSH process exited before becoming ready")
		default:
		}
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("SSH listener failed to become ready at %s", address)
}

func connectForwardedTLS(t *testing.T, forward *sshForwardProcess, cert testtls.Certificate) *tls.Conn {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", forward.localAddress, 150*time.Millisecond)
		if err == nil {
			client := cert.Client.Clone()
			client.NextProtos = []string{adapter.FramedALPN}
			client.ServerName = "127.0.0.1"
			tlsConn := tls.Client(conn, client)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			err = tlsConn.HandshakeContext(ctx)
			cancel()
			if err == nil {
				return tlsConn
			}
			lastErr = err
			_ = conn.Close()
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(forward.logPath)
	t.Fatalf("authenticated SSH forward did not reach the framed TLS listener; TLS err=%v log=%s", lastErr, log)
	return nil
}
