package hostagent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/sshocker/pkg/ssh"
	"github.com/sirupsen/logrus"
)

func (a *HostAgent) waitForRequirements(label string, requirements []requirement) error {
	const (
		retries       = 60
		sleepDuration = 10 * time.Second
	)
	var errs []error

	for i, req := range requirements {
	retryLoop:
		for j := 0; j < retries; j++ {
			logrus.Infof("Waiting for the %s requirement %d of %d: %q", label, i+1, len(requirements), req.description)
			err := a.waitForRequirement(req)
			if err == nil {
				logrus.Infof("The %s requirement %d of %d is satisfied", label, i+1, len(requirements))
				break retryLoop
			}
			if req.fatal {
				logrus.Infof("No further %s requirements will be checked", label)
				errs = append(errs, fmt.Errorf("failed to satisfy the %s requirement %d of %d %q: %s; skipping further checks: %w", label, i+1, len(requirements), req.description, req.debugHint, err))
				return errors.Join(errs...)
			}
			if j == retries-1 {
				errs = append(errs, fmt.Errorf("failed to satisfy the %s requirement %d of %d %q: %s: %w", label, i+1, len(requirements), req.description, req.debugHint, err))
				break retryLoop
			}
			time.Sleep(10 * time.Second)
		}
	}
	return errors.Join(errs...)
}

// prefixExportParam will modify a script to be executed by ssh.ExecuteScript so that it exports
// all the variables from /mnt/lima-cidata/param.env before invoking the actual interpreter.
//
//   - The script is executed in user mode, so needs to read the file using `sudo`.
//
//   - `sudo cat param.env | while …; do export …; done` does not work because the piping
//     creates a subshell, and the exported variables are not visible to the parent process.
//
//   - The `<<<"$string"` redirection is not available on alpine-lima, where /bin/bash is
//     just a wrapper around busybox ash.
//
// A script that will start with `#!/usr/bin/env ruby` will be modified to look like this:
//
//	while read -r line; do
//	    [ -n "$line" ] && export "$line"
//	done<<EOF
//	$(sudo cat /mnt/lima-cidata/param.env)
//	EOF
//	/usr/bin/env ruby
//
// ssh.ExecuteScript will strip the `#!` prefix from the first line and invoke the
// rest of the line as the command. The full script is then passed via STDIN. We use
// "$(printf '…')" to be able to use \n as newline escapes, to fit everything on a
// single line:
//
//	#!/bin/bash -c "$(printf 'while … done<<EOF\n$(sudo …)\nEOF\n/usr/bin/env ruby')"
//	#!/usr/bin/env ruby
//	…
//
// An earlier implementation used $'…' for quoting, but that isn't supported if the
// user switched the default shell to fish.
func prefixExportParam(script string) (string, error) {
	interpreter, err := ssh.ParseScriptInterpreter(script)
	if err != nil {
		return "", err
	}

	// TODO we should have a symbolic constant for `/mnt/lima-cidata`
	exportParam := `while read -r line; do [ -n "$line" ] && export "$line"; done<<EOF\n$(sudo cat /mnt/lima-cidata/param.env)\nEOF\n`

	// double up all '%' characters so we can pass them through unchanged in the format string of printf
	interpreter = strings.ReplaceAll(interpreter, "%", "%%")
	exportParam = strings.ReplaceAll(exportParam, "%", "%%")
	// strings will be interpolated into single-quoted strings, so protect any existing single quotes
	interpreter = strings.ReplaceAll(interpreter, "'", `'"'"'`)
	exportParam = strings.ReplaceAll(exportParam, "'", `'"'"'`)
	return fmt.Sprintf("#!/bin/bash -c \"$(printf '%s%s')\"\n%s", exportParam, interpreter, script), nil
}

func (a *HostAgent) waitForRequirement(r requirement) error {
	logrus.Debugf("executing script %q", r.description)
	script, err := prefixExportParam(r.script)
	if err != nil {
		return err
	}
	stdout, stderr, err := ssh.ExecuteScript(a.instSSHAddress, a.sshLocalPort, a.sshConfig, script, r.description)
	logrus.Debugf("stdout=%q, stderr=%q, err=%v", stdout, stderr, err)
	if err != nil {
		return fmt.Errorf("stdout=%q, stderr=%q: %w", stdout, stderr, err)
	}
	return nil
}

type requirement struct {
	description string
	script      string
	debugHint   string
	fatal       bool
}

