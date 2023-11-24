package hostagent

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lima-vm/lima/pkg/bicopy"
	"github.com/lima-vm/lima/pkg/portfwd"
	"github.com/lima-vm/sshocker/pkg/ssh"
	"github.com/sirupsen/logrus"
)

// forwardTCP is not thread-safe.
func forwardTCP(ctx context.Context, sshConfig *ssh.SSHConfig, addr string, port int, local, remote, verb string) error {
	if strings.HasPrefix(local, "/") {
		return forwardSSH(ctx, sshConfig, addr, port, local, remote, verb, false)
	}
	localIPStr, localPortStr, err := net.SplitHostPort(local)
	if err != nil {
		return err
	}
	localIP := net.ParseIP(localIPStr)
	localPort, err := strconv.Atoi(localPortStr)
	if err != nil {
		return err
	}

	if !localIP.Equal(IPv4loopback1) || localPort >= 1024 {
		return forwardSSH(ctx, sshConfig, addr, port, local, remote, verb, false)
	}

	// on macOS, listening on 127.0.0.1:80 requires root while 0.0.0.0:80 does not require root.
	// https://twitter.com/_AkihiroSuda_/status/1403403845842075648
	//
	// We use "pseudoloopback" forwarder that listens on 0.0.0.0:80 but rejects connections from non-loopback src IP.
	logrus.Debugf("using pseudoloopback port forwarder for %q", local)

	if verb == verbCancel {
		plf, ok := pseudoLoopbackForwarders[local]
		if ok {
			localUnix := plf.unixAddr.Name
			_ = plf.Close()
			delete(pseudoLoopbackForwarders, local)
			if err := forwardSSH(ctx, sshConfig, addr, port, localUnix, remote, verb, false); err != nil {
				return err
			}
		} else {
			logrus.Warnf("forwarding for %q seems already cancelled?", local)
		}
		return nil
	}

	localUnixDir, err := os.MkdirTemp("/tmp", fmt.Sprintf("lima-psl-%s-%d-", localIP, localPort))
	if err != nil {
		return err
	}
	localUnix := filepath.Join(localUnixDir, "sock")
	logrus.Debugf("forwarding %q to %q", localUnix, remote)
	if err := forwardSSH(ctx, sshConfig, addr, port, localUnix, remote, verb, false); err != nil {
		return err
	}
	plf, err := newPseudoLoopbackForwarder(localPort, localUnix)
	if err != nil {
		if cancelErr := forwardSSH(ctx, sshConfig, addr, port, localUnix, remote, verbCancel, false); cancelErr != nil {
			logrus.WithError(cancelErr).Warnf("failed to cancel forwarding %q to %q", localUnix, remote)
		}
		return err
	}
	plf.onClose = func() error {
		return os.RemoveAll(localUnixDir)
	}
	pseudoLoopbackForwarders[local] = plf
	go func() {
		if plfErr := plf.Serve(); plfErr != nil {
			logrus.WithError(plfErr).Warning("pseudoloopback forwarder crashed")
		}
	}()
	return nil
}

var pseudoLoopbackForwarders = make(map[string]*pseudoLoopbackForwarder)

type pseudoLoopbackForwarder struct {
	ln       *net.TCPListener
	unixAddr *net.UnixAddr
	onClose  func() error
}

func newPseudoLoopbackForwarder(localPort int, unixSock string) (*pseudoLoopbackForwarder, error) {
	unixAddr, err := net.ResolveUnixAddr("unix", unixSock)
	if err != nil {
		return nil, err
	}

	// use "tcp" network to listen on both "tcp4" and "tcp6"
	lnAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("0.0.0.0:%d", localPort))
	if err != nil {
		return nil, err
	}
	ln, err := net.ListenTCP("tcp", lnAddr)
	if err != nil {
		return nil, err
	}

	plf := &pseudoLoopbackForwarder{
		ln:       ln,
		unixAddr: unixAddr,
	}

	return plf, nil
}

func (plf *pseudoLoopbackForwarder) Serve() error {
	defer plf.ln.Close()
	for {
		ac, err := plf.ln.AcceptTCP()
		if err != nil {
			return err
		}
		remoteAddr := ac.RemoteAddr().String() // ip:port
		remoteAddrIP, _, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			logrus.WithError(err).Debugf("pseudoloopback forwarder: rejecting non-loopback remoteAddr %q (unparsable)", remoteAddr)
			ac.Close()
			continue
		}
		if !portfwd.IsLoopback(remoteAddrIP) {
			logrus.WithError(err).Debugf("pseudoloopback forwarder: rejecting non-loopback remoteAddr %q", remoteAddr)
			ac.Close()
			continue
		}
		go func(ac *net.TCPConn) {
			if fErr := plf.forward(ac); fErr != nil {
				logrus.Error(fErr)
			}
		}(ac)
	}
}

func (plf *pseudoLoopbackForwarder) forward(ac *net.TCPConn) error {
	defer ac.Close()
	unixConn, err := net.DialUnix("unix", nil, plf.unixAddr)
	if err != nil {
		return err
	}
	defer unixConn.Close()
	bicopy.Bicopy(ac, unixConn, nil)
	return nil
}

func (plf *pseudoLoopbackForwarder) Close() error {
	_ = plf.ln.Close()
	return plf.onClose()
}
