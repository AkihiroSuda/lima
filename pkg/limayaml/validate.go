package limayaml

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"github.com/containerd/containerd/identifiers"
	"github.com/coreos/go-semver/semver"
	"github.com/docker/go-units"
	"github.com/lima-vm/lima/pkg/localpathutil"
	"github.com/lima-vm/lima/pkg/networks"
	"github.com/lima-vm/lima/pkg/osutil"
	"github.com/lima-vm/lima/pkg/version"
	"github.com/lima-vm/lima/pkg/version/versionutil"
	"github.com/sirupsen/logrus"
)

func validateFileObject(f File, fieldName string) error {
	if !strings.Contains(f.Location, "://") {
		if _, err := localpathutil.Expand(f.Location); err != nil {
			return fmt.Errorf("field `%s.location` refers to an invalid local file path: %q: %w", fieldName, f.Location, err)
		}
		// f.Location does NOT need to be accessible, so we do NOT check os.Stat(f.Location)
	}
	switch f.Arch {
	case X8664, AARCH64, ARMV7L, RISCV64:
	default:
		return fmt.Errorf("field `arch` must be %q, %q, %q, or %q; got %q", X8664, AARCH64, ARMV7L, RISCV64, f.Arch)
	}
	if f.Digest != "" {
		if !f.Digest.Algorithm().Available() {
			return fmt.Errorf("field `%s.digest` refers to an unavailable digest algorithm", fieldName)
		}
		if err := f.Digest.Validate(); err != nil {
			return fmt.Errorf("field `%s.digest` is invalid: %s: %w", fieldName, f.Digest.String(), err)
		}
	}
	return nil
}

