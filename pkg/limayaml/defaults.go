package limayaml

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/coreos/go-semver/semver"
	"github.com/docker/go-units"
	"github.com/goccy/go-yaml"
	"github.com/lima-vm/lima/pkg/version"
	"github.com/pbnjay/memory"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/cpu"

	"github.com/lima-vm/lima/pkg/identifierutil"
	. "github.com/lima-vm/lima/pkg/must"
	"github.com/lima-vm/lima/pkg/networks"
	"github.com/lima-vm/lima/pkg/osutil"
	"github.com/lima-vm/lima/pkg/ptr"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/lima-vm/lima/pkg/version/versionutil"
)

const (
	// Default9pSecurityModel is "none" for supporting symlinks
	// https://gitlab.com/qemu-project/qemu/-/issues/173
	Default9pSecurityModel   string = "none"
	Default9pProtocolVersion string = "9p2000.L"
	Default9pMsize           string = "128KiB"
	Default9pCacheForRO      string = "fscache"
	Default9pCacheForRW      string = "mmap"

	DefaultVirtiofsQueueSize int = 1024
)

var (
	IPv4loopback1 = net.IPv4(127, 0, 0, 1)

	userHomeDir = Must(os.UserHomeDir())
	currentUser = Must(user.Current())
)

func defaultCPUType() CPUType {
	cpuType := map[Arch]string{
		AARCH64: "cortex-a72",
		ARMV7L:  "cortex-a7",
		// Since https://github.com/lima-vm/lima/pull/494, we use qemu64 cpu for better emulation of x86_64.
		X8664:   "qemu64",
		RISCV64: "rv64", // FIXME: what is the right choice for riscv64?
	}
	for arch := range cpuType {
		if IsNativeArch(arch) && IsAccelOS() {
			if HasHostCPU() {
				cpuType[arch] = "host"
			} else if HasMaxCPU() {
				cpuType[arch] = "max"
			}
		}
		if arch == X8664 && runtime.GOOS == "darwin" {
			switch cpuType[arch] {
			case "host", "max":
				// disable AVX-512, since it requires trapping instruction faults in guest
				// Enterprise Linux requires either v2 (SSE4) or v3 (AVX2), but not yet v4.
				cpuType[arch] += ",-avx512vl"

				// Disable pdpe1gb on Intel Mac
				// https://github.com/lima-vm/lima/issues/1485
				// https://stackoverflow.com/a/72863744/5167443
				cpuType[arch] += ",-pdpe1gb"
			}
		}
	}
	return cpuType
}

//go:embed containerd.yaml
var defaultContainerdYAML []byte

type ContainerdYAML struct {
	Archives []File
}

func defaultContainerdArchives() []File {
	var containerd ContainerdYAML
	err := yaml.UnmarshalWithOptions(defaultContainerdYAML, &containerd, yaml.Strict())
	if err != nil {
		panic(fmt.Errorf("failed to unmarshal as YAML: %w", err))
	}
	return containerd.Archives
}

// FirstUsernetIndex gets the index of first usernet network under l.Network[]. Returns -1 if no usernet network found.
func FirstUsernetIndex(l *LimaYAML) int {
	return slices.IndexFunc(l.Networks, func(network Network) bool { return networks.IsUsernet(network.Lima) })
}

func MACAddress(uniqueID string) string {
	sha := sha256.Sum256([]byte(osutil.MachineID() + uniqueID))
	// "5" is the magic number in the Lima ecosystem.
	// (Visit https://en.wiktionary.org/wiki/lima and Command-F "five")
	//
	// But the second hex number is changed to 2 to satisfy the convention for
	// local MAC addresses (https://en.wikipedia.org/wiki/MAC_address#Ranges_of_group_and_locally_administered_addresses)
	//
	// See also https://gitlab.com/wireshark/wireshark/-/blob/release-4.0/manuf to confirm the uniqueness of this prefix.
	hw := append(net.HardwareAddr{0x52, 0x55, 0x55}, sha[0:3]...)
	return hw.String()
}

func hostTimeZone() string {
	// WSL2 will automatically set the timezone
	if runtime.GOOS != "windows" {
		tz, err := os.ReadFile("/etc/timezone")
		if err == nil {
			return strings.TrimSpace(string(tz))
		}
		zoneinfoFile, err := filepath.EvalSymlinks("/etc/localtime")
		if err == nil {
			for baseDir := filepath.Dir(zoneinfoFile); baseDir != "/"; baseDir = filepath.Dir(baseDir) {
				if _, err = os.Stat(filepath.Join(baseDir, "Etc/UTC")); err == nil {
					return strings.TrimPrefix(zoneinfoFile, baseDir+"/")
				}
			}
			logrus.Warnf("could not locate zoneinfo directory from %q", zoneinfoFile)
		}
	}
	return ""
}

func defaultCPUs() int {
	const x = 4
	if hostCPUs := runtime.NumCPU(); hostCPUs < x {
		return hostCPUs
	}
	return x
}

func defaultMemory() uint64 {
	const x uint64 = 4 * 1024 * 1024 * 1024
	if halfOfHostMemory := memory.TotalMemory() / 2; halfOfHostMemory < x {
		return halfOfHostMemory
	}
	return x
}

func defaultMemoryAsString() string {
	return units.BytesSize(float64(defaultMemory()))
}

func defaultDiskSizeAsString() string {
	// currently just hardcoded
	return "100GiB"
}

