package limayaml

import (
	"net"

	"github.com/opencontainers/go-digest"
)

type LimaYAML struct {
	Base                  BaseTemplates `yaml:"base,omitempty" json:"base,omitempty" jsonschema:"nullable"`
	MinimumLimaVersion    *string       `yaml:"minimumLimaVersion,omitempty" json:"minimumLimaVersion,omitempty" jsonschema:"nullable"`
	VMType                *VMType       `yaml:"vmType,omitempty" json:"vmType,omitempty" jsonschema:"nullable"`
	VMOpts                VMOpts        `yaml:"vmOpts,omitempty" json:"vmOpts,omitempty"`
	OS                    *OS           `yaml:"os,omitempty" json:"os,omitempty" jsonschema:"nullable"`
	Arch                  *Arch         `yaml:"arch,omitempty" json:"arch,omitempty" jsonschema:"nullable"`
	Images                []Image       `yaml:"images" json:"images"` // REQUIRED
	CPUType               CPUType       `yaml:"cpuType,omitempty" json:"cpuType,omitempty" jsonschema:"nullable"`
	CPUs                  *int          `yaml:"cpus,omitempty" json:"cpus,omitempty" jsonschema:"nullable"`
	Memory                *string       `yaml:"memory,omitempty" json:"memory,omitempty" jsonschema:"nullable"` // go-units.RAMInBytes
	Disk                  *string       `yaml:"disk,omitempty" json:"disk,omitempty" jsonschema:"nullable"`     // go-units.RAMInBytes
	AdditionalDisks       []Disk        `yaml:"additionalDisks,omitempty" json:"additionalDisks,omitempty" jsonschema:"nullable"`
	Mounts                []Mount       `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	MountTypesUnsupported []string      `yaml:"mountTypesUnsupported,omitempty" json:"mountTypesUnsupported,omitempty" jsonschema:"nullable"`
	MountType             *MountType    `yaml:"mountType,omitempty" json:"mountType,omitempty" jsonschema:"nullable"`
	MountInotify          *bool         `yaml:"mountInotify,omitempty" json:"mountInotify,omitempty" jsonschema:"nullable"`
	SSH                   SSH           `yaml:"ssh,omitempty" json:"ssh,omitempty"` // REQUIRED (FIXME)
	Firmware              Firmware      `yaml:"firmware,omitempty" json:"firmware,omitempty"`
	Audio                 Audio         `yaml:"audio,omitempty" json:"audio,omitempty"`
	Video                 Video         `yaml:"video,omitempty" json:"video,omitempty"`
	Provision             []Provision   `yaml:"provision,omitempty" json:"provision,omitempty"`
	UpgradePackages       *bool         `yaml:"upgradePackages,omitempty" json:"upgradePackages,omitempty" jsonschema:"nullable"`
	Containerd            Containerd    `yaml:"containerd,omitempty" json:"containerd,omitempty"`
	GuestInstallPrefix    *string       `yaml:"guestInstallPrefix,omitempty" json:"guestInstallPrefix,omitempty" jsonschema:"nullable"`
	Probes                []Probe       `yaml:"probes,omitempty" json:"probes,omitempty"`
	PortForwards          []PortForward `yaml:"portForwards,omitempty" json:"portForwards,omitempty"`
	CopyToHost            []CopyToHost  `yaml:"copyToHost,omitempty" json:"copyToHost,omitempty"`
	Message               string        `yaml:"message,omitempty" json:"message,omitempty"`
	Networks              []Network     `yaml:"networks,omitempty" json:"networks,omitempty" jsonschema:"nullable"`
	// `network` was deprecated in Lima v0.7.0, removed in Lima v0.14.0. Use `networks` instead.
	Env          map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Param        map[string]string `yaml:"param,omitempty" json:"param,omitempty"`
	DNS          []net.IP          `yaml:"dns,omitempty" json:"dns,omitempty"`
	HostResolver HostResolver      `yaml:"hostResolver,omitempty" json:"hostResolver,omitempty"`
	// `useHostResolver` was deprecated in Lima v0.8.1, removed in Lima v0.14.0. Use `hostResolver.enabled` instead.
	PropagateProxyEnv    *bool          `yaml:"propagateProxyEnv,omitempty" json:"propagateProxyEnv,omitempty" jsonschema:"nullable"`
	CACertificates       CACertificates `yaml:"caCerts,omitempty" json:"caCerts,omitempty"`
	Rosetta              Rosetta        `yaml:"rosetta,omitempty" json:"rosetta,omitempty"`
	Plain                *bool          `yaml:"plain,omitempty" json:"plain,omitempty" jsonschema:"nullable"`
	TimeZone             *string        `yaml:"timezone,omitempty" json:"timezone,omitempty" jsonschema:"nullable"`
	NestedVirtualization *bool          `yaml:"nestedVirtualization,omitempty" json:"nestedVirtualization,omitempty" jsonschema:"nullable"`
	User                 User           `yaml:"user,omitempty" json:"user,omitempty"`
}

type BaseTemplates []LocatorWithDigest

type LocatorWithDigest struct {
	URL    string  `yaml:"url" json:"url"`
	Digest *string `yaml:"digest,omitempty" json:"digest,omitempty" jsonschema:"nullable"` // TODO currently unused
}

type (
	OS        = string
	Arch      = string
	MountType = string
	VMType    = string
)

type CPUType = map[Arch]string

const (
	LINUX OS = "Linux"

	X8664   Arch = "x86_64"
	AARCH64 Arch = "aarch64"
	ARMV7L  Arch = "armv7l"
	RISCV64 Arch = "riscv64"

	REVSSHFS MountType = "reverse-sshfs"
	NINEP    MountType = "9p"
	VIRTIOFS MountType = "virtiofs"
	WSLMount MountType = "wsl2"

	QEMU VMType = "qemu"
	VZ   VMType = "vz"
	WSL2 VMType = "wsl2"
)

var (
	OSTypes    = []OS{LINUX}
	ArchTypes  = []Arch{X8664, AARCH64, ARMV7L, RISCV64}
	MountTypes = []MountType{REVSSHFS, NINEP, VIRTIOFS, WSLMount}
	VMTypes    = []VMType{QEMU, VZ, WSL2}
)

type User struct {
	Name    *string `yaml:"name,omitempty" json:"name,omitempty" jsonschema:"nullable"`
	Comment *string `yaml:"comment,omitempty" json:"comment,omitempty" jsonschema:"nullable"`
	Home    *string `yaml:"home,omitempty" json:"home,omitempty" jsonschema:"nullable"`
	Shell   *string `yaml:"shell,omitempty" json:"shell,omitempty" jsonschema:"nullable"`
	UID     *uint32 `yaml:"uid,omitempty" json:"uid,omitempty" jsonschema:"nullable"`
}

type VMOpts struct {
	QEMU QEMUOpts `yaml:"qemu,omitempty" json:"qemu,omitempty"`
}

type QEMUOpts struct {
	MinimumVersion *string `yaml:"minimumVersion,omitempty" json:"minimumVersion,omitempty" jsonschema:"nullable"`
}

type Rosetta struct {
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema:"nullable"`
	BinFmt  *bool `yaml:"binfmt,omitempty" json:"binfmt,omitempty" jsonschema:"nullable"`
}

