package hostagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/localpathutil"
	"github.com/lima-vm/sshocker/pkg/reversesshfs"
	"github.com/sirupsen/logrus"
)

type mount struct {
	close func() error
}

func (a *HostAgent) setupMounts() ([]*mount, error) {
	var (
		res  []*mount
		errs []error
	)
	for _, f := range a.instConfig.Mounts {
		m, err := a.setupMount(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		res = append(res, m)
	}
	return res, errors.Join(errs...)
}

func (a *HostAgent) setupMount(m limayaml.Mount) (*mount, error) {
	location, err := localpathutil.Expand(m.Location)
	if err != nil {
		return nil, err
	}

	mountPoint, err := localpathutil.Expand(*m.MountPoint)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(location, 0o755); err != nil {
		return nil, err
	}
	// NOTE: allow_other requires "user_allow_other" in /etc/fuse.conf
	sshfsOptions := "allow_other"
	if !*m.SSHFS.Cache {
		sshfsOptions += ",cache=no"
	}
	if *m.SSHFS.FollowSymlinks {
		sshfsOptions += ",follow_symlinks"
	}
	logrus.Infof("Mounting %q on %q", location, mountPoint)

	rsf := &reversesshfs.ReverseSSHFS{
		Driver:              *m.SSHFS.SFTPDriver,
		SSHConfig:           a.sshConfig,
		LocalPath:           location,
		Host:                "127.0.0.1",
		Port:                a.sshLocalPort,
		RemotePath:          mountPoint,
		Readonly:            !(*m.Writable),
		SSHFSAdditionalArgs: []string{"-o", sshfsOptions},
	}
	serverPath, err := exec.LookPath("sftp-server")
	if err == nil {
		rsf.OpensshSftpServerBinary = serverPath
	}
	if err := rsf.Prepare(); err != nil {
		return nil, fmt.Errorf("failed to prepare reverse sshfs for %q on %q: %w", location, mountPoint, err)
	}
	if err := rsf.Start(); err != nil {
		logrus.WithError(err).Warnf("failed to mount reverse sshfs for %q on %q, retrying with `-o nonempty`", location, mountPoint)
		// NOTE: nonempty is not supported for libfuse3: https://github.com/canonical/multipass/issues/1381
		rsf.SSHFSAdditionalArgs = []string{"-o", "nonempty"}
		if err := rsf.Start(); err != nil {
			return nil, fmt.Errorf("failed to mount reverse sshfs for %q on %q: %w", location, mountPoint, err)
		}
	}

	res := &mount{
		close: func() error {
			logrus.Infof("Unmounting %q", location)
			if closeErr := rsf.Close(); closeErr != nil {
				return fmt.Errorf("failed to unmount reverse sshfs for %q on %q: %w", location, mountPoint, err)
			}
			return nil
		},
	}
	return res, nil
}