func defaultGuestInstallPrefix() string {
	return "/usr/local"
}

// FillDefault updates undefined fields in y with defaults from d (or built-in default), and overwrites with values from o.
// Both d and o may be empty.
//
// Maps (`Env`) are being merged: first populated from d, overwritten by y, and again overwritten by o.
// Slices (e.g. `Mounts`, `Provision`) are appended, starting with o, followed by y, and finally d. This
// makes sure o takes priority over y over d, in cases it matters (e.g. `PortForwards`, where the first
// matching rule terminates the search).
//
// Exceptions:
//   - Mounts are appended in d, y, o order, but "merged" when the Location matches a previous entry;
//     the highest priority Writable setting wins.
//   - Networks are appended in d, y, o order
//   - DNS are picked from the highest priority where DNS is not empty.
//   - CACertificates Files and Certs are uniquely appended in d, y, o order
func FillDefault(y, d, o *LimaYAML, filePath string, warn bool) {
	instDir := filepath.Dir(filePath)

	// existingLimaVersion can be empty if the instance was created with Lima prior to v0.20,
	var existingLimaVersion string
	if !isExistingInstanceDir(instDir) {
		existingLimaVersion = version.Version
	} else {
		limaVersionFile := filepath.Join(instDir, filenames.LimaVersion)
		if b, err := os.ReadFile(limaVersionFile); err == nil {
			existingLimaVersion = strings.TrimSpace(string(b))
		} else if !errors.Is(err, os.ErrNotExist) {
			logrus.WithError(err).Warnf("Failed to read %q", limaVersionFile)
		}
	}

	if y.User.Name == nil {
		y.User.Name = d.User.Name
	}
	if y.User.Comment == nil {
		y.User.Comment = d.User.Comment
	}
	if y.User.Home == nil {
		y.User.Home = d.User.Home
	}
	if y.User.UID == nil {
		y.User.UID = d.User.UID
	}
	if o.User.Name != nil {
		y.User.Name = o.User.Name
	}
	if o.User.Comment != nil {
		y.User.Comment = o.User.Comment
	}
	if o.User.Home != nil {
		y.User.Home = o.User.Home
	}
	if o.User.UID != nil {
		y.User.UID = o.User.UID
	}
	if y.User.Name == nil {
		y.User.Name = ptr.Of(osutil.LimaUser(existingLimaVersion, warn).Username)
		warn = false
	}
	if y.User.Comment == nil {
		y.User.Comment = ptr.Of(osutil.LimaUser(existingLimaVersion, warn).Name)
		warn = false
	}
	if y.User.Home == nil {
		y.User.Home = ptr.Of(osutil.LimaUser(existingLimaVersion, warn).HomeDir)
		warn = false
	}
	if y.User.UID == nil {
		uidString := osutil.LimaUser(existingLimaVersion, warn).Uid
		if uid, err := strconv.ParseUint(uidString, 10, 32); err == nil {
			y.User.UID = ptr.Of(uint32(uid))
		} else {
			// This should never happen; LimaUser() makes sure that .Uid is numeric
			logrus.WithError(err).Warnf("Can't parse `user.uid` %q", uidString)
			y.User.UID = ptr.Of(uint32(1000))
		}
		// warn = false
	}
	if out, err := executeGuestTemplate(*y.User.Home, instDir, y.User, y.Param); err == nil {
		y.User.Home = ptr.Of(out.String())
	} else {
		logrus.WithError(err).Warnf("Couldn't process `user.home` value %q as a template", *y.User.Home)
	}

	if y.VMType == nil {
		y.VMType = d.VMType
	}
	if o.VMType != nil {
		y.VMType = o.VMType
	}
	y.VMType = ptr.Of(ResolveVMType(y, d, o, filePath))
	if y.OS == nil {
		y.OS = d.OS
	}
	if o.OS != nil {
		y.OS = o.OS
	}
	y.OS = ptr.Of(ResolveOS(y.OS))
	if y.Arch == nil {
		y.Arch = d.Arch
	}
	if o.Arch != nil {
		y.Arch = o.Arch
	}
	y.Arch = ptr.Of(ResolveArch(y.Arch))

	y.Images = append(append(o.Images, y.Images...), d.Images...)
	for i := range y.Images {
		img := &y.Images[i]
		if img.Arch == "" {
			img.Arch = *y.Arch
		}
		if img.Kernel != nil && img.Kernel.Arch == "" {
			img.Kernel.Arch = img.Arch
		}
		if img.Initrd != nil && img.Initrd.Arch == "" {
			img.Initrd.Arch = img.Arch
		}
	}

	cpuType := defaultCPUType()
	var overrideCPUType bool
	for k, v := range d.CPUType {
		if v != "" {
			overrideCPUType = true
			cpuType[k] = v
		}
	}
	for k, v := range y.CPUType {
		if v != "" {
			overrideCPUType = true
			cpuType[k] = v
		}
	}
	for k, v := range o.CPUType {
		if v != "" {
			overrideCPUType = true
			cpuType[k] = v
		}
	}
	if *y.VMType == QEMU || overrideCPUType {
		y.CPUType = cpuType
	}

	if y.CPUs == nil {
		y.CPUs = d.CPUs
	}
	if o.CPUs != nil {
		y.CPUs = o.CPUs
	}
	if y.CPUs == nil || *y.CPUs == 0 {
		y.CPUs = ptr.Of(defaultCPUs())
	}

	if y.Memory == nil {
		y.Memory = d.Memory
	}
	if o.Memory != nil {
		y.Memory = o.Memory
	}
	if y.Memory == nil || *y.Memory == "" {
		y.Memory = ptr.Of(defaultMemoryAsString())
	}

	if y.Disk == nil {
		y.Disk = d.Disk
	}
	if o.Disk != nil {
		y.Disk = o.Disk
	}
	if y.Disk == nil || *y.Disk == "" {
		y.Disk = ptr.Of(defaultDiskSizeAsString())
	}

	y.AdditionalDisks = append(append(o.AdditionalDisks, y.AdditionalDisks...), d.AdditionalDisks...)

	if y.Audio.Device == nil {
		y.Audio.Device = d.Audio.Device
	}
	if o.Audio.Device != nil {
		y.Audio.Device = o.Audio.Device
	}
	if y.Audio.Device == nil {
		y.Audio.Device = ptr.Of("")
	}

	if y.Video.Display == nil {
		y.Video.Display = d.Video.Display
	}
	if o.Video.Display != nil {
		y.Video.Display = o.Video.Display
	}
	if y.Video.Display == nil || *y.Video.Display == "" {
		y.Video.Display = ptr.Of("none")
	}

	if y.Video.VNC.Display == nil {
		y.Video.VNC.Display = d.Video.VNC.Display
	}
	if o.Video.VNC.Display != nil {
		y.Video.VNC.Display = o.Video.VNC.Display
	}
	if (y.Video.VNC.Display == nil || *y.Video.VNC.Display == "") && *y.VMType == QEMU {
		y.Video.VNC.Display = ptr.Of("127.0.0.1:0,to=9")
	}

	if y.Firmware.LegacyBIOS == nil {
		y.Firmware.LegacyBIOS = d.Firmware.LegacyBIOS
	}
	if o.Firmware.LegacyBIOS != nil {
		y.Firmware.LegacyBIOS = o.Firmware.LegacyBIOS
	}
	if y.Firmware.LegacyBIOS == nil {
		y.Firmware.LegacyBIOS = ptr.Of(false)
	}

	y.Firmware.Images = append(append(o.Firmware.Images, y.Firmware.Images...), d.Firmware.Images...)
	for i := range y.Firmware.Images {
		f := &y.Firmware.Images[i]
		if f.Arch == "" {
			f.Arch = *y.Arch
		}
	}

	if y.TimeZone == nil {
		y.TimeZone = d.TimeZone
	}
	if o.TimeZone != nil {
		y.TimeZone = o.TimeZone
	}
	if y.TimeZone == nil {
		y.TimeZone = ptr.Of(hostTimeZone())
	}

	if y.SSH.LocalPort == nil {
		y.SSH.LocalPort = d.SSH.LocalPort
	}
	if o.SSH.LocalPort != nil {
		y.SSH.LocalPort = o.SSH.LocalPort
	}
	if y.SSH.LocalPort == nil {
		// y.SSH.LocalPort value is not filled here (filled by the hostagent)
		y.SSH.LocalPort = ptr.Of(0)
	}
	if y.SSH.LoadDotSSHPubKeys == nil {
		y.SSH.LoadDotSSHPubKeys = d.SSH.LoadDotSSHPubKeys
	}
	if o.SSH.LoadDotSSHPubKeys != nil {
		y.SSH.LoadDotSSHPubKeys = o.SSH.LoadDotSSHPubKeys
	}
	if y.SSH.LoadDotSSHPubKeys == nil {
		y.SSH.LoadDotSSHPubKeys = ptr.Of(false) // was true before Lima v1.0
	}

	if y.SSH.ForwardAgent == nil {
		y.SSH.ForwardAgent = d.SSH.ForwardAgent
	}
	if o.SSH.ForwardAgent != nil {
		y.SSH.ForwardAgent = o.SSH.ForwardAgent
	}
	if y.SSH.ForwardAgent == nil {
		y.SSH.ForwardAgent = ptr.Of(false)
	}

	if y.SSH.ForwardX11 == nil {
		y.SSH.ForwardX11 = d.SSH.ForwardX11
	}
	if o.SSH.ForwardX11 != nil {
		y.SSH.ForwardX11 = o.SSH.ForwardX11
	}
	if y.SSH.ForwardX11 == nil {
		y.SSH.ForwardX11 = ptr.Of(false)
	}

	if y.SSH.ForwardX11Trusted == nil {
		y.SSH.ForwardX11Trusted = d.SSH.ForwardX11Trusted
	}
	if o.SSH.ForwardX11Trusted != nil {
		y.SSH.ForwardX11Trusted = o.SSH.ForwardX11Trusted
	}
	if y.SSH.ForwardX11Trusted == nil {
		y.SSH.ForwardX11Trusted = ptr.Of(false)
	}

	hosts := make(map[string]string)
	// Values can be either names or IP addresses. Name values are canonicalized in the hostResolver.
	for k, v := range d.HostResolver.Hosts {
		hosts[k] = v
	}
	for k, v := range y.HostResolver.Hosts {
		hosts[k] = v
	}
	for k, v := range o.HostResolver.Hosts {
		hosts[k] = v
	}
	y.HostResolver.Hosts = hosts

	y.Provision = append(append(o.Provision, y.Provision...), d.Provision...)
	for i := range y.Provision {
		provision := &y.Provision[i]
		if provision.Mode == "" {
			provision.Mode = ProvisionModeSystem
		}
		if provision.Mode == ProvisionModeDependency && provision.SkipDefaultDependencyResolution == nil {
			provision.SkipDefaultDependencyResolution = ptr.Of(false)
		}
		if out, err := executeGuestTemplate(provision.Script, instDir, y.User, y.Param); err == nil {
			provision.Script = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process provisioning script %q as a template", provision.Script)
		}
	}

	if y.GuestInstallPrefix == nil {
		y.GuestInstallPrefix = d.GuestInstallPrefix
	}
	if o.GuestInstallPrefix != nil {
		y.GuestInstallPrefix = o.GuestInstallPrefix
	}
	if y.GuestInstallPrefix == nil {
		y.GuestInstallPrefix = ptr.Of(defaultGuestInstallPrefix())
	}

	if y.UpgradePackages == nil {
		y.UpgradePackages = d.UpgradePackages
	}
	if o.UpgradePackages != nil {
		y.UpgradePackages = o.UpgradePackages
	}
	if y.UpgradePackages == nil {
		y.UpgradePackages = ptr.Of(false)
	}

	if y.Containerd.System == nil {
		y.Containerd.System = d.Containerd.System
	}
	if o.Containerd.System != nil {
		y.Containerd.System = o.Containerd.System
	}
	if y.Containerd.System == nil {
		y.Containerd.System = ptr.Of(false)
	}
	if y.Containerd.User == nil {
		y.Containerd.User = d.Containerd.User
	}
	if o.Containerd.User != nil {
		y.Containerd.User = o.Containerd.User
	}
	if y.Containerd.User == nil {
		switch *y.Arch {
		case X8664, AARCH64:
			y.Containerd.User = ptr.Of(true)
		default:
			y.Containerd.User = ptr.Of(false)
		}
	}

	y.Containerd.Archives = append(append(o.Containerd.Archives, y.Containerd.Archives...), d.Containerd.Archives...)
	if len(y.Containerd.Archives) == 0 {
		y.Containerd.Archives = defaultContainerdArchives()
	}
	for i := range y.Containerd.Archives {
		f := &y.Containerd.Archives[i]
		if f.Arch == "" {
			f.Arch = *y.Arch
		}
	}

	y.Probes = append(append(o.Probes, y.Probes...), d.Probes...)
	for i := range y.Probes {
		probe := &y.Probes[i]
		if probe.Mode == "" {
			probe.Mode = ProbeModeReadiness
		}
		if probe.Description == "" {
			probe.Description = fmt.Sprintf("user probe %d/%d", i+1, len(y.Probes))
		}
		if out, err := executeGuestTemplate(probe.Script, instDir, y.User, y.Param); err == nil {
			probe.Script = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process probing script %q as a template", probe.Script)
		}
	}

	y.PortForwards = append(append(o.PortForwards, y.PortForwards...), d.PortForwards...)
	for i := range y.PortForwards {
		FillPortForwardDefaults(&y.PortForwards[i], instDir, y.User, y.Param)
		// After defaults processing the singular HostPort and GuestPort values should not be used again.
	}

	y.CopyToHost = append(append(o.CopyToHost, y.CopyToHost...), d.CopyToHost...)
	for i := range y.CopyToHost {
		FillCopyToHostDefaults(&y.CopyToHost[i], instDir, y.User, y.Param)
	}

	if y.HostResolver.Enabled == nil {
		y.HostResolver.Enabled = d.HostResolver.Enabled
	}
	if o.HostResolver.Enabled != nil {
		y.HostResolver.Enabled = o.HostResolver.Enabled
	}
	if y.HostResolver.Enabled == nil {
		y.HostResolver.Enabled = ptr.Of(true)
	}

	if y.HostResolver.IPv6 == nil {
		y.HostResolver.IPv6 = d.HostResolver.IPv6
	}
	if o.HostResolver.IPv6 != nil {
		y.HostResolver.IPv6 = o.HostResolver.IPv6
	}
	if y.HostResolver.IPv6 == nil {
		y.HostResolver.IPv6 = ptr.Of(false)
	}

	if y.PropagateProxyEnv == nil {
		y.PropagateProxyEnv = d.PropagateProxyEnv
	}
	if o.PropagateProxyEnv != nil {
		y.PropagateProxyEnv = o.PropagateProxyEnv
	}
	if y.PropagateProxyEnv == nil {
		y.PropagateProxyEnv = ptr.Of(true)
	}

	networks := make([]Network, 0, len(d.Networks)+len(y.Networks)+len(o.Networks))
	iface := make(map[string]int)
	for _, nw := range append(append(d.Networks, y.Networks...), o.Networks...) {
		if i, ok := iface[nw.Interface]; ok {
			if nw.Socket != "" {
				networks[i].Socket = nw.Socket
				networks[i].Lima = ""
			}
			if nw.Lima != "" {
				if nw.Socket != "" {
					// We can't return an error, so just log it, and prefer `lima` over `socket`
					logrus.Errorf("Network %q has both socket=%q and lima=%q fields; ignoring socket",
						nw.Interface, nw.Socket, nw.Lima)
				}
				networks[i].Lima = nw.Lima
				networks[i].Socket = ""
			}
			if nw.MACAddress != "" {
				networks[i].MACAddress = nw.MACAddress
			}
			if nw.Metric != nil {
				networks[i].Metric = nw.Metric
			}
		} else {
			// unnamed network definitions are not combined/overwritten
			if nw.Interface != "" {
				iface[nw.Interface] = len(networks)
			}
			networks = append(networks, nw)
		}
	}
	y.Networks = networks
	for i := range y.Networks {
		nw := &y.Networks[i]
		if nw.MACAddress == "" {
			// every interface in every limayaml file must get its own unique MAC address
			nw.MACAddress = MACAddress(fmt.Sprintf("%s#%d", filePath, i))
		}
		if nw.Interface == "" {
			nw.Interface = "lima" + strconv.Itoa(i)
		}
		if nw.Metric == nil {
			nw.Metric = ptr.Of(uint32(100))
		}
	}

	y.MountTypesUnsupported = append(append(o.MountTypesUnsupported, y.MountTypesUnsupported...), d.MountTypesUnsupported...)
	mountTypesUnsupported := make(map[string]struct{})
	for _, f := range y.MountTypesUnsupported {
		mountTypesUnsupported[f] = struct{}{}
	}
	// MountType has to be resolved before resolving Mounts
	if y.MountType == nil {
		y.MountType = d.MountType
	}
	if o.MountType != nil {
		y.MountType = o.MountType
	}
	if y.MountType == nil || *y.MountType == "" || *y.MountType == "default" {
		switch *y.VMType {
		case VZ:
			y.MountType = ptr.Of(VIRTIOFS)
		case QEMU:
			y.MountType = ptr.Of(NINEP)
			if _, ok := mountTypesUnsupported[NINEP]; ok {
				// Use REVSSHFS if the instance does not support 9p
				y.MountType = ptr.Of(REVSSHFS)
			} else if isExistingInstanceDir(instDir) && !versionutil.GreaterEqual(existingLimaVersion, "1.0.0") {
				// Use REVSSHFS if the instance was created with Lima prior to v1.0
				y.MountType = ptr.Of(REVSSHFS)
			}
		default:
			y.MountType = ptr.Of(REVSSHFS)
		}
	}

	if _, ok := mountTypesUnsupported[*y.MountType]; ok {
		// We cannot return an error here, but Validate() will return it.
		logrus.Warnf("Unsupported mount type: %q", *y.MountType)
	}

	if y.MountInotify == nil {
		y.MountInotify = d.MountInotify
	}
	if o.MountInotify != nil {
		y.MountInotify = o.MountInotify
	}
	if y.MountInotify == nil {
		y.MountInotify = ptr.Of(false)
	}

	// Combine all mounts; highest priority entry determines writable status.
	// Only works for exact matches; does not normalize case or resolve symlinks.
	mounts := make([]Mount, 0, len(d.Mounts)+len(y.Mounts)+len(o.Mounts))
	location := make(map[string]int)
	for _, mount := range append(append(d.Mounts, y.Mounts...), o.Mounts...) {
		if out, err := executeHostTemplate(mount.Location, instDir, y.Param); err == nil {
			mount.Location = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process mount location %q as a template", mount.Location)
		}
		if mount.MountPoint != nil {
			if out, err := executeGuestTemplate(*mount.MountPoint, instDir, y.User, y.Param); err == nil {
				mount.MountPoint = ptr.Of(out.String())
			} else {
				logrus.WithError(err).Warnf("Couldn't process mount point %q as a template", *mount.MountPoint)
			}
		}
		if i, ok := location[mount.Location]; ok {
			if mount.SSHFS.Cache != nil {
				mounts[i].SSHFS.Cache = mount.SSHFS.Cache
			}
			if mount.SSHFS.FollowSymlinks != nil {
				mounts[i].SSHFS.FollowSymlinks = mount.SSHFS.FollowSymlinks
			}
			if mount.SSHFS.SFTPDriver != nil {
				mounts[i].SSHFS.SFTPDriver = mount.SSHFS.SFTPDriver
			}
			if mount.NineP.SecurityModel != nil {
				mounts[i].NineP.SecurityModel = mount.NineP.SecurityModel
			}
			if mount.NineP.ProtocolVersion != nil {
				mounts[i].NineP.ProtocolVersion = mount.NineP.ProtocolVersion
			}
			if mount.NineP.Msize != nil {
				mounts[i].NineP.Msize = mount.NineP.Msize
			}
			if mount.NineP.Cache != nil {
				mounts[i].NineP.Cache = mount.NineP.Cache
			}
			if mount.Virtiofs.QueueSize != nil {
				mounts[i].Virtiofs.QueueSize = mount.Virtiofs.QueueSize
			}
			if mount.Writable != nil {
				mounts[i].Writable = mount.Writable
			}
			if mount.MountPoint != nil {
				mounts[i].MountPoint = mount.MountPoint
			}
		} else {
			location[mount.Location] = len(mounts)
			mounts = append(mounts, mount)
		}
	}
	y.Mounts = mounts

	for i := range y.Mounts {
		mount := &y.Mounts[i]
		if mount.SSHFS.Cache == nil {
			mount.SSHFS.Cache = ptr.Of(true)
		}
		if mount.SSHFS.FollowSymlinks == nil {
			mount.SSHFS.FollowSymlinks = ptr.Of(false)
		}
		if mount.SSHFS.SFTPDriver == nil {
			mount.SSHFS.SFTPDriver = ptr.Of("")
		}
		if mount.NineP.SecurityModel == nil {
			mounts[i].NineP.SecurityModel = ptr.Of(Default9pSecurityModel)
		}
		if mount.NineP.ProtocolVersion == nil {
			mounts[i].NineP.ProtocolVersion = ptr.Of(Default9pProtocolVersion)
		}
		if mount.NineP.Msize == nil {
			mounts[i].NineP.Msize = ptr.Of(Default9pMsize)
		}
		if mount.Virtiofs.QueueSize == nil && *y.VMType == QEMU && *y.MountType == VIRTIOFS {
			mounts[i].Virtiofs.QueueSize = ptr.Of(DefaultVirtiofsQueueSize)
		}
		if mount.Writable == nil {
			mount.Writable = ptr.Of(false)
		}
		if mount.NineP.Cache == nil {
			if *mount.Writable {
				mounts[i].NineP.Cache = ptr.Of(Default9pCacheForRW)
			} else {
				mounts[i].NineP.Cache = ptr.Of(Default9pCacheForRO)
			}
		}
		if mount.MountPoint == nil {
			mounts[i].MountPoint = ptr.Of(mount.Location)
		}
	}

	// Note: DNS lists are not combined; highest priority setting is picked
	if len(y.DNS) == 0 {
		y.DNS = d.DNS
	}
	if len(o.DNS) > 0 {
		y.DNS = o.DNS
	}

	env := make(map[string]string)
	for k, v := range d.Env {
		env[k] = v
	}
	for k, v := range y.Env {
		env[k] = v
	}
	for k, v := range o.Env {
		env[k] = v
	}
	y.Env = env

	param := make(map[string]string)
	for k, v := range d.Param {
		param[k] = v
	}
	for k, v := range y.Param {
		param[k] = v
	}
	for k, v := range o.Param {
		param[k] = v
	}
	y.Param = param

	if y.CACertificates.RemoveDefaults == nil {
		y.CACertificates.RemoveDefaults = d.CACertificates.RemoveDefaults
	}
	if o.CACertificates.RemoveDefaults != nil {
		y.CACertificates.RemoveDefaults = o.CACertificates.RemoveDefaults
	}
	if y.CACertificates.RemoveDefaults == nil {
		y.CACertificates.RemoveDefaults = ptr.Of(false)
	}

	caFiles := unique(append(append(d.CACertificates.Files, y.CACertificates.Files...), o.CACertificates.Files...))
	y.CACertificates.Files = caFiles

	caCerts := unique(append(append(d.CACertificates.Certs, y.CACertificates.Certs...), o.CACertificates.Certs...))
	y.CACertificates.Certs = caCerts

	if runtime.GOOS == "darwin" && IsNativeArch(AARCH64) {
		if y.Rosetta.Enabled == nil {
			y.Rosetta.Enabled = d.Rosetta.Enabled
		}
		if o.Rosetta.Enabled != nil {
			y.Rosetta.Enabled = o.Rosetta.Enabled
		}
		if y.Rosetta.Enabled == nil {
			y.Rosetta.Enabled = ptr.Of(false)
		}
	} else {
		y.Rosetta.Enabled = ptr.Of(false)
	}

	if y.Rosetta.BinFmt == nil {
		y.Rosetta.BinFmt = d.Rosetta.BinFmt
	}
	if o.Rosetta.BinFmt != nil {
		y.Rosetta.BinFmt = o.Rosetta.BinFmt
	}
	if y.Rosetta.BinFmt == nil {
		y.Rosetta.BinFmt = ptr.Of(false)
	}

	if y.NestedVirtualization == nil {
		y.NestedVirtualization = d.NestedVirtualization
	}
	if o.NestedVirtualization != nil {
		y.NestedVirtualization = o.NestedVirtualization
	}
	if y.NestedVirtualization == nil {
		y.NestedVirtualization = ptr.Of(false)
	}

	if y.Plain == nil {
		y.Plain = d.Plain
	}
	if o.Plain != nil {
		y.Plain = o.Plain
	}
	if y.Plain == nil {
		y.Plain = ptr.Of(false)
	}

	fixUpForPlainMode(y)
}