type File struct {
	Location string        `yaml:"location" json:"location"` // REQUIRED
	Arch     Arch          `yaml:"arch,omitempty" json:"arch,omitempty"`
	Digest   digest.Digest `yaml:"digest,omitempty" json:"digest,omitempty"`
}

type FileWithVMType struct {
	File   `yaml:",inline"`
	VMType VMType `yaml:"vmType,omitempty" json:"vmType,omitempty"`
}

type Kernel struct {
	File    `yaml:",inline"`
	Cmdline string `yaml:"cmdline,omitempty" json:"cmdline,omitempty"`
}

type Image struct {
	File   `yaml:",inline"`
	Kernel *Kernel `yaml:"kernel,omitempty" json:"kernel,omitempty"`
	Initrd *File   `yaml:"initrd,omitempty" json:"initrd,omitempty"`
}

type Disk struct {
	Name   string   `yaml:"name" json:"name"` // REQUIRED
	Format *bool    `yaml:"format,omitempty" json:"format,omitempty"`
	FSType *string  `yaml:"fsType,omitempty" json:"fsType,omitempty"`
	FSArgs []string `yaml:"fsArgs,omitempty" json:"fsArgs,omitempty"`
}

type Mount struct {
	Location   string   `yaml:"location" json:"location"` // REQUIRED
	MountPoint *string  `yaml:"mountPoint,omitempty" json:"mountPoint,omitempty" jsonschema:"nullable"`
	Writable   *bool    `yaml:"writable,omitempty" json:"writable,omitempty" jsonschema:"nullable"`
	SSHFS      SSHFS    `yaml:"sshfs,omitempty" json:"sshfs,omitempty"`
	NineP      NineP    `yaml:"9p,omitempty" json:"9p,omitempty"`
	Virtiofs   Virtiofs `yaml:"virtiofs,omitempty" json:"virtiofs,omitempty"`
}

