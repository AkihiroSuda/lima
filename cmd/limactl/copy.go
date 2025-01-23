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

type copyTool string

const (
	rsync copyTool = "rsync"
	scp   copyTool = "scp"
)

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

	debug, err := cmd.Flags().GetBool("debug")
	if err != nil {
		return err
	}

	if debug {
		verbose = true
	}

	cpTool := rsync
	arg0, err := exec.LookPath(string(cpTool))
	if err != nil {
		arg0, err = exec.LookPath(string(cpTool))
		if err != nil {
			return err
		}
	}
	logrus.Infof("using copy tool %q", arg0)

	var copyCmd *exec.Cmd
	switch cpTool {
	case scp:
		copyCmd, err = scpCommand(arg0, args, verbose, recursive)
	case rsync:
		copyCmd, err = rsyncCommand(arg0, args, verbose, recursive)
	default:
		err = fmt.Errorf("invalid copy tool %q", cpTool)
	}
	if err != nil {
		return err
	}

	copyCmd.Stdin = cmd.InOrStdin()
	copyCmd.Stdout = cmd.OutOrStdout()
	copyCmd.Stderr = cmd.ErrOrStderr()
	logrus.Debugf("executing %v (may take a long time)", copyCmd)

	// TODO: use syscall.Exec directly (results in losing tty?)
	return copyCmd.Run()
}

func scpCommand(command string, args []string, verbose, recursive bool) (*exec.Cmd, error) {
	instances := make(map[string]*store.Instance)

	scpFlags := []string{}
	scpArgs := []string{}
	var err error

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
					return nil, fmt.Errorf("instance %q does not exist, run `limactl create %s` to create a new instance", instName, instName)
				}
				return nil, err
			}
			if inst.Status == store.StatusStopped {
				return nil, fmt.Errorf("instance %q is stopped, run `limactl start %s` to start the instance", instName, instName)
			}
			if legacySSH {
				scpFlags = append(scpFlags, "-P", fmt.Sprintf("%d", inst.SSHLocalPort))
				scpArgs = append(scpArgs, fmt.Sprintf("%s@127.0.0.1:%s", *inst.Config.User.Name, path[1]))
			} else {
				scpArgs = append(scpArgs, fmt.Sprintf("scp://%s@127.0.0.1:%d/%s", *inst.Config.User.Name, inst.SSHLocalPort, path[1]))
			}
			instances[instName] = inst
		default:
			return nil, fmt.Errorf("path %q contains multiple colons", arg)
		}
	}
	if legacySSH && len(instances) > 1 {
		return nil, errors.New("more than one (instance) host is involved in this command, this is only supported for openSSH v8.0 or higher")
	}
	scpFlags = append(scpFlags, "-3", "--")
	scpArgs = append(scpFlags, scpArgs...)

	var sshOpts []string
	if len(instances) == 1 {
		// Only one (instance) host is involved; we can use the instance-specific
		// arguments such as ControlPath.  This is preferred as we can multiplex
		// sessions without re-authenticating (MaxSessions permitting).
		for _, inst := range instances {
			sshOpts, err = sshutil.SSHOpts("ssh", inst.Dir, *inst.Config.User.Name, false, false, false, false)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// Copying among multiple hosts; we can't pass in host-specific options.
		sshOpts, err = sshutil.CommonOpts("ssh", false)
		if err != nil {
			return nil, err
		}
	}
	sshArgs := sshutil.SSHArgsFromOpts(sshOpts)

	return exec.Command(command, append(sshArgs, scpArgs...)...), nil
}

func rsyncCommand(command string, args []string, verbose, recursive bool) (*exec.Cmd, error) {
	instances := make(map[string]*store.Instance)

	var instName string

	rsyncFlags := []string{}
	rsyncArgs := []string{}

	if verbose {
		rsyncFlags = append(rsyncFlags, "-v", "--progress")
	} else {
		rsyncFlags = append(rsyncFlags, "-q")
	}

	if recursive {
		rsyncFlags = append(rsyncFlags, "-r")
	}

	for _, arg := range args {
		path := strings.Split(arg, ":")
		switch len(path) {
		case 1:
			inst, ok := instances[instName]
			if !ok {
				return nil, fmt.Errorf("instance %q does not exist, run `limactl create %s` to create a new instance", instName, instName)
			}
			guestVM := fmt.Sprintf("%s@127.0.0.1:%s", *inst.Config.User.Name, path[0])
			rsyncArgs = append(rsyncArgs, guestVM)
		case 2:
			instName = path[0]
			inst, err := store.Inspect(instName)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf("instance %q does not exist, run `limactl create %s` to create a new instance", instName, instName)
				}
				return nil, err
			}
			sshOpts, err := sshutil.SSHOpts("ssh", inst.Dir, *inst.Config.User.Name, false, false, false, false)
			if err != nil {
				return nil, err
			}

			sshArgs := sshutil.SSHArgsFromOpts(sshOpts)
			sshStr := fmt.Sprintf("ssh -p %s %s", fmt.Sprintf("%d", inst.SSHLocalPort), strings.Join(sshArgs, " "))

			destDir := args[1]
			mkdirCmd := exec.Command(
				"ssh",
				"-p", fmt.Sprintf("%d", inst.SSHLocalPort),
			)
			mkdirCmd.Args = append(mkdirCmd.Args, sshArgs...)
			mkdirCmd.Args = append(mkdirCmd.Args,
				fmt.Sprintf("%s@%s", *inst.Config.User.Name, "127.0.0.1"),
				fmt.Sprintf("sudo mkdir -p %q && sudo chown %s:%s %s", destDir, *inst.Config.User.Name, *inst.Config.User.Name, destDir),
			)
			mkdirCmd.Stdout = os.Stdout
			mkdirCmd.Stderr = os.Stderr
			if err := mkdirCmd.Run(); err != nil {
				return nil, fmt.Errorf("failed to create directory %q on remote: %w", destDir, err)
			}

			rsyncArgs = append(rsyncArgs, "-avz", "-e", sshStr, path[1])
			instances[instName] = inst
		default:
			return nil, fmt.Errorf("path %q contains multiple colons", arg)
		}
	}

	rsyncArgs = append(rsyncFlags, rsyncArgs...)

	return exec.Command(command, rsyncArgs...), nil
}
