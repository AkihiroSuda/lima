package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/coreos/go-semver/semver"
	"github.com/lima-vm/lima/pkg/sshutil"
	"github.com/lima-vm/lima/pkg/store"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const copyHelp = `Copy files between host and guest

Prefix guest filenames with the instance name and a colon.

Example: limactl copy default:/etc/os-release .
`

func newCopyCommand() *cobra.Command {
	copyCommand := &cobra.Command{
		Use:     "copy SOURCE ... TARGET",
		Aliases: []string{"cp"},
		Short:   "Copy files between host and guest",
		Long:    copyHelp,
		Args:    WrapArgsError(cobra.MinimumNArgs(2)),
		RunE:    copyAction,
		GroupID: advancedCommand,
	}

	copyCommand.Flags().BoolP("recursive", "r", false, "copy directories recursively")
	copyCommand.Flags().BoolP("verbose", "v", false, "enable verbose output")

	return copyCommand
}

func copyAction(cmd *cobra.Command, args []string) error {
	recursive, err := cmd.Flags().GetBool("recursive")
	if err != nil {
		return err
	}

	verbose, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return err
	}

	arg0, err := exec.LookPath("scp")
	if err != nil {
		return err
	}
	instances := make(map[string]*store.Instance)
	scpFlags := []string{}
	scpArgs := []string{}
	debug, err := cmd.Flags().GetBool("debug")
	if err != nil {
		return err
	}

	if debug {
		verbose = true
	}

	if verbose {
		scpFlags = append(scpFlags, "-v")
	} else {
		scpFlags = append(scpFlags, "-q")
	}

	if recursive {
		scpFlags = append(scpFlags, "-r")
	}
	// this assumes that ssh and scp come from the same place, but scp has no -V
	legacySSH := sshutil.DetectOpenSSHVersion("ssh").LessThan(*semver.New("8.0.0"))
	localhostOnly := true
	for _, arg := range args {
		path := strings.Split(arg, ":")
		switch len(path) {
		case 1:
			scpArgs = append(scpArgs, arg)
		case 2:
			instName := path[0]
			inst, err := store.Inspect(instName)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("instance %q does not exist, run `limactl create %s` to create a new instance", instName, instName)
				}
				return err
			}
			if inst.Status == store.StatusStopped {
				return fmt.Errorf("instance %q is stopped, run `limactl start %s` to start the instance", instName, instName)
			}
			if legacySSH {
				scpFlags = append(scpFlags, "-P", fmt.Sprintf("%d", inst.SSHLocalPort))
				scpArgs = append(scpArgs, fmt.Sprintf("%s@%s:%s", *inst.Config.User.Name, inst.SSHAddress, path[1]))
			} else {
				scpArgs = append(scpArgs, fmt.Sprintf("scp://%s@%s:%d/%s", *inst.Config.User.Name, inst.SSHAddress, inst.SSHLocalPort, path[1]))
			}
			if inst.SSHAddress != "127.0.0.1" {
				localhostOnly = false
			}
			instances[instName] = inst
		default:
			return fmt.Errorf("path %q contains multiple colons", arg)
		}
	}
	if legacySSH && len(instances) > 1 {
		return errors.New("more than one (instance) host is involved in this command, this is only supported for openSSH v8.0 or higher")
	}
	scpFlags = append(scpFlags, "-3", "--")
	scpArgs = append(scpFlags, scpArgs...)

	var sshOpts []string
	if len(instances) == 1 {
		// Only one (instance) host is involved; we can use the instance-specific
		// arguments such as ControlPath.  This is preferred as we can multiplex
		// sessions without re-authenticating (MaxSessions permitting).
		for _, inst := range instances {
			sshOpts, err = sshutil.SSHOpts("ssh", inst.Dir, *inst.Config.User.Name, false, inst.SSHAddress, false, false, false)
			if err != nil {
				return err
			}
		}
	} else {
		// Copying among multiple hosts; we can't pass in host-specific options.
		sshOpts, err = sshutil.CommonOpts("ssh", false, localhostOnly)
		if err != nil {
			return err
		}
	}
	sshArgs := sshutil.SSHArgsFromOpts(sshOpts)

	sshCmd := exec.Command(arg0, append(sshArgs, scpArgs...)...)
	sshCmd.Stdin = cmd.InOrStdin()
	sshCmd.Stdout = cmd.OutOrStdout()
	sshCmd.Stderr = cmd.ErrOrStderr()
	logrus.Debugf("executing scp (may take a long time): %+v", sshCmd.Args)

	// TODO: use syscall.Exec directly (results in losing tty?)
	return sshCmd.Run()
}
