package limayaml

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/lima-vm/lima/pkg/osutil"
	"github.com/lima-vm/lima/pkg/ptr"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/sirupsen/logrus"
	"gotest.tools/v3/assert"
)

func TestFillDefault(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	var d, y, o LimaYAML

	defaultVMType := ResolveVMType(&y, &d, &o, "")

	opts := []cmp.Option{
		// Consider nil slices and empty slices to be identical
		cmpopts.EquateEmpty(),
	}

	var arch Arch
	switch runtime.GOARCH {
	case "amd64":
		arch = X8664
	case "arm64":
		arch = AARCH64
	case "arm":
		if runtime.GOOS != "linux" {
			t.Skipf("unsupported GOOS: %s", runtime.GOOS)
		}
		if arm := goarm(); arm < 7 {
			t.Skipf("unsupported GOARM: %d", arm)
		}
		arch = ARMV7L
	case "riscv64":
		arch = RISCV64
	default:
		t.Skipf("unknown GOARCH: %s", runtime.GOARCH)
	}

	hostHome, err := os.UserHomeDir()
	assert.NilError(t, err)
	limaHome, err := dirnames.LimaDir()
	assert.NilError(t, err)
	user := osutil.LimaUser("0.0.0", false)
	if runtime.GOOS != "windows" {
		// manual template expansion for "/home/{{.User}}.linux" (done by FillDefault)
		user.HomeDir = fmt.Sprintf("/home/%s.linux", user.Username)
	}
	uid, err := strconv.ParseUint(user.Uid, 10, 32)
	assert.NilError(t, err)

	instName := "instance"
	instDir := filepath.Join(limaHome, instName)
	filePath := filepath.Join(instDir, filenames.LimaYAML)

	// Builtin default values
	builtin := LimaYAML{
		VMType:             &defaultVMType,
		OS:                 ptr.Of(LINUX),
		Arch:               ptr.Of(arch),
		CPUType:            defaultCPUType(),
		CPUs:               ptr.Of(defaultCPUs()),
		Memory:             ptr.Of(defaultMemoryAsString()),
		Disk:               ptr.Of(defaultDiskSizeAsString()),
		GuestInstallPrefix: ptr.Of(defaultGuestInstallPrefix()),
		UpgradePackages:    ptr.Of(false),
		Containerd: Containerd{
			System:   ptr.Of(false),
			User:     ptr.Of(true),
			Archives: defaultContainerdArchives(),
		},
		SSH: SSH{
			Address:           ptr.Of("127.0.0.1"),
			LocalPort:         ptr.Of(0),
			LoadDotSSHPubKeys: ptr.Of(false),
			ForwardAgent:      ptr.Of(false),
			ForwardX11:        ptr.Of(false),
			ForwardX11Trusted: ptr.Of(false),
		},
		TimeZone: ptr.Of(hostTimeZone()),
		Firmware: Firmware{
			LegacyBIOS: ptr.Of(false),
		},
		Audio: Audio{
			Device: ptr.Of(""),
		},
		Video: Video{
			Display: ptr.Of("none"),
			VNC: VNCOptions{
				Display: ptr.Of("127.0.0.1:0,to=9"),
			},
		},
		HostResolver: HostResolver{
			Enabled: ptr.Of(true),
			IPv6:    ptr.Of(false),
		},
		PropagateProxyEnv: ptr.Of(true),
		CACertificates: CACertificates{
			RemoveDefaults: ptr.Of(false),
		},
		NestedVirtualization: ptr.Of(false),
		Plain:                ptr.Of(false),
		User: User{
			Name:    ptr.Of(user.Username),
			Comment: ptr.Of(user.Name),
			Home:    ptr.Of(user.HomeDir),
			Shell:   ptr.Of("/bin/bash"),
			UID:     ptr.Of(uint32(uid)),
		},
	}

	defaultPortForward := PortForward{
		GuestIP:        IPv4loopback1,
		GuestPortRange: [2]int{1, 65535},
		HostIP:         IPv4loopback1,
		HostPortRange:  [2]int{1, 65535},
		Proto:          ProtoTCP,
		Reverse:        false,
	}

	// ------------------------------------------------------------------------------------
	// Builtin defaults are set when y is (mostly) empty

	// All these slices and maps are empty in "builtin". Add minimal entries here to see that
	// their values are retained and defaults for their fields are applied correctly.
	y = LimaYAML{
		HostResolver: HostResolver{
			Hosts: map[string]string{
				"MY.Host": "host.lima.internal",
			},
		},
		Mounts: []Mount{
			{Location: "/tmp"},
			{Location: "{{.Dir}}/{{.Param.ONE}}", MountPoint: ptr.Of("/mnt/{{.Param.ONE}}")},
		},
		MountType: ptr.Of(NINEP),
		Provision: []Provision{
			{Script: "#!/bin/true # {{.Param.ONE}}"},
		},
		Probes: []Probe{
			{Script: "#!/bin/false # {{.Param.ONE}}"},
		},
		Networks: []Network{
			{Lima: "shared"},
		},
		DNS: []net.IP{
			net.ParseIP("1.0.1.0"),
		},
		PortForwards: []PortForward{
			{},
			{GuestPort: 80},
			{GuestPort: 8080, HostPort: 8888},
			{
				GuestSocket: "{{.Home}} | {{.UID}} | {{.User}} | {{.Param.ONE}}",
				HostSocket:  "{{.Home}} | {{.Dir}} | {{.Name}} | {{.UID}} | {{.User}} | {{.Param.ONE}}",
			},
		},
		CopyToHost: []CopyToHost{
			{
				GuestFile: "{{.Home}} | {{.UID}} | {{.User}} | {{.Param.ONE}}",
				HostFile:  "{{.Home}} | {{.Dir}} | {{.Name}} | {{.UID}} | {{.User}} | {{.Param.ONE}}",
			},
		},
		Env: map[string]string{
			"ONE": "Eins",
		},
		Param: map[string]string{
			"ONE": "Eins",
		},
		CACertificates: CACertificates{
			Files: []string{"ca.crt"},
			Certs: []string{
				"-----BEGIN CERTIFICATE-----\nYOUR-ORGS-TRUSTED-CA-CERT\n-----END CERTIFICATE-----\n",
			},
		},
		TimeZone: ptr.Of("Antarctica/Troll"),
		Firmware: Firmware{
			LegacyBIOS: ptr.Of(false),
			Images: []FileWithVMType{
				{
					File: File{
						Location: "https://gitlab.com/kraxel/qemu/-/raw/704f7cad5105246822686f65765ab92045f71a3b/pc-bios/edk2-aarch64-code.fd.bz2",
						Arch:     AARCH64,
						Digest:   "sha256:a5fc228623891297f2d82e22ea56ec57cde93fea5ec01abf543e4ed5cacaf277",
					},
					VMType: QEMU,
				},
				{
					File: File{
						Location: "https://github.com/AkihiroSuda/qemu/raw/704f7cad5105246822686f65765ab92045f71a3b/pc-bios/edk2-aarch64-code.fd.bz2",
						Arch:     AARCH64,
						Digest:   "sha256:a5fc228623891297f2d82e22ea56ec57cde93fea5ec01abf543e4ed5cacaf277",
					},
					VMType: QEMU,
				},
			},
		},
	}

	expect := builtin
	expect.VMType = ptr.Of(QEMU) // due to NINEP
	expect.HostResolver.Hosts = map[string]string{
		"MY.Host": "host.lima.internal",
	}

	expect.Mounts = slices.Clone(y.Mounts)
	expect.Mounts[0].MountPoint = ptr.Of(expect.Mounts[0].Location)
	expect.Mounts[0].Writable = ptr.Of(false)
	expect.Mounts[0].SSHFS.Cache = ptr.Of(true)
	expect.Mounts[0].SSHFS.FollowSymlinks = ptr.Of(false)
	expect.Mounts[0].SSHFS.SFTPDriver = ptr.Of("")
	expect.Mounts[0].NineP.SecurityModel = ptr.Of(Default9pSecurityModel)
	expect.Mounts[0].NineP.ProtocolVersion = ptr.Of(Default9pProtocolVersion)
	expect.Mounts[0].NineP.Msize = ptr.Of(Default9pMsize)
	expect.Mounts[0].NineP.Cache = ptr.Of(Default9pCacheForRO)
	expect.Mounts[0].Virtiofs.QueueSize = nil
	// Only missing Mounts field is Writable, and the default value is also the null value: false
	expect.Mounts[1].Location = fmt.Sprintf("%s/%s", instDir, y.Param["ONE"])
	expect.Mounts[1].MountPoint = ptr.Of(fmt.Sprintf("/mnt/%s", y.Param["ONE"]))
	expect.Mounts[1].Writable = ptr.Of(false)
	expect.Mounts[1].SSHFS.Cache = ptr.Of(true)
	expect.Mounts[1].SSHFS.FollowSymlinks = ptr.Of(false)
	expect.Mounts[1].SSHFS.SFTPDriver = ptr.Of("")
	expect.Mounts[1].NineP.SecurityModel = ptr.Of(Default9pSecurityModel)
	expect.Mounts[1].NineP.ProtocolVersion = ptr.Of(Default9pProtocolVersion)
	expect.Mounts[1].NineP.Msize = ptr.Of(Default9pMsize)
	expect.Mounts[1].NineP.Cache = ptr.Of(Default9pCacheForRO)
	expect.Mounts[1].Virtiofs.QueueSize = nil

	expect.MountType = ptr.Of(NINEP)

	expect.MountInotify = ptr.Of(false)

	expect.Provision = slices.Clone(y.Provision)
	expect.Provision[0].Mode = ProvisionModeSystem
	expect.Provision[0].Script = "#!/bin/true # Eins"

	expect.Probes = slices.Clone(y.Probes)
	expect.Probes[0].Mode = ProbeModeReadiness
	expect.Probes[0].Description = "user probe 1/1"
	expect.Probes[0].Script = "#!/bin/false # Eins"

	expect.Networks = slices.Clone(y.Networks)
	expect.Networks[0].MACAddress = MACAddress(fmt.Sprintf("%s#%d", filePath, 0))
	expect.Networks[0].Interface = "lima0"
	expect.Networks[0].Metric = ptr.Of(uint32(100))

	expect.DNS = slices.Clone(y.DNS)
	expect.PortForwards = []PortForward{
		defaultPortForward,
		defaultPortForward,
		defaultPortForward,
		defaultPortForward,
	}
	expect.CopyToHost = []CopyToHost{
		{},
	}

	// Setting GuestPort and HostPort for DeepEqual(), but they are not supposed to be used
	// after FillDefault() has been called and the ...PortRange fields have been set.
	expect.PortForwards[1].GuestPort = 80
	expect.PortForwards[1].GuestPortRange = [2]int{80, 80}
	expect.PortForwards[1].HostPortRange = expect.PortForwards[1].GuestPortRange

	expect.PortForwards[2].GuestPort = 8080
	expect.PortForwards[2].GuestPortRange = [2]int{8080, 8080}
	expect.PortForwards[2].HostPort = 8888
	expect.PortForwards[2].HostPortRange = [2]int{8888, 8888}

	expect.PortForwards[3].GuestSocket = fmt.Sprintf("%s | %s | %s | %s", user.HomeDir, user.Uid, user.Username, y.Param["ONE"])
	expect.PortForwards[3].HostSocket = fmt.Sprintf("%s | %s | %s | %s | %s | %s", hostHome, instDir, instName, currentUser.Uid, currentUser.Username, y.Param["ONE"])

	expect.CopyToHost[0].GuestFile = fmt.Sprintf("%s | %s | %s | %s", user.HomeDir, user.Uid, user.Username, y.Param["ONE"])
	expect.CopyToHost[0].HostFile = fmt.Sprintf("%s | %s | %s | %s | %s | %s", hostHome, instDir, instName, currentUser.Uid, currentUser.Username, y.Param["ONE"])

	expect.Env = y.Env

	expect.Param = y.Param

	expect.CACertificates = CACertificates{
		RemoveDefaults: ptr.Of(false),
		Files:          []string{"ca.crt"},
		Certs: []string{
			"-----BEGIN CERTIFICATE-----\nYOUR-ORGS-TRUSTED-CA-CERT\n-----END CERTIFICATE-----\n",
		},
	}

	expect.TimeZone = y.TimeZone
	expect.Firmware = y.Firmware
	expect.Firmware.Images = slices.Clone(y.Firmware.Images)

	expect.Rosetta = Rosetta{
		Enabled: ptr.Of(false),
		BinFmt:  ptr.Of(false),
	}

	expect.NestedVirtualization = ptr.Of(false)

	FillDefault(&y, &LimaYAML{}, &LimaYAML{}, filePath, false)
	assert.DeepEqual(t, &y, &expect, opts...)

	filledDefaults := y

	// ------------------------------------------------------------------------------------
	// User-provided defaults should override any builtin defaults

	// Choose values that are different from the "builtin" defaults
	d = LimaYAML{
		VMType: ptr.Of("vz"),
		OS:     ptr.Of("unknown"),
		Arch:   ptr.Of("unknown"),
		CPUType: CPUType{
			AARCH64: "arm64",
			ARMV7L:  "armhf",
			X8664:   "amd64",
			RISCV64: "riscv64",
		},
		CPUs:   ptr.Of(7),
		Memory: ptr.Of("5GiB"),
		Disk:   ptr.Of("105GiB"),
		AdditionalDisks: []Disk{
			{Name: "data"},
		},
		GuestInstallPrefix: ptr.Of("/opt"),
		UpgradePackages:    ptr.Of(true),
		Containerd: Containerd{
			System: ptr.Of(true),
			User:   ptr.Of(false),
			Archives: []File{
				{Location: "/tmp/nerdctl.tgz"},
			},
		},
		SSH: SSH{
			Address:           ptr.Of("0.0.0.0"),
			LocalPort:         ptr.Of(888),
			LoadDotSSHPubKeys: ptr.Of(false),
			ForwardAgent:      ptr.Of(true),
			ForwardX11:        ptr.Of(false),
			ForwardX11Trusted: ptr.Of(false),
		},
		TimeZone: ptr.Of("Zulu"),
		Firmware: Firmware{
			LegacyBIOS: ptr.Of(true),
			Images: []FileWithVMType{
				{
					File: File{
						Location: "/dummy",
						Arch:     X8664,
					},
				},
			},
		},
		Audio: Audio{
			Device: ptr.Of("coreaudio"),
		},
		Video: Video{
			Display: ptr.Of("cocoa"),
			VNC: VNCOptions{
				Display: ptr.Of("none"),
			},
		},
		HostResolver: HostResolver{
			Enabled: ptr.Of(false),
			IPv6:    ptr.Of(true),
			Hosts: map[string]string{
				"default": "localhost",
			},
		},
		PropagateProxyEnv: ptr.Of(false),

		Mounts: []Mount{
			{
				Location: "/var/log",
				Writable: ptr.Of(false),
			},
		},
		Provision: []Provision{
			{
				Script: "#!/bin/true",
				Mode:   ProvisionModeUser,
			},
		},
		Probes: []Probe{
			{
				Script:      "#!/bin/false",
				Mode:        ProbeModeReadiness,
				Description: "User Probe",
			},
		},
		Networks: []Network{
			{
				MACAddress: "11:22:33:44:55:66",
				Interface:  "def0",
				Metric:     ptr.Of(uint32(50)),
			},
		},
		DNS: []net.IP{
			net.ParseIP("1.1.1.1"),
		},
		PortForwards: []PortForward{{
			GuestIP:        IPv4loopback1,
			GuestPort:      80,
			GuestPortRange: [2]int{80, 80},
			HostIP:         IPv4loopback1,
			HostPort:       80,
			HostPortRange:  [2]int{80, 80},
			Proto:          ProtoTCP,
		}},
		CopyToHost: []CopyToHost{{}},
		Env: map[string]string{
			"ONE": "one",
			"TWO": "two",
		},
		Param: map[string]string{
			"ONE": "one",
			"TWO": "two",
		},
		CACertificates: CACertificates{
			RemoveDefaults: ptr.Of(true),
			Certs: []string{
				"-----BEGIN CERTIFICATE-----\nYOUR-ORGS-TRUSTED-CA-CERT\n-----END CERTIFICATE-----\n",
			},
		},
		Rosetta: Rosetta{
			Enabled: ptr.Of(true),
			BinFmt:  ptr.Of(true),
		},
		NestedVirtualization: ptr.Of(true),
		User: User{
			Name:    ptr.Of("xxx"),
			Comment: ptr.Of("Foo Bar"),
			Home:    ptr.Of("/tmp"),
			Shell:   ptr.Of("/bin/tcsh"),
			UID:     ptr.Of(uint32(8080)),
		},
	}

	expect = d
	// Also verify that archive arch is filled in
	expect.Containerd.Archives = slices.Clone(d.Containerd.Archives)
	expect.Containerd.Archives[0].Arch = *d.Arch
	expect.Mounts = slices.Clone(d.Mounts)
	expect.Mounts[0].MountPoint = ptr.Of(expect.Mounts[0].Location)
	expect.Mounts[0].SSHFS.Cache = ptr.Of(true)
	expect.Mounts[0].SSHFS.FollowSymlinks = ptr.Of(false)
	expect.Mounts[0].SSHFS.SFTPDriver = ptr.Of("")
	expect.Mounts[0].NineP.SecurityModel = ptr.Of(Default9pSecurityModel)
	expect.Mounts[0].NineP.ProtocolVersion = ptr.Of(Default9pProtocolVersion)
	expect.Mounts[0].NineP.Msize = ptr.Of(Default9pMsize)
	expect.Mounts[0].NineP.Cache = ptr.Of(Default9pCacheForRO)
	expect.Mounts[0].Virtiofs.QueueSize = nil
	expect.HostResolver.Hosts = map[string]string{
		"default": d.HostResolver.Hosts["default"],
	}
	expect.MountType = ptr.Of(VIRTIOFS)
	expect.MountInotify = ptr.Of(false)
	expect.CACertificates.RemoveDefaults = ptr.Of(true)
	expect.CACertificates.Certs = []string{
		"-----BEGIN CERTIFICATE-----\nYOUR-ORGS-TRUSTED-CA-CERT\n-----END CERTIFICATE-----\n",
	}

	if runtime.GOOS == "darwin" && IsNativeArch(AARCH64) {
		expect.Rosetta = Rosetta{
			Enabled: ptr.Of(true),
			BinFmt:  ptr.Of(true),
		}
	} else {
		expect.Rosetta = Rosetta{
			Enabled: ptr.Of(false),
			BinFmt:  ptr.Of(true),
		}
	}
	expect.Plain = ptr.Of(false)

	y = LimaYAML{}
	FillDefault(&y, &d, &LimaYAML{}, filePath, false)
	assert.DeepEqual(t, &y, &expect, opts...)

	dExpect := expect

	// ------------------------------------------------------------------------------------
	// User-provided defaults should not override user-provided config values

	y = filledDefaults
	y.DNS = []net.IP{net.ParseIP("8.8.8.8")}
	y.AdditionalDisks = []Disk{{Name: "overridden"}}
	y.User.Home = ptr.Of("/root")

	expect = y

	expect.Provision = append(append([]Provision{}, y.Provision...), dExpect.Provision...)
	expect.Probes = append(append([]Probe{}, y.Probes...), dExpect.Probes...)
	expect.PortForwards = append(append([]PortForward{}, y.PortForwards...), dExpect.PortForwards...)
	expect.CopyToHost = append(append([]CopyToHost{}, y.CopyToHost...), dExpect.CopyToHost...)
	expect.Containerd.Archives = append(append([]File{}, y.Containerd.Archives...), dExpect.Containerd.Archives...)
	expect.Containerd.Archives[2].Arch = *expect.Arch
	expect.AdditionalDisks = append(append([]Disk{}, y.AdditionalDisks...), dExpect.AdditionalDisks...)
	expect.Firmware.Images = append(append([]FileWithVMType{}, y.Firmware.Images...), dExpect.Firmware.Images...)

	// Mounts and Networks start with lowest priority first, so higher priority entries can overwrite
	expect.Mounts = append(append([]Mount{}, dExpect.Mounts...), y.Mounts...)
	expect.Networks = append(append([]Network{}, dExpect.Networks...), y.Networks...)

	expect.HostResolver.Hosts["default"] = dExpect.HostResolver.Hosts["default"]

	// dExpect.DNS will be ignored, and not appended to y.DNS

	// "TWO" does not exist in filledDefaults.Env, so is set from dExpect.Env
	expect.Env["TWO"] = dExpect.Env["TWO"]

	expect.Param["TWO"] = dExpect.Param["TWO"]

	t.Logf("d.vmType=%q, y.vmType=%q, expect.vmType=%q", *d.VMType, *y.VMType, *expect.VMType)

	FillDefault(&y, &d, &LimaYAML{}, filePath, false)
	assert.DeepEqual(t, &y, &expect, opts...)

	// ------------------------------------------------------------------------------------
	// User-provided overrides should override user-provided config settings

	o = LimaYAML{
		VMType: ptr.Of("qemu"),
		OS:     ptr.Of(LINUX),
		Arch:   ptr.Of(arch),
		CPUType: CPUType{
			AARCH64: "uber-arm",
			ARMV7L:  "armv8",
			X8664:   "pentium",
			RISCV64: "sifive-u54",
		},
		CPUs:   ptr.Of(12),
		Memory: ptr.Of("7GiB"),
		Disk:   ptr.Of("117GiB"),
		AdditionalDisks: []Disk{
			{Name: "test"},
		},
		GuestInstallPrefix: ptr.Of("/usr"),
		UpgradePackages:    ptr.Of(true),
		Containerd: Containerd{
			System: ptr.Of(true),
			User:   ptr.Of(false),
			Archives: []File{
				{
					Arch:     arch,
					Location: "/tmp/nerdctl.tgz",
					Digest:   "$DIGEST",
				},
			},
		},
		SSH: SSH{
			Address:           ptr.Of("127.0.1.1"),
			LocalPort:         ptr.Of(4433),
			LoadDotSSHPubKeys: ptr.Of(true),
			ForwardAgent:      ptr.Of(true),
			ForwardX11:        ptr.Of(false),
			ForwardX11Trusted: ptr.Of(false),
		},
		TimeZone: ptr.Of("Universal"),
		Firmware: Firmware{
			LegacyBIOS: ptr.Of(true),
		},
		Audio: Audio{
			Device: ptr.Of("coreaudio"),
		},
		Video: Video{
			Display: ptr.Of("cocoa"),
			VNC: VNCOptions{
				Display: ptr.Of("none"),
			},
		},
		HostResolver: HostResolver{
			Enabled: ptr.Of(false),
			IPv6:    ptr.Of(false),
			Hosts: map[string]string{
				"override.": "underflow",
			},
		},
		PropagateProxyEnv: ptr.Of(false),

		Mounts: []Mount{
			{
				Location: "/var/log",
				Writable: ptr.Of(true),
				SSHFS: SSHFS{
					Cache:          ptr.Of(false),
					FollowSymlinks: ptr.Of(true),
				},
				NineP: NineP{
					SecurityModel:   ptr.Of("mapped-file"),
					ProtocolVersion: ptr.Of("9p2000"),
					Msize:           ptr.Of("8KiB"),
					Cache:           ptr.Of("none"),
				},
				Virtiofs: Virtiofs{
					QueueSize: ptr.Of(2048),
				},
			},
		},
		MountInotify: ptr.Of(true),
		Provision: []Provision{
			{
				Script: "#!/bin/true",
				Mode:   ProvisionModeSystem,
			},
		},
		Probes: []Probe{
			{
				Script:      "#!/bin/false",
				Mode:        ProbeModeReadiness,
				Description: "Another Probe",
			},
		},
		Networks: []Network{
			{
				Lima:       "shared",
				MACAddress: "10:20:30:40:50:60",
				Interface:  "def1",
				Metric:     ptr.Of(uint32(25)),
			},
			{
				Lima:      "bridged",
				Interface: "def0",
			},
		},
		DNS: []net.IP{
			net.ParseIP("2.2.2.2"),
		},
		PortForwards: []PortForward{{
			GuestIP:        IPv4loopback1,
			GuestPort:      88,
			GuestPortRange: [2]int{88, 88},
			HostIP:         IPv4loopback1,
			HostPort:       8080,
			HostPortRange:  [2]int{8080, 8080},
			Proto:          ProtoTCP,
		}},
		CopyToHost: []CopyToHost{{}},
		Env: map[string]string{
			"TWO":   "deux",
			"THREE": "trois",
		},
		Param: map[string]string{
			"TWO":   "deux",
			"THREE": "trois",
		},
		CACertificates: CACertificates{
			RemoveDefaults: ptr.Of(true),
		},
		Rosetta: Rosetta{
			Enabled: ptr.Of(false),
			BinFmt:  ptr.Of(false),
		},
		NestedVirtualization: ptr.Of(false),
		User: User{
			Name:    ptr.Of("foo"),
			Comment: ptr.Of("foo bar baz"),
			Home:    ptr.Of("/override"),
			Shell:   ptr.Of("/bin/sh"),
			UID:     ptr.Of(uint32(1122)),
		},
	}

	y = filledDefaults

	expect = o

	expect.Provision = append(append(o.Provision, y.Provision...), dExpect.Provision...)
	expect.Probes = append(append(o.Probes, y.Probes...), dExpect.Probes...)
	expect.PortForwards = append(append(o.PortForwards, y.PortForwards...), dExpect.PortForwards...)
	expect.CopyToHost = append(append(o.CopyToHost, y.CopyToHost...), dExpect.CopyToHost...)
	expect.Containerd.Archives = append(append(o.Containerd.Archives, y.Containerd.Archives...), dExpect.Containerd.Archives...)
	expect.Containerd.Archives[3].Arch = *expect.Arch
	expect.AdditionalDisks = append(append(o.AdditionalDisks, y.AdditionalDisks...), dExpect.AdditionalDisks...)
	expect.Firmware.Images = append(append(o.Firmware.Images, y.Firmware.Images...), dExpect.Firmware.Images...)

	expect.HostResolver.Hosts["default"] = dExpect.HostResolver.Hosts["default"]
	expect.HostResolver.Hosts["MY.Host"] = dExpect.HostResolver.Hosts["host.lima.internal"]

	// o.Mounts just makes dExpect.Mounts[0] writable because the Location matches
	expect.Mounts = append(append([]Mount{}, dExpect.Mounts...), y.Mounts...)
	expect.Mounts[0].Writable = ptr.Of(true)
	expect.Mounts[0].SSHFS.Cache = ptr.Of(false)
	expect.Mounts[0].SSHFS.FollowSymlinks = ptr.Of(true)
	expect.Mounts[0].NineP.SecurityModel = ptr.Of("mapped-file")
	expect.Mounts[0].NineP.ProtocolVersion = ptr.Of("9p2000")
	expect.Mounts[0].NineP.Msize = ptr.Of("8KiB")
	expect.Mounts[0].NineP.Cache = ptr.Of("none")
	expect.Mounts[0].Virtiofs.QueueSize = ptr.Of(2048)

	expect.MountType = ptr.Of(NINEP)
	expect.MountInotify = ptr.Of(true)

	// o.Networks[1] is overriding the dExpect.Networks[0].Lima entry for the "def0" interface
	expect.Networks = append(append(dExpect.Networks, y.Networks...), o.Networks[0])
	expect.Networks[0].Lima = o.Networks[1].Lima

	// Only highest prio DNS are retained
	expect.DNS = slices.Clone(o.DNS)

	// ONE remains from filledDefaults.Env; the rest are set from o
	expect.Env["ONE"] = y.Env["ONE"]

	expect.Param["ONE"] = y.Param["ONE"]

	expect.CACertificates.RemoveDefaults = ptr.Of(true)
	expect.CACertificates.Files = []string{"ca.crt"}
	expect.CACertificates.Certs = []string{
		"-----BEGIN CERTIFICATE-----\nYOUR-ORGS-TRUSTED-CA-CERT\n-----END CERTIFICATE-----\n",
	}

	expect.Rosetta = Rosetta{
		Enabled: ptr.Of(false),
		BinFmt:  ptr.Of(false),
	}
	expect.Plain = ptr.Of(false)

	expect.NestedVirtualization = ptr.Of(false)

	FillDefault(&y, &d, &o, filePath, false)
	assert.DeepEqual(t, &y, &expect, opts...)
}

func TestContainerdDefault(t *testing.T) {
	archives := defaultContainerdArchives()
	assert.Assert(t, len(archives) > 0)
}
