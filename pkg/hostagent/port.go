package hostagent

import (
	"context"
	"net"

	"github.com/lima-vm/lima/pkg/guestagent/api"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/sshocker/pkg/ssh"
	"github.com/sirupsen/logrus"
)

type portForwarder struct {
	sshConfig   *ssh.SSHConfig
	sshHostAddr string
	sshHostPort int
	rules       []limayaml.PortForward
	ignore      bool
	vmType      limayaml.VMType
}

const sshGuestPort = 22

var IPv4loopback1 = limayaml.IPv4loopback1

func newPortForwarder(sshConfig *ssh.SSHConfig, sshHostAddr string, sshHostPort int, rules []limayaml.PortForward, ignore bool, vmType limayaml.VMType) *portForwarder {
	return &portForwarder{
		sshConfig:   sshConfig,
		sshHostAddr: sshHostAddr,
		sshHostPort: sshHostPort,
		rules:       rules,
		ignore:      ignore,
		vmType:      vmType,
	}
}

func hostAddress(rule limayaml.PortForward, guest *api.IPPort) string {
	if rule.HostSocket != "" {
		return rule.HostSocket
	}
	host := &api.IPPort{Ip: rule.HostIP.String()}
	if guest.Port == 0 {
		// guest is a socket
		host.Port = int32(rule.HostPort)
	} else {
		host.Port = guest.Port + int32(rule.HostPortRange[0]-rule.GuestPortRange[0])
	}
	return host.HostString()
}

func (pf *portForwarder) forwardingAddresses(guest *api.IPPort) (hostAddr, guestAddr string) {
	guestIP := net.ParseIP(guest.Ip)
	for _, rule := range pf.rules {
		if rule.GuestSocket != "" {
			continue
		}
		switch rule.Proto {
		case limayaml.ProtoTCP, limayaml.ProtoAny:
		default:
			continue
		}
		if guest.Port < int32(rule.GuestPortRange[0]) || guest.Port > int32(rule.GuestPortRange[1]) {
			continue
		}
		switch {
		case guestIP.IsUnspecified():
		case guestIP.Equal(rule.GuestIP):
		case guestIP.Equal(net.IPv6loopback) && rule.GuestIP.Equal(IPv4loopback1):
		case rule.GuestIP.IsUnspecified() && !rule.GuestIPMustBeZero:
			// When GuestIPMustBeZero is true, then 0.0.0.0 must be an exact match, which is already
			// handled above by the guest.IP.IsUnspecified() condition.
		default:
			continue
		}
		if rule.Ignore {
			if guestIP.IsUnspecified() && !rule.GuestIP.IsUnspecified() {
				continue
			}
			break
		}
		return hostAddress(rule, guest), guest.HostString()
	}
	return "", guest.HostString()
}

func (pf *portForwarder) OnEvent(ctx context.Context, ev *api.Event) {
	for _, f := range ev.LocalPortsRemoved {
		if f.Protocol != "tcp" {
			continue
		}
		local, remote := pf.forwardingAddresses(f)
		if local == "" {
			continue
		}
		logrus.Infof("Stopping forwarding TCP from %s to %s", remote, local)
		if err := forwardTCP(ctx, pf.sshConfig, pf.sshHostAddr, pf.sshHostPort, local, remote, verbCancel); err != nil {
			logrus.WithError(err).Warnf("failed to stop forwarding tcp port %d", f.Port)
		}
	}
	for _, f := range ev.LocalPortsAdded {
		if f.Protocol != "tcp" {
			continue
		}
		local, remote := pf.forwardingAddresses(f)
		if local == "" {
			if !pf.ignore {
				logrus.Infof("Not forwarding TCP %s", remote)
			}
			continue
		}
		logrus.Infof("Forwarding TCP from %s to %s", remote, local)
		if err := forwardTCP(ctx, pf.sshConfig, pf.sshHostAddr, pf.sshHostPort, local, remote, verbForward); err != nil {
			logrus.WithError(err).Warnf("failed to set up forwarding tcp port %d (negligible if already forwarded)", f.Port)
		}
	}
}