type SFTPDriver = string

const (
	SFTPDriverBuiltin           = "builtin"
	SFTPDriverOpenSSHSFTPServer = "openssh-sftp-server"
)

type SSHFS struct {
	Cache          *bool       `yaml:"cache,omitempty" json:"cache,omitempty" jsonschema:"nullable"`
	FollowSymlinks *bool       `yaml:"followSymlinks,omitempty" json:"followSymlinks,omitempty" jsonschema:"nullable"`
	SFTPDriver     *SFTPDriver `yaml:"sftpDriver,omitempty" json:"sftpDriver,omitempty" jsonschema:"nullable"`
}

type NineP struct {
	SecurityModel   *string `yaml:"securityModel,omitempty" json:"securityModel,omitempty" jsonschema:"nullable"`
	ProtocolVersion *string `yaml:"protocolVersion,omitempty" json:"protocolVersion,omitempty" jsonschema:"nullable"`
	Msize           *string `yaml:"msize,omitempty" json:"msize,omitempty" jsonschema:"nullable"`
	Cache           *string `yaml:"cache,omitempty" json:"cache,omitempty" jsonschema:"nullable"`
}

type Virtiofs struct {
	QueueSize *int `yaml:"queueSize,omitempty" json:"queueSize,omitempty"`
}

type SSH struct {
	LocalPort *int `yaml:"localPort,omitempty" json:"localPort,omitempty" jsonschema:"nullable"`

	// LoadDotSSHPubKeys loads ~/.ssh/*.pub in addition to $LIMA_HOME/_config/user.pub .
	LoadDotSSHPubKeys *bool `yaml:"loadDotSSHPubKeys,omitempty" json:"loadDotSSHPubKeys,omitempty" jsonschema:"nullable"` // default: false
	ForwardAgent      *bool `yaml:"forwardAgent,omitempty" json:"forwardAgent,omitempty" jsonschema:"nullable"`           // default: false
	ForwardX11        *bool `yaml:"forwardX11,omitempty" json:"forwardX11,omitempty" jsonschema:"nullable"`               // default: false
	ForwardX11Trusted *bool `yaml:"forwardX11Trusted,omitempty" json:"forwardX11Trusted,omitempty" jsonschema:"nullable"` // default: false
}

type Firmware struct {
	// LegacyBIOS disables UEFI if set.
	// LegacyBIOS is ignored for aarch64.
	LegacyBIOS *bool `yaml:"legacyBIOS,omitempty" json:"legacyBIOS,omitempty" jsonschema:"nullable"`

	// Images specify UEFI images (edk2-aarch64-code.fd.gz).
	// Defaults to built-in UEFI.
	Images []FileWithVMType `yaml:"images,omitempty" json:"images,omitempty"`
}

type Audio struct {
	// Device is a QEMU audiodev string
	Device *string `yaml:"device,omitempty" json:"device,omitempty" jsonschema:"nullable"`
}

type VNCOptions struct {
	Display *string `yaml:"display,omitempty" json:"display,omitempty" jsonschema:"nullable"`
}

type Video struct {
	// Display is a QEMU display string
	Display *string    `yaml:"display,omitempty" json:"display,omitempty" jsonschema:"nullable"`
	VNC     VNCOptions `yaml:"vnc,omitempty" json:"vnc,omitempty"`
}

type ProvisionMode = string

const (
	ProvisionModeSystem     ProvisionMode = "system"
	ProvisionModeUser       ProvisionMode = "user"
	ProvisionModeBoot       ProvisionMode = "boot"
	ProvisionModeDependency ProvisionMode = "dependency"
	ProvisionModeAnsible    ProvisionMode = "ansible"
)

type Provision struct {
	Mode                            ProvisionMode      `yaml:"mode,omitempty" json:"mode,omitempty" jsonschema:"default=system"`
	SkipDefaultDependencyResolution *bool              `yaml:"skipDefaultDependencyResolution,omitempty" json:"skipDefaultDependencyResolution,omitempty"`
	Script                          string             `yaml:"script" json:"script"`
	File                            *LocatorWithDigest `yaml:"file,omitempty" json:"file,omitempty" jsonschema:"nullable"`
	Playbook                        string             `yaml:"playbook,omitempty" json:"playbook,omitempty"`
}

