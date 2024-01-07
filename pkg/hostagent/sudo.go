package hostagent

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/lima-vm/sshocker/pkg/ssh"
	"github.com/sirupsen/logrus"
)

// sudoExecuteScript executes the given script (as root) on the remote host via stdin.
// Returns stdout and stderr.
//
// scriptName is used only for readability of error strings.
func sudoExecuteScript(host string, port int, c *ssh.SSHConfig, script, scriptName string) (stdout, stderr string, err error) {
	if c == nil {
		return "", "", errors.New("got nil SSHConfig")
	}
	interpreter, err := ssh.ParseScriptInterpreter(script)
	if err != nil {
		return "", "", err
	}
	sshBinary := c.Binary()
	sshArgs := c.Args()
	if port != 0 {
		sshArgs = append(sshArgs, "-p", strconv.Itoa(port))
	}
	sshArgs = append(sshArgs, host, "--", "sudo", interpreter)
	sshCmd := exec.Command(sshBinary, sshArgs...)
	sshCmd.Stdin = strings.NewReader(script)
	var buf bytes.Buffer
	sshCmd.Stderr = &buf
	logrus.Debugf("executing ssh for script %q: %s %v", scriptName, sshCmd.Path, sshCmd.Args)
	out, err := sshCmd.Output()
	if err != nil {
		return string(out), buf.String(), fmt.Errorf("failed to execute script %q: stdout=%q, stderr=%q: %w", scriptName, string(out), buf.String(), err)
	}
	return string(out), buf.String(), nil
}