func Validate(y *LimaYAML, warn bool) error {
	if y.MinimumLimaVersion != nil {
		if _, err := versionutil.Parse(*y.MinimumLimaVersion); err != nil {
			return fmt.Errorf("field `minimumLimaVersion` must be a semvar value, got %q: %w", *y.MinimumLimaVersion, err)
		}
		limaVersion, err := versionutil.Parse(version.Version)
		if err != nil {
			return fmt.Errorf("can't parse builtin Lima version %q: %w", version.Version, err)
		}
		if versionutil.GreaterThan(*y.MinimumLimaVersion, limaVersion.String()) {
			return fmt.Errorf("template requires Lima version %q; this is only %q", *y.MinimumLimaVersion, limaVersion.String())
		}
	}
	if y.VMOpts.QEMU.MinimumVersion != nil {
		if _, err := semver.NewVersion(*y.VMOpts.QEMU.MinimumVersion); err != nil {
			return fmt.Errorf("field `vmOpts.qemu.minimumVersion` must be a semvar value, got %q: %w", *y.VMOpts.QEMU.MinimumVersion, err)
		}
	}
	switch *y.OS {
	case LINUX:
	default:
		return fmt.Errorf("field `os` must be %q; got %q", LINUX, *y.OS)
	}
	switch *y.Arch {
	case X8664, AARCH64, ARMV7L, RISCV64:
	default:
		return fmt.Errorf("field `arch` must be %q, %q, %q or %q; got %q", X8664, AARCH64, ARMV7L, RISCV64, *y.Arch)
	}

	switch *y.VMType {
	case QEMU:
		// NOP
	case WSL2:
		// NOP
	case VZ:
		if !IsNativeArch(*y.Arch) {
			return fmt.Errorf("field `arch` must be %q for VZ; got %q", NewArch(runtime.GOARCH), *y.Arch)
		}
	default:
		return fmt.Errorf("field `vmType` must be %q, %q, %q; got %q", QEMU, VZ, WSL2, *y.VMType)
	}

	if len(y.Images) == 0 {
		return errors.New("field `images` must be set")
	}
	for i, f := range y.Images {
		if err := validateFileObject(f.File, fmt.Sprintf("images[%d]", i)); err != nil {
			return err
		}
		if f.Kernel != nil {
			if err := validateFileObject(f.Kernel.File, fmt.Sprintf("images[%d].kernel", i)); err != nil {
				return err
			}
			if f.Kernel.Arch != f.Arch {
				return fmt.Errorf("images[%d].kernel has unexpected architecture %q, must be %q", i, f.Kernel.Arch, f.Arch)
			}
		}
		if f.Initrd != nil {
			if err := validateFileObject(*f.Initrd, fmt.Sprintf("images[%d].initrd", i)); err != nil {
				return err
			}
			if f.Kernel == nil {
				return errors.New("initrd requires the kernel to be specified")
			}
			if f.Initrd.Arch != f.Arch {
				return fmt.Errorf("images[%d].initrd has unexpected architecture %q, must be %q", i, f.Initrd.Arch, f.Arch)
			}
		}
	}

	for arch := range y.CPUType {
		switch arch {
		case AARCH64, X8664, ARMV7L, RISCV64:
			// these are the only supported architectures
		default:
			return fmt.Errorf("field `cpuType` uses unsupported arch %q", arch)
		}
	}

	if *y.CPUs == 0 {
		return errors.New("field `cpus` must be set")
	}

	if _, err := units.RAMInBytes(*y.Memory); err != nil {
		return fmt.Errorf("field `memory` has an invalid value: %w", err)
	}

	if _, err := units.RAMInBytes(*y.Disk); err != nil {
		return fmt.Errorf("field `memory` has an invalid value: %w", err)
	}

	for i, disk := range y.AdditionalDisks {
		if err := identifiers.Validate(disk.Name); err != nil {
			return fmt.Errorf("field `additionalDisks[%d].name is invalid`: %w", i, err)
		}
	}

	for i, f := range y.Mounts {
		if !filepath.IsAbs(f.Location) && !strings.HasPrefix(f.Location, "~") {
			return fmt.Errorf("field `mounts[%d].location` must be an absolute path, got %q",
				i, f.Location)
		}
		loc, err := localpathutil.Expand(f.Location)
		if err != nil {
			return fmt.Errorf("field `mounts[%d].location` refers to an unexpandable path: %q: %w", i, f.Location, err)
		}
		st, err := os.Stat(loc)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("field `mounts[%d].location` refers to an inaccessible path: %q: %w", i, f.Location, err)
			}
		} else if !st.IsDir() {
			return fmt.Errorf("field `mounts[%d].location` refers to a non-directory path: %q: %w", i, f.Location, err)
		}

		switch *f.MountPoint {
		case "/", "/bin", "/dev", "/etc", "/home", "/opt", "/sbin", "/tmp", "/usr", "/var":
			return fmt.Errorf("field `mounts[%d].mountPoint` must not be a system path such as /etc or /usr", i)
		// home directory defined in "cidata.iso:/user-data"
		case *y.User.Home:
			return fmt.Errorf("field `mounts[%d].mountPoint` is the reserved internal home directory %q", i, *y.User.Home)
		}

		if _, err := units.RAMInBytes(*f.NineP.Msize); err != nil {
			return fmt.Errorf("field `msize` has an invalid value: %w", err)
		}
	}

	if *y.SSH.LocalPort != 0 {
		if err := validatePort("ssh.localPort", *y.SSH.LocalPort); err != nil {
			return err
		}
	}

	switch *y.MountType {
	case REVSSHFS, NINEP, VIRTIOFS, WSLMount:
	default:
		return fmt.Errorf("field `mountType` must be %q or %q or %q, or %q, got %q", REVSSHFS, NINEP, VIRTIOFS, WSLMount, *y.MountType)
	}

	for _, f := range y.MountTypesUnsupported {
		if f == *y.MountType {
			return fmt.Errorf("field `mountType` must not be one of %v (`mountTypesUnsupported`), got %q", y.MountTypesUnsupported, *y.MountType)
		}
	}

	if warn && runtime.GOOS != "linux" {
		for i, mount := range y.Mounts {
			if mount.Virtiofs.QueueSize != nil {
				logrus.Warnf("field mounts[%d].virtiofs.queueSize is only supported on Linux", i)
			}
		}
	}

	// y.Firmware.LegacyBIOS is ignored for aarch64, but not a fatal error.

	for i, p := range y.Provision {
		switch p.Mode {
		case ProvisionModeSystem, ProvisionModeUser, ProvisionModeBoot:
			if p.SkipDefaultDependencyResolution != nil {
				return fmt.Errorf("field `provision[%d].mode` cannot set skipDefaultDependencyResolution, only valid on scripts of type %q",
					i, ProvisionModeDependency)
			}
		case ProvisionModeDependency:
		case ProvisionModeAnsible:
		default:
			return fmt.Errorf("field `provision[%d].mode` must one of %q, %q, %q, %q, or %q",
				i, ProvisionModeSystem, ProvisionModeUser, ProvisionModeBoot, ProvisionModeDependency, ProvisionModeAnsible)
		}
		if p.Playbook != "" {
			if p.Mode != ProvisionModeAnsible {
				return fmt.Errorf("field `provision[%d].mode must be %q if playbook is set", i, ProvisionModeAnsible)
			}
			if p.Script != "" {
				return fmt.Errorf("field `provision[%d].script must be empty if playbook is set", i)
			}
			playbook := p.Playbook
			if _, err := os.Stat(playbook); err != nil {
				return fmt.Errorf("field `provision[%d].playbook` refers to an inaccessible path: %q: %w", i, playbook, err)
			}
		}
		if strings.Contains(p.Script, "LIMA_CIDATA") {
			logrus.Warn("provisioning scripts should not reference the LIMA_CIDATA variables")
		}
	}
	needsContainerdArchives := (y.Containerd.User != nil && *y.Containerd.User) || (y.Containerd.System != nil && *y.Containerd.System)
	if needsContainerdArchives {
		if len(y.Containerd.Archives) == 0 {
			return errors.New("field `containerd.archives` must be provided")
		}
		for i, f := range y.Containerd.Archives {
			if err := validateFileObject(f, fmt.Sprintf("containerd.archives[%d]", i)); err != nil {
				return err
			}
		}
	}
	for i, p := range y.Probes {
		if !strings.HasPrefix(p.Script, "#!") {
			return fmt.Errorf("field `probe[%d].script` must start with a '#!' line", i)
		}
		switch p.Mode {
		case ProbeModeReadiness:
		default:
			return fmt.Errorf("field `probe[%d].mode` can only be %q", i, ProbeModeReadiness)
		}
	}
	for i, rule := range y.PortForwards {
		field := fmt.Sprintf("portForwards[%d]", i)
		if rule.GuestIPMustBeZero && !rule.GuestIP.Equal(net.IPv4zero) {
			return fmt.Errorf("field `%s.guestIPMustBeZero` can only be true when field `%s.guestIP` is 0.0.0.0", field, field)
		}
		if rule.GuestPort != 0 {
			if rule.GuestSocket != "" {
				return fmt.Errorf("field `%s.guestPort` must be 0 when field `%s.guestSocket` is set", field, field)
			}
			if rule.GuestPort != rule.GuestPortRange[0] {
				return fmt.Errorf("field `%s.guestPort` must match field `%s.guestPortRange[0]`", field, field)
			}
			// redundant validation to make sure the error contains the correct field name
			if err := validatePort(field+".guestPort", rule.GuestPort); err != nil {
				return err
			}
		}
		if rule.HostPort != 0 {
			if rule.HostSocket != "" {
				return fmt.Errorf("field `%s.hostPort` must be 0 when field `%s.hostSocket` is set", field, field)
			}
			if rule.HostPort != rule.HostPortRange[0] {
				return fmt.Errorf("field `%s.hostPort` must match field `%s.hostPortRange[0]`", field, field)
			}
			// redundant validation to make sure the error contains the correct field name
			if err := validatePort(field+".hostPort", rule.HostPort); err != nil {
				return err
			}
		}
		for j := 0; j < 2; j++ {
			if err := validatePort(fmt.Sprintf("%s.guestPortRange[%d]", field, j), rule.GuestPortRange[j]); err != nil {
				return err
			}
			if err := validatePort(fmt.Sprintf("%s.hostPortRange[%d]", field, j), rule.HostPortRange[j]); err != nil {
				return err
			}
		}
		if rule.GuestPortRange[0] > rule.GuestPortRange[1] {
			return fmt.Errorf("field `%s.guestPortRange[1]` must be greater than or equal to field `%s.guestPortRange[0]`", field, field)
		}
		if rule.HostPortRange[0] > rule.HostPortRange[1] {
			return fmt.Errorf("field `%s.hostPortRange[1]` must be greater than or equal to field `%s.hostPortRange[0]`", field, field)
		}
		if rule.GuestPortRange[1]-rule.GuestPortRange[0] != rule.HostPortRange[1]-rule.HostPortRange[0] {
			return fmt.Errorf("field `%s.hostPortRange` must specify the same number of ports as field `%s.guestPortRange`", field, field)
		}
		if rule.GuestSocket != "" {
			if !path.IsAbs(rule.GuestSocket) {
				return fmt.Errorf("field `%s.guestSocket` must be an absolute path, but is %q", field, rule.GuestSocket)
			}
			if rule.HostSocket == "" && rule.HostPortRange[1]-rule.HostPortRange[0] > 0 {
				return fmt.Errorf("field `%s.guestSocket` can only be mapped to a single port or socket. not a range", field)
			}
		}
		if rule.HostSocket != "" {
			if !filepath.IsAbs(rule.HostSocket) {
				// should be unreachable because FillDefault() will prepend the instance directory to relative names
				return fmt.Errorf("field `%s.hostSocket` must be an absolute path, but is %q", field, rule.HostSocket)
			}
			if rule.GuestSocket == "" && rule.GuestPortRange[1]-rule.GuestPortRange[0] > 0 {
				return fmt.Errorf("field `%s.hostSocket` can only be mapped from a single port or socket. not a range", field)
			}
		}
		if len(rule.HostSocket) >= osutil.UnixPathMax {
			return fmt.Errorf("field `%s.hostSocket` must be less than UNIX_PATH_MAX=%d characters, but is %d",
				field, osutil.UnixPathMax, len(rule.HostSocket))
		}
		switch rule.Proto {
		case ProtoTCP, ProtoUDP, ProtoAny:
		default:
			return fmt.Errorf("field `%s.proto` must be %q, %q, or %q", field, ProtoTCP, ProtoUDP, ProtoAny)
		}
		if rule.Reverse && rule.GuestSocket == "" {
			return fmt.Errorf("field `%s.reverse` must be %t", field, false)
		}
		if rule.Reverse && rule.HostSocket == "" {
			return fmt.Errorf("field `%s.reverse` must be %t", field, false)
		}
		// Not validating that the various GuestPortRanges and HostPortRanges are not overlapping. Rules will be
		// processed sequentially and the first matching rule for a guest port determines forwarding behavior.
	}
	for i, rule := range y.CopyToHost {
		field := fmt.Sprintf("CopyToHost[%d]", i)
		if rule.GuestFile != "" {
			if !path.IsAbs(rule.GuestFile) {
				return fmt.Errorf("field `%s.guest` must be an absolute path, but is %q", field, rule.GuestFile)
			}
		}
		if rule.HostFile != "" {
			if !filepath.IsAbs(rule.HostFile) {
				return fmt.Errorf("field `%s.host` must be an absolute path, but is %q", field, rule.HostFile)
			}
		}
	}

	if y.HostResolver.Enabled != nil && *y.HostResolver.Enabled && len(y.DNS) > 0 {
		return errors.New("field `dns` must be empty when field `HostResolver.Enabled` is true")
	}

	if err := validateNetwork(y); err != nil {
		return err
	}
	if warn {
		warnExperimental(y)
	}

	// Validate Param settings
	// Names must start with a letter, followed by any number of letters, digits, or underscores
	validParamName := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	for param, value := range y.Param {
		if !validParamName.MatchString(param) {
			return fmt.Errorf("param %q name does not match regex %q", param, validParamName.String())
		}
		for _, r := range value {
			if !unicode.IsPrint(r) && r != '\t' && r != ' ' {
				return fmt.Errorf("param %q value contains unprintable character %q", param, r)
			}
		}
	}

	return nil
}

