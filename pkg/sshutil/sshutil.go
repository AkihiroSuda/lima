package sshutil

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/lima-vm/lima/pkg/ioutilx"
	"github.com/lima-vm/lima/pkg/lockutil"
	"github.com/lima-vm/lima/pkg/osutil"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/mattn/go-shellwords"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/cpu"
)

// Environment variable that allows configuring the command (alias) to execute
// in place of the 'ssh' executable.
const EnvShellSSH = "SSH"

func SSHArguments() (arg0 string, arg0Args []string, err error) {
	if sshShell := os.Getenv(EnvShellSSH); sshShell != "" {
		sshShellFields, err := shellwords.Parse(sshShell)
		switch {
		case err != nil:
			logrus.WithError(err).Warnf("Failed to split %s variable into shell tokens. "+
				"Falling back to 'ssh' command", EnvShellSSH)
		case len(sshShellFields) > 0:
			arg0 = sshShellFields[0]
			if len(sshShellFields) > 1 {
				arg0Args = sshShellFields[1:]
			}
		}
	}

	if arg0 == "" {
		arg0, err = exec.LookPath("ssh")
		if err != nil {
			return "", []string{""}, err
		}
	}

	return arg0, arg0Args, nil
}

type PubKey struct {
	Filename string
	Content  string
}

func readPublicKey(f string) (PubKey, error) {
	entry := PubKey{
		Filename: f,
	}
	content, err := os.ReadFile(f)
	if err == nil {
		entry.Content = strings.TrimSpace(string(content))
	} else {
		err = fmt.Errorf("failed to read ssh public key %q: %w", f, err)
	}
	return entry, err
}

