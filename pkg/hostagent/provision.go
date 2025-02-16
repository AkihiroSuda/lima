package hostagent

import (
	"errors"
	"fmt"

	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/sshocker/pkg/ssh"
	"github.com/sirupsen/logrus"
)

func (a *HostAgent) runProvisionScripts() error {
	var errs []error

	for i, f := range a.instConfig.Provision {
		switch f.Mode {
		case limayaml.ProvisionModeSystem, limayaml.ProvisionModeUser:
			logrus.Infof("Running %s provision %d of %d", f.Mode, i+1, len(a.instConfig.Provision))
			err := a.waitForProvision(
				provision{
					description: fmt.Sprintf("provision.%s/%08d", f.Mode, i),
					sudo:        f.Mode == limayaml.ProvisionModeSystem,
					script:      f.Script,
				})
			if err != nil {
				errs = append(errs, err)
			}
		case limayaml.ProvisionModeDependency, limayaml.ProvisionModeBoot:
			logrus.Infof("Skipping %s provision %d of %d", f.Mode, i+1, len(a.instConfig.Provision))
			continue
		default:
			return fmt.Errorf("unknown provision mode %q", f.Mode)
		}
	}
	return errors.Join(errs...)
}

func (a *HostAgent) waitForProvision(p provision) error {
	if p.sudo {
		return a.waitForSystemProvision(p)
	}
	return a.waitForUserProvision(p)
}

func (a *HostAgent) waitForSystemProvision(p provision) error {
	logrus.Debugf("executing script %q", p.description)
	stdout, stderr, err := sudoExecuteScript(a.instSSHAddress, a.sshLocalPort, a.sshConfig, p.script, p.description)
	logrus.Debugf("stdout=%q, stderr=%q, err=%v", stdout, stderr, err)
	if err != nil {
		return fmt.Errorf("stdout=%q, stderr=%q: %w", stdout, stderr, err)
	}
	return nil
}

func (a *HostAgent) waitForUserProvision(p provision) error {
	logrus.Debugf("executing script %q", p.description)
	stdout, stderr, err := ssh.ExecuteScript(a.instSSHAddress, a.sshLocalPort, a.sshConfig, p.script, p.description)
	logrus.Debugf("stdout=%q, stderr=%q, err=%v", stdout, stderr, err)
	if err != nil {
		return fmt.Errorf("stdout=%q, stderr=%q: %w", stdout, stderr, err)
	}
	return nil
}

type provision struct {
	description string
	script      string
	sudo        bool
}