func validateNetwork(y *LimaYAML) error {
	interfaceName := make(map[string]int)
	for i, nw := range y.Networks {
		field := fmt.Sprintf("networks[%d]", i)
		switch {
		case nw.Lima != "":
			nwCfg, err := networks.LoadConfig()
			if err != nil {
				return err
			}
			if nwCfg.Check(nw.Lima) != nil {
				return fmt.Errorf("field `%s.lima` references network %q which is not defined in networks.yaml", field, nw.Lima)
			}
			usernet, err := nwCfg.Usernet(nw.Lima)
			if err != nil {
				return err
			}
			if !usernet && runtime.GOOS != "darwin" {
				return fmt.Errorf("field `%s.lima` is only supported on macOS right now", field)
			}
			if nw.Socket != "" {
				return fmt.Errorf("field `%s.lima` and field `%s.socket` are mutually exclusive", field, field)
			}
			if nw.VZNAT != nil && *nw.VZNAT {
				return fmt.Errorf("field `%s.lima` and field `%s.vzNAT` are mutually exclusive", field, field)
			}
		case nw.Socket != "":
			if nw.VZNAT != nil && *nw.VZNAT {
				return fmt.Errorf("field `%s.socket` and field `%s.vzNAT` are mutually exclusive", field, field)
			}
			if fi, err := os.Stat(nw.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			} else if err == nil && fi.Mode()&os.ModeSocket == 0 {
				return fmt.Errorf("field `%s.socket` %q points to a non-socket file", field, nw.Socket)
			}
		case nw.VZNAT != nil && *nw.VZNAT:
			if y.VMType == nil || *y.VMType != VZ {
				return fmt.Errorf("field `%s.vzNAT` requires `vmType` to be %q", field, VZ)
			}
			if nw.Lima != "" {
				return fmt.Errorf("field `%s.vzNAT` and field `%s.lima` are mutually exclusive", field, field)
			}
			if nw.Socket != "" {
				return fmt.Errorf("field `%s.vzNAT` and field `%s.socket` are mutually exclusive", field, field)
			}
		default:
			return fmt.Errorf("field `%s.lima` or  field `%s.socket must be set", field, field)
		}
		if nw.MACAddress != "" {
			hw, err := net.ParseMAC(nw.MACAddress)
			if err != nil {
				return fmt.Errorf("field `vmnet.mac` invalid: %w", err)
			}
			if len(hw) != 6 {
				return fmt.Errorf("field `%s.macAddress` must be a 48 bit (6 bytes) MAC address; actual length of %q is %d bytes", field, nw.MACAddress, len(hw))
			}
		}
		// FillDefault() will make sure that nw.Interface is not the empty string
		if len(nw.Interface) >= 16 {
			return fmt.Errorf("field `%s.interface` must be less than 16 bytes, but is %d bytes: %q", field, len(nw.Interface), nw.Interface)
		}
		if strings.ContainsAny(nw.Interface, " \t\n/") {
			return fmt.Errorf("field `%s.interface` must not contain whitespace or slashes", field)
		}
		if nw.Interface == networks.SlirpNICName {
			return fmt.Errorf("field `%s.interface` must not be set to %q because it is reserved for slirp", field, networks.SlirpNICName)
		}
		if prev, ok := interfaceName[nw.Interface]; ok {
			return fmt.Errorf("field `%s.interface` value %q has already been used by field `networks[%d].interface`", field, nw.Interface, prev)
		}
		interfaceName[nw.Interface] = i
	}
	return nil
}