// DefaultPubKeys returns the public key from $LIMA_HOME/_config/user.pub.
// The key will be created if it does not yet exist.
//
// When loadDotSSH is true, ~/.ssh/*.pub will be appended to make the VM accessible without specifying
// an identity explicitly.
func DefaultPubKeys(loadDotSSH bool) ([]PubKey, error) {
	// Read $LIMA_HOME/_config/user.pub
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(filepath.Join(configDir, filenames.UserPrivateKey))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			return nil, fmt.Errorf("could not create %q directory: %w", configDir, err)
		}
		if err := lockutil.WithDirLock(configDir, func() error {
			// no passphrase, no user@host comment
			keygenCmd := exec.Command("ssh-keygen", "-t", "ed25519", "-q", "-N", "",
				"-C", "lima", "-f", filepath.Join(configDir, filenames.UserPrivateKey))
			logrus.Debugf("executing %v", keygenCmd.Args)
			if out, err := keygenCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to run %v: %q: %w", keygenCmd.Args, string(out), err)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	entry, err := readPublicKey(filepath.Join(configDir, filenames.UserPublicKey))
	if err != nil {
		return nil, err
	}
	res := []PubKey{entry}

	if !loadDotSSH {
		return res, nil
	}

	// Append all of ~/.ssh/*.pub
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(homeDir, ".ssh/*.pub"))
	if err != nil {
		panic(err) // Only possible error is ErrBadPattern, so this should be unreachable.
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".pub") {
			panic(fmt.Errorf("unexpected ssh public key filename %q", f))
		}
		entry, err := readPublicKey(f)
		if err == nil {
			if !detectValidPublicKey(entry.Content) {
				logrus.Warnf("public key %q doesn't seem to be in ssh format", entry.Filename)
			} else {
				res = append(res, entry)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return res, nil
}

var sshInfo struct {
	sync.Once
	// aesAccelerated is set to true when AES acceleration is available.
	// Available on almost all modern Intel/AMD processors.
	aesAccelerated bool
	// openSSHVersion is set to the version of OpenSSH, or semver.New("0.0.0") if the version cannot be determined.
	openSSHVersion semver.Version
}

func IsLocalhost(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// CommonOpts returns ssh option key-value pairs like {"IdentityFile=/path/to/id_foo"}.
// The result may contain different values with the same key.
//
// The result always contains the IdentityFile option.
// The result never contains the Port option.
func CommonOpts(sshPath string, useDotSSH, localhost bool) ([]string, error) {
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return nil, err
	}
	privateKeyPath := filepath.Join(configDir, filenames.UserPrivateKey)
	_, err = os.Stat(privateKeyPath)
	if err != nil {
		return nil, err
	}
	var opts []string
	if runtime.GOOS == "windows" {
		privateKeyPath = ioutilx.CanonicalWindowsPath(privateKeyPath)
		opts = []string{fmt.Sprintf(`IdentityFile='%s'`, privateKeyPath)}
	} else {
		opts = []string{fmt.Sprintf(`IdentityFile="%s"`, privateKeyPath)}
	}

	// Append all private keys corresponding to ~/.ssh/*.pub to keep old instances working
	// that had been created before lima started using an internal identity.
	if useDotSSH {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		files, err := filepath.Glob(filepath.Join(homeDir, ".ssh/*.pub"))
		if err != nil {
			panic(err) // Only possible error is ErrBadPattern, so this should be unreachable.
		}
		for _, f := range files {
			if !strings.HasSuffix(f, ".pub") {
				panic(fmt.Errorf("unexpected ssh public key filename %q", f))
			}
			privateKeyPath := strings.TrimSuffix(f, ".pub")
			_, err = os.Stat(privateKeyPath)
			if errors.Is(err, fs.ErrNotExist) {
				// Skip .pub files without a matching private key. This is reasonably common,
				// due to major projects like Vault recommending the ${name}-cert.pub format
				// for SSH certificate files.
				//
				// e.g. https://www.vaultproject.io/docs/secrets/ssh/signed-ssh-certificates
				continue
			}
			if err != nil {
				// Fail on permission-related and other path errors
				return nil, err
			}
			if runtime.GOOS == "windows" {
				opts = append(opts, fmt.Sprintf(`IdentityFile='%s'`, privateKeyPath))
			} else {
				opts = append(opts, fmt.Sprintf(`IdentityFile="%s"`, privateKeyPath))
			}
		}
	}

	if localhost {
		opts = append(opts,
			"StrictHostKeyChecking=no",
			"UserKnownHostsFile=/dev/null",
			"BatchMode=yes",
		)
	}

	opts = append(opts,
		"NoHostAuthenticationForLocalhost=yes",
		"GSSAPIAuthentication=no",
		"PreferredAuthentications=publickey",
		"Compression=no",
		"PasswordAuthentication=no",
		"IdentitiesOnly=yes",
	)

	sshInfo.Do(func() {
		sshInfo.aesAccelerated = detectAESAcceleration()
		sshInfo.openSSHVersion = DetectOpenSSHVersion(sshPath)
	})

	// Only OpenSSH version 8.1 and later support adding ciphers to the front of the default set
	if !sshInfo.openSSHVersion.LessThan(*semver.New("8.1.0")) {
		// By default, `ssh` choose chacha20-poly1305@openssh.com, even when AES accelerator is available.
		// (OpenSSH_8.1p1, macOS 11.6, MacBookPro 2020, Core i7-1068NG7)
		//
		// We prioritize AES algorithms when AES accelerator is available.
		if sshInfo.aesAccelerated {
			logrus.Debugf("AES accelerator seems available, prioritizing aes128-gcm@openssh.com and aes256-gcm@openssh.com")
			if runtime.GOOS == "windows" {
				opts = append(opts, "Ciphers=^aes128-gcm@openssh.com,aes256-gcm@openssh.com")
			} else {
				opts = append(opts, "Ciphers=\"^aes128-gcm@openssh.com,aes256-gcm@openssh.com\"")
			}
		} else {
			logrus.Debugf("AES accelerator does not seem available, prioritizing chacha20-poly1305@openssh.com")
			if runtime.GOOS == "windows" {
				opts = append(opts, "Ciphers=^chacha20-poly1305@openssh.com")
			} else {
				opts = append(opts, "Ciphers=\"^chacha20-poly1305@openssh.com\"")
			}
		}
	}
	return opts, nil
}

// SSHOpts adds the following options to CommonOptions: User, ControlMaster, ControlPath, ControlPersist.
func SSHOpts(sshPath, instDir, username string, useDotSSH bool, hostAddress string, forwardAgent, forwardX11, forwardX11Trusted bool) ([]string, error) {
	controlSock := filepath.Join(instDir, filenames.SSHSock)
	if len(controlSock) >= osutil.UnixPathMax {
		return nil, fmt.Errorf("socket path %q is too long: >= UNIX_PATH_MAX=%d", controlSock, osutil.UnixPathMax)
	}
	opts, err := CommonOpts(sshPath, useDotSSH, IsLocalhost(hostAddress))
	if err != nil {
		return nil, err
	}
	controlPath := fmt.Sprintf(`ControlPath="%s"`, controlSock)
	if runtime.GOOS == "windows" {
		controlSock = ioutilx.CanonicalWindowsPath(controlSock)
		controlPath = fmt.Sprintf(`ControlPath='%s'`, controlSock)
	}
	opts = append(opts,
		fmt.Sprintf("User=%s", username), // guest and host have the same username, but we should specify the username explicitly (#85)
		"ControlMaster=auto",
		controlPath,
		"ControlPersist=yes",
	)
	if forwardAgent {
		opts = append(opts, "ForwardAgent=yes")
	}
	if forwardX11 {
		opts = append(opts, "ForwardX11=yes")
	}
	if forwardX11Trusted {
		opts = append(opts, "ForwardX11Trusted=yes")
	}
	return opts, nil
}

// SSHArgsFromOpts returns ssh args from opts.
// The result always contains {"-F", "/dev/null} in addition to {"-o", "KEY=VALUE", ...}.
func SSHArgsFromOpts(opts []string) []string {
	args := []string{"-F", "/dev/null"}
	for _, o := range opts {
		args = append(args, "-o", o)
	}
	return args
}

func ParseOpenSSHVersion(version []byte) *semver.Version {
	regex := regexp.MustCompile(`^OpenSSH_(\d+\.\d+)(?:p(\d+))?\b`)
	matches := regex.FindSubmatch(version)
	if len(matches) == 3 {
		if len(matches[2]) == 0 {
			matches[2] = []byte("0")
		}
		return semver.New(fmt.Sprintf("%s.%s", matches[1], matches[2]))
	}
	return &semver.Version{}
}

// sshExecutable beyond path also records size and mtime, in the case of ssh upgrades.
type sshExecutable struct {
	Path    string
	Size    int64
	ModTime time.Time
}

var (
	// sshVersions caches the parsed version of each ssh executable, if it is needed again.
	sshVersions   = map[sshExecutable]*semver.Version{}
	sshVersionsRW sync.RWMutex
)

func DetectOpenSSHVersion(ssh string) semver.Version {
	var (
		v      semver.Version
		exe    sshExecutable
		stderr bytes.Buffer
	)
	path, err := exec.LookPath(ssh)
	if err != nil {
		logrus.Warnf("failed to find ssh executable: %v", err)
	} else {
		st, _ := os.Stat(path)
		exe = sshExecutable{Path: path, Size: st.Size(), ModTime: st.ModTime()}
		sshVersionsRW.RLock()
		ver := sshVersions[exe]
		sshVersionsRW.RUnlock()
		if ver != nil {
			return *ver
		}
	}
	cmd := exec.Command(path, "-V")
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		logrus.Warnf("failed to run %v: stderr=%q", cmd.Args, stderr.String())
	} else {
		v = *ParseOpenSSHVersion(stderr.Bytes())
		logrus.Debugf("OpenSSH version %s detected", v)
		sshVersionsRW.Lock()
		sshVersions[exe] = &v
		sshVersionsRW.Unlock()
	}
	return v
}

// detectValidPublicKey returns whether content represent a public key.
// OpenSSH public key format have the structure of '<algorithm> <key> <comment>'.
// By checking 'algorithm' with signature format identifier in 'key' part,
// this function may report false positive but provide better compatibility.
func detectValidPublicKey(content string) bool {
	if strings.ContainsRune(content, '\n') {
		return false
	}
	spaced := strings.SplitN(content, " ", 3)
	if len(spaced) < 2 {
		return false
	}
	algo, base64Key := spaced[0], spaced[1]
	decodedKey, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil || len(decodedKey) < 4 {
		return false
	}
	sigLength := binary.BigEndian.Uint32(decodedKey)
	if uint32(len(decodedKey)) < sigLength {
		return false
	}
	sigFormat := string(decodedKey[4 : 4+sigLength])
	return algo == sigFormat
}

func detectAESAcceleration() bool {
	if !cpu.Initialized {
		if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
			// cpu.Initialized seems to always be false, even when the cpu.ARM64 struct is filled out
			// it is only being set by readARM64Registers, but not by readHWCAP or readLinuxProcCPUInfo
			return cpu.ARM64.HasAES
		}
		if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			// golang.org/x/sys/cpu supports darwin/amd64, linux/amd64, and linux/arm64,
			// but apparently lacks support for darwin/arm64: https://github.com/golang/sys/blob/v0.5.0/cpu/cpu_arm64.go#L43-L60
			//
			// According to https://gist.github.com/voluntas/fd279c7b4e71f9950cfd4a5ab90b722b ,
			// aes-128-gcm is faster than chacha20-poly1305 on Apple M1.
			//
			// So we return `true` here.
			//
			// This workaround will not be needed when https://go-review.googlesource.com/c/sys/+/332729 is merged.
			logrus.Debug("Failed to detect CPU features. Assuming that AES acceleration is available on this Apple silicon.")
			return true
		}
		logrus.Warn("Failed to detect CPU features. Assuming that AES acceleration is not available.")
		return false
	}
	return cpu.ARM.HasAES || cpu.ARM64.HasAES || cpu.S390X.HasAES || cpu.X86.HasAES
}