func (a *HostAgent) essentialRequirements() []requirement {
	req := make([]requirement, 0)
	req = append(req,
		requirement{
			description: "ssh",
			script: `#!/bin/bash
true
`,
			debugHint: `Failed to SSH into the guest.
Make sure that the YAML field "ssh.localPort" is not used by other processes on the host.
If any private key under ~/.ssh is protected with a passphrase, you need to have ssh-agent to be running.
`,
		})
	if *a.instConfig.Plain {
		return req
	}
	if *a.y.VMType != limayaml.EXT {
		req = append(req,
			requirement{
				description: "user session is ready for ssh",
				script: `#!/bin/bash
set -eux -o pipefail
if ! timeout 30s bash -c "until sudo diff -q /run/lima-ssh-ready /mnt/lima-cidata/meta-data 2>/dev/null; do sleep 3; done"; then
	echo >&2 "not ready to start persistent ssh session"
	exit 1
fi
`,
				debugHint: `The boot sequence will terminate any existing user session after updating
/etc/environment to make sure the session includes the new values.
Terminating the session will break the persistent SSH tunnel, so
it must not be created until the session reset is done.
`,
			})
	}

	if *a.instConfig.MountType == limayaml.REVSSHFS && len(a.instConfig.Mounts) > 0 {
		req = append(req, requirement{
			description: "sshfs binary to be installed",
			script: `#!/bin/bash
set -eux -o pipefail
if ! timeout 30s bash -c "until command -v sshfs; do sleep 3; done"; then
	echo >&2 "sshfs is not installed yet"
	exit 1
fi
`,
			debugHint: `The sshfs binary was not installed in the guest.
Make sure that you are using an officially supported image.
Also see "/var/log/cloud-init-output.log" in the guest.
A possible workaround is to run "apt-get install sshfs" in the guest.
`,
		})
		req = append(req, requirement{
			description: "fuse to \"allow_other\" as user",
			script: `#!/bin/bash
set -eux -o pipefail
if ! timeout 30s bash -c "until sudo grep -q ^user_allow_other /etc/fuse*.conf; do sleep 3; done"; then
	echo >&2 "/etc/fuse.conf (/etc/fuse3.conf) is not updated to contain \"user_allow_other\""
	exit 1
fi
`,
			debugHint: `Append "user_allow_other" to /etc/fuse.conf (/etc/fuse3.conf) in the guest`,
		})
	}
	return req
}

func (a *HostAgent) optionalRequirements() []requirement {
	req := make([]requirement, 0)
	if (*a.instConfig.Containerd.System || *a.instConfig.Containerd.User) && !*a.instConfig.Plain {
		req = append(req,
			requirement{
				description: "systemd must be available",
				fatal:       true,
				script: `#!/bin/bash
set -eux -o pipefail
if ! command -v systemctl 2>&1 >/dev/null; then
    echo >&2 "systemd is not available on this OS"
    exit 1
fi
`,
				debugHint: `systemd is required to run containerd, but does not seem to be available.
Make sure that you use an image that supports systemd. If you do not want to run
containerd, please make sure that both 'container.system' and 'containerd.user'
are set to 'false' in the config file.
`,
			},
			requirement{
				description: "containerd binaries to be installed",
				script: `#!/bin/bash
set -eux -o pipefail
if ! timeout 30s bash -c "until command -v nerdctl || test -x ` + *a.instConfig.GuestInstallPrefix + `/bin/nerdctl; do sleep 3; done"; then
	echo >&2 "nerdctl is not installed yet"
	exit 1
fi
`,
				debugHint: `The nerdctl binary was not installed in the guest.
Make sure that you are using an officially supported image.
Also see "/var/log/cloud-init-output.log" in the guest.
`,
			})
	}
	for _, probe := range a.instConfig.Probes {
		if probe.Mode == limayaml.ProbeModeReadiness {
			req = append(req, requirement{
				description: probe.Description,
				script:      probe.Script,
				debugHint:   probe.Hint,
			})
		}
	}
	return req
}

func (a *HostAgent) finalRequirements() []requirement {
	req := make([]requirement, 0)
	if *a.y.VMType != limayaml.EXT {
		req = append(req,
			requirement{
				description: "boot scripts must have finished",
				script: `#!/bin/bash
set -eux -o pipefail
if ! timeout 30s bash -c "until sudo diff -q /run/lima-boot-done /mnt/lima-cidata/meta-data 2>/dev/null; do sleep 3; done"; then
	echo >&2 "boot scripts have not finished"
	exit 1
fi
`,
				debugHint: `All boot scripts, provisioning scripts, and readiness probes must
finish before the instance is considered "ready".
Check "/var/log/cloud-init-output.log" in the guest to see where the process is blocked!
`,
			})
	}
	return req
}