type Containerd struct {
	System   *bool  `yaml:"system,omitempty" json:"system,omitempty" jsonschema:"nullable"` // default: false
	User     *bool  `yaml:"user,omitempty" json:"user,omitempty" jsonschema:"nullable"`     // default: true
	Archives []File `yaml:"archives,omitempty" json:"archives,omitempty"`                   // default: see defaultContainerdArchives
}

type ProbeMode = string

const (
	ProbeModeReadiness ProbeMode = "readiness"
)

type Probe struct {
	Mode        ProbeMode          `yaml:"mode,omitempty" json:"mode,omitempty" jsonschema:"default=readiness"`
	Description string             `yaml:"description,omitempty" json:"description,omitempty"`
	Script      string             `yaml:"script,omitempty" json:"script,omitempty"`
	File        *LocatorWithDigest `yaml:"file,omitempty" json:"file,omitempty" jsonschema:"nullable"`
	Hint        string             `yaml:"hint,omitempty" json:"hint,omitempty"`
}

type Proto = string

const (
	ProtoTCP Proto = "tcp"
	ProtoUDP Proto = "udp"
	ProtoAny Proto = "any"
)

type PortForward struct {
	GuestIPMustBeZero bool   `yaml:"guestIPMustBeZero,omitempty" json:"guestIPMustBeZero,omitempty"`
	GuestIP           net.IP `yaml:"guestIP,omitempty" json:"guestIP,omitempty"`
	GuestPort         int    `yaml:"guestPort,omitempty" json:"guestPort,omitempty"`
	GuestPortRange    [2]int `yaml:"guestPortRange,omitempty" json:"guestPortRange,omitempty"`
	GuestSocket       string `yaml:"guestSocket,omitempty" json:"guestSocket,omitempty"`
	HostIP            net.IP `yaml:"hostIP,omitempty" json:"hostIP,omitempty"`
	HostPort          int    `yaml:"hostPort,omitempty" json:"hostPort,omitempty"`
	HostPortRange     [2]int `yaml:"hostPortRange,omitempty" json:"hostPortRange,omitempty"`
	HostSocket        string `yaml:"hostSocket,omitempty" json:"hostSocket,omitempty"`
	Proto             Proto  `yaml:"proto,omitempty" json:"proto,omitempty"`
	Reverse           bool   `yaml:"reverse,omitempty" json:"reverse,omitempty"`
	Ignore            bool   `yaml:"ignore,omitempty" json:"ignore,omitempty"`
}

type CopyToHost struct {
	GuestFile    string `yaml:"guest,omitempty" json:"guest,omitempty"`
	HostFile     string `yaml:"host,omitempty" json:"host,omitempty"`
	DeleteOnStop bool   `yaml:"deleteOnStop,omitempty" json:"deleteOnStop,omitempty"`
}

type Network struct {
	// `Lima` and `Socket` are mutually exclusive; exactly one is required
	Lima string `yaml:"lima,omitempty" json:"lima,omitempty"`
	// Socket is a QEMU-compatible socket
	Socket string `yaml:"socket,omitempty" json:"socket,omitempty"`
	// VZNAT uses VZNATNetworkDeviceAttachment. Needs VZ. No root privilege is required.
	VZNAT *bool `yaml:"vzNAT,omitempty" json:"vzNAT,omitempty"`

	MACAddress string  `yaml:"macAddress,omitempty" json:"macAddress,omitempty"`
	Interface  string  `yaml:"interface,omitempty" json:"interface,omitempty"`
	Metric     *uint32 `yaml:"metric,omitempty" json:"metric,omitempty"`
}

type HostResolver struct {
	Enabled *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty" jsonschema:"nullable"`
	IPv6    *bool             `yaml:"ipv6,omitempty" json:"ipv6,omitempty" jsonschema:"nullable"`
	Hosts   map[string]string `yaml:"hosts,omitempty" json:"hosts,omitempty" jsonschema:"nullable"`
}

type CACertificates struct {
	RemoveDefaults *bool    `yaml:"removeDefaults,omitempty" json:"removeDefaults,omitempty" jsonschema:"nullable"` // default: false
	Files          []string `yaml:"files,omitempty" json:"files,omitempty" jsonschema:"nullable"`
	Certs          []string `yaml:"certs,omitempty" json:"certs,omitempty" jsonschema:"nullable"`
}