func fixUpForPlainMode(y *LimaYAML) {
	if !*y.Plain {
		return
	}
	y.Mounts = nil
	y.PortForwards = nil
	y.Containerd.System = ptr.Of(false)
	y.Containerd.User = ptr.Of(false)
	y.Rosetta.BinFmt = ptr.Of(false)
	y.Rosetta.Enabled = ptr.Of(false)
	y.TimeZone = ptr.Of("")
}

func executeGuestTemplate(format, instDir string, user User, param map[string]string) (bytes.Buffer, error) {
	tmpl, err := template.New("").Parse(format)
	if err == nil {
		name := filepath.Base(instDir)
		data := map[string]any{
			"Name":     name,
			"Hostname": identifierutil.HostnameFromInstName(name), // TODO: support customization
			"UID":      *user.UID,
			"User":     *user.Name,
			"Home":     *user.Home,
			"Param":    param,
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err == nil {
			return out, nil
		}
	}
	return bytes.Buffer{}, err
}

func executeHostTemplate(format, instDir string, param map[string]string) (bytes.Buffer, error) {
	tmpl, err := template.New("").Parse(format)
	if err == nil {
		limaHome, _ := dirnames.LimaDir()
		data := map[string]any{
			"Dir":  instDir,
			"Name": filepath.Base(instDir),
			// TODO: add hostname fields for the host and the guest
			"UID":   currentUser.Uid,
			"User":  currentUser.Username,
			"Home":  userHomeDir,
			"Param": param,

			"Instance": filepath.Base(instDir), // DEPRECATED, use `{{.Name}}`
			"LimaHome": limaHome,               // DEPRECATED, use `{{.Dir}}` instead of `{{.LimaHome}}/{{.Instance}}`
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, data); err == nil {
			return out, nil
		}
	}
	return bytes.Buffer{}, err
}

func FillPortForwardDefaults(rule *PortForward, instDir string, user User, param map[string]string) {
	if rule.Proto == "" {
		rule.Proto = ProtoTCP
	}
	if rule.GuestIP == nil {
		if rule.GuestIPMustBeZero {
			rule.GuestIP = net.IPv4zero
		} else {
			rule.GuestIP = IPv4loopback1
		}
	}
	if rule.HostIP == nil {
		rule.HostIP = IPv4loopback1
	}
	if rule.GuestPortRange[0] == 0 && rule.GuestPortRange[1] == 0 {
		if rule.GuestPort == 0 {
			rule.GuestPortRange[0] = 1
			rule.GuestPortRange[1] = 65535
		} else {
			rule.GuestPortRange[0] = rule.GuestPort
			rule.GuestPortRange[1] = rule.GuestPort
		}
	}
	if rule.HostPortRange[0] == 0 && rule.HostPortRange[1] == 0 {
		if rule.HostPort == 0 {
			rule.HostPortRange = rule.GuestPortRange
		} else {
			rule.HostPortRange[0] = rule.HostPort
			rule.HostPortRange[1] = rule.HostPort
		}
	}
	if rule.GuestSocket != "" {
		if out, err := executeGuestTemplate(rule.GuestSocket, instDir, user, param); err == nil {
			rule.GuestSocket = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process guestSocket %q as a template", rule.GuestSocket)
		}
	}
	if rule.HostSocket != "" {
		if out, err := executeHostTemplate(rule.HostSocket, instDir, param); err == nil {
			rule.HostSocket = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process hostSocket %q as a template", rule.HostSocket)
		}
		if !filepath.IsAbs(rule.HostSocket) {
			rule.HostSocket = filepath.Join(instDir, filenames.SocketDir, rule.HostSocket)
		}
	}
}

func FillCopyToHostDefaults(rule *CopyToHost, instDir string, user User, param map[string]string) {
	if rule.GuestFile != "" {
		if out, err := executeGuestTemplate(rule.GuestFile, instDir, user, param); err == nil {
			rule.GuestFile = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process guest %q as a template", rule.GuestFile)
		}
	}
	if rule.HostFile != "" {
		if out, err := executeHostTemplate(rule.HostFile, instDir, param); err == nil {
			rule.HostFile = out.String()
		} else {
			logrus.WithError(err).Warnf("Couldn't process host %q as a template", rule.HostFile)
		}
	}
}

func NewOS(osname string) OS {
	switch osname {
	case "linux":
		return LINUX
	default:
		logrus.Warnf("Unknown os: %s", osname)
		return osname
	}
}

func goarm() int {
	if runtime.GOOS != "linux" {
		return 0
	}
	if runtime.GOARCH != "arm" {
		return 0
	}
	if cpu.ARM.HasVFPv3 {
		return 7
	}
	if cpu.ARM.HasVFP {
		return 6
	}
	return 5 // default
}

func NewArch(arch string) Arch {
	switch arch {
	case "amd64":
		return X8664
	case "arm64":
		return AARCH64
	case "arm":
		arm := goarm()
		if arm == 7 {
			return ARMV7L
		}
		logrus.Warnf("Unknown arm: %d", arm)
		return arch
	case "riscv64":
		return RISCV64
	default:
		logrus.Warnf("Unknown arch: %s", arch)
		return arch
	}
}

func NewVMType(driver string) VMType {
	switch driver {
	case "vz":
		return VZ
	case "qemu":
		return QEMU
	case "wsl2":
		return WSL2
	default:
		logrus.Warnf("Unknown driver: %s", driver)
		return driver
	}
}

func isExistingInstanceDir(dir string) bool {
	// existence of "lima.yaml" does not signify existence of the instance,
	// because the file is created during the initialization of the instance.
	for _, f := range []string{
		filenames.HostAgentStdoutLog, filenames.HostAgentStderrLog,
		filenames.VzIdentifier, filenames.BaseDisk, filenames.DiffDisk, filenames.CIDataISO,
	} {
		file := filepath.Join(dir, f)
		if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

func ResolveVMType(y, d, o *LimaYAML, filePath string) VMType {
	// Check if the VMType is explicitly specified
	for i, f := range []*LimaYAML{o, y, d} {
		if f.VMType != nil && *f.VMType != "" && *f.VMType != "default" {
			logrus.Debugf("ResolveVMType: resolved VMType %q (explicitly specified in []*LimaYAML{o,y,d}[%d])", *f.VMType, i)
			return NewVMType(*f.VMType)
		}
	}

	// If this is an existing instance, guess the VMType from the contents of the instance directory.
	if dir, basename := filepath.Split(filePath); dir != "" && basename == filenames.LimaYAML && isExistingInstanceDir(dir) {
		if runtime.GOOS == "darwin" {
			vzIdentifier := filepath.Join(dir, filenames.VzIdentifier) // since Lima v0.14
			if _, err := os.Lstat(vzIdentifier); !errors.Is(err, os.ErrNotExist) {
				logrus.Debugf("ResolveVMType: resolved VMType %q (existing instance, with %q)", VZ, vzIdentifier)
				return VZ
			}
			logrus.Debugf("ResolveVMType: resolved VMType %q (existing instance, without %q)", QEMU, vzIdentifier)
			return QEMU
		}
		logrus.Debugf("ResolveVMType: resolved VMType %q (existing instance)", QEMU)
		return QEMU
	}

	// Resolve the best type, depending on GOOS
	switch runtime.GOOS {
	case "darwin":
		macOSProductVersion, err := osutil.ProductVersion()
		if err != nil {
			logrus.WithError(err).Warn("Failed to get macOS product version")
			logrus.Debugf("ResolveVMType: resolved VMType %q (default for unknown version of macOS)", QEMU)
			return QEMU
		}
		// Virtualization.framework in macOS prior to 13.5 could not boot Linux kernel v6.2 on Intel
		// https://github.com/lima-vm/lima/issues/1577
		if macOSProductVersion.LessThan(*semver.New("13.5.0")) {
			logrus.Debugf("ResolveVMType: resolved VMType %q (default for macOS prior to 13.5)", QEMU)
			return QEMU
		}
		// Use QEMU if the config depends on QEMU
		for i, f := range []*LimaYAML{o, y, d} {
			if f.Arch != nil && !IsNativeArch(*f.Arch) {
				logrus.Debugf("ResolveVMType: resolved VMType %q (non-native arch=%q is specified in []*LimaYAML{o,y,d}[%d])", QEMU, *f.Arch, i)
				return QEMU
			}
			if f.Firmware.LegacyBIOS != nil && *f.Firmware.LegacyBIOS {
				logrus.Debugf("ResolveVMType: resolved VMType %q (firmware.legacyBIOS is specified in []*LimaYAML{o,y,d}[%d])", QEMU, i)
				return QEMU
			}
			if f.MountType != nil && *f.MountType == NINEP {
				logrus.Debugf("ResolveVMType: resolved VMType %q (mountType=%q is specified in []*LimaYAML{o,y,d}[%d])", QEMU, NINEP, i)
				return QEMU
			}
			if f.Audio.Device != nil {
				switch *f.Audio.Device {
				case "", "none", "default", "vz":
					// NOP
				default:
					logrus.Debugf("ResolveVMType: resolved VMType %q (audio.device=%q is specified in []*LimaYAML{o,y,d}[%d])", QEMU, *f.Audio.Device, i)
					return QEMU
				}
			}
			if f.Video.Display != nil {
				switch *f.Video.Display {
				case "", "none", "default", "vz":
					// NOP
				default:
					logrus.Debugf("ResolveVMType: resolved VMType %q (video.display=%q is specified in []*LimaYAML{o,y,d}[%d])", QEMU, *f.Video.Display, i)
					return QEMU
				}
			}
		}
		// Use VZ if the config is compatible with VZ
		logrus.Debugf("ResolveVMType: resolved VMType %q (default for macOS 13.5 and later)", VZ)
		return VZ
	default:
		logrus.Debugf("ResolveVMType: resolved VMType %q (default for GOOS=%q)", QEMU, runtime.GOOS)
		return QEMU
	}
}

func ResolveOS(s *string) OS {
	if s == nil || *s == "" || *s == "default" {
		return NewOS("linux")
	}
	return *s
}

func ResolveArch(s *string) Arch {
	if s == nil || *s == "" || *s == "default" {
		return NewArch(runtime.GOARCH)
	}
	return *s
}

func IsAccelOS() bool {
	switch runtime.GOOS {
	case "darwin", "linux", "netbsd", "windows":
		// Accelerator
		return true
	}
	// Using TCG
	return false
}

var hasSMEDarwin = sync.OnceValue(func() bool {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return false
	}
	// golang.org/x/sys/cpu does not support inspecting the availability of SME yet
	s, err := osutil.Sysctl("hw.optional.arm.FEAT_SME")
	if err != nil {
		logrus.WithError(err).Debug("failed to check hw.optional.arm.FEAT_SME")
	}
	return s == "1"
})

func HasHostCPU() bool {
	switch runtime.GOOS {
	case "darwin":
		if hasSMEDarwin() {
			macOSProductVersion, err := osutil.ProductVersion()
			if err != nil || macOSProductVersion.Equal(*semver.New("15.2")) {
				// SME is available since Apple M4 running macOS 15.2, but it was broken on macOS 15.2.
				// It has been fixed in 15.3.
				//
				// https://github.com/lima-vm/lima/issues/3032
				// https://gitlab.com/qemu-project/qemu/-/issues/2665
				// https://gitlab.com/qemu-project/qemu/-/issues/2721
				return false
			}
		}
		return true
	case "linux":
		return true
	case "netbsd", "windows":
		return false
	}
	// Not reached
	return false
}

func HasMaxCPU() bool {
	// windows: WHPX: Unexpected VP exit code 4
	return HasHostCPU()
}

func IsNativeArch(arch Arch) bool {
	nativeX8664 := arch == X8664 && runtime.GOARCH == "amd64"
	nativeAARCH64 := arch == AARCH64 && runtime.GOARCH == "arm64"
	nativeARMV7L := arch == ARMV7L && runtime.GOARCH == "arm" && goarm() == 7
	nativeRISCV64 := arch == RISCV64 && runtime.GOARCH == "riscv64"
	return nativeX8664 || nativeAARCH64 || nativeARMV7L || nativeRISCV64
}

func unique(s []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range s {
		if _, found := keys[entry]; !found {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