// ValidateParamIsUsed checks if the keys in the `param` field are used in any script, probe, copyToHost, or portForward.
// It should be called before the `y` parameter is passed to FillDefault() that execute template.
func ValidateParamIsUsed(y *LimaYAML) error {
	for key := range y.Param {
		re, err := regexp.Compile(`{{[^}]*\.Param\.` + key + `[^}]*}}|\bPARAM_` + key + `\b`)
		if err != nil {
			return fmt.Errorf("field to compile regexp for key %q: %w", key, err)
		}
		keyIsUsed := false
		for _, p := range y.Provision {
			if re.MatchString(p.Script) {
				keyIsUsed = true
				break
			}
		}
		for _, p := range y.Probes {
			if re.MatchString(p.Script) {
				keyIsUsed = true
				break
			}
		}
		for _, p := range y.CopyToHost {
			if re.MatchString(p.GuestFile) || re.MatchString(p.HostFile) {
				keyIsUsed = true
				break
			}
		}
		for _, p := range y.PortForwards {
			if re.MatchString(p.GuestSocket) || re.MatchString(p.HostSocket) {
				keyIsUsed = true
				break
			}
		}
		for _, p := range y.Mounts {
			if re.MatchString(p.Location) {
				keyIsUsed = true
				break
			}
			if p.MountPoint != nil && re.MatchString(*p.MountPoint) {
				keyIsUsed = true
				break
			}
		}
		if !keyIsUsed {
			return fmt.Errorf("field `param` key %q is not used in any provision, probe, copyToHost, or portForward", key)
		}
	}
	return nil
}

func validatePort(field string, port int) error {
	switch {
	case port < 0:
		return fmt.Errorf("field `%s` must be > 0", field)
	case port == 0:
		return fmt.Errorf("field `%s` must be set", field)
	case port == 22:
		return fmt.Errorf("field `%s` must not be 22", field)
	case port > 65535:
		return fmt.Errorf("field `%s` must be < 65536", field)
	}
	return nil
}

func warnExperimental(y *LimaYAML) {
	if *y.MountType == VIRTIOFS && runtime.GOOS == "linux" {
		logrus.Warn("`mountType: virtiofs` on Linux is experimental")
	}
	if *y.Arch == RISCV64 {
		logrus.Warn("`arch: riscv64` is experimental")
	}
	if y.Video.Display != nil && strings.Contains(*y.Video.Display, "vnc") {
		logrus.Warn("`video.display: vnc` is experimental")
	}
	if y.Audio.Device != nil && *y.Audio.Device != "" {
		logrus.Warn("`audio.device` is experimental")
	}
	if y.MountInotify != nil && *y.MountInotify {
		logrus.Warn("`mountInotify` is experimental")
	}
}
