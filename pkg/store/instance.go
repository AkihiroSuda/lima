package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/docker/go-units"
	hostagentclient "github.com/lima-vm/lima/pkg/hostagent/api/client"
	"github.com/lima-vm/lima/pkg/identifierutil"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/lima-vm/lima/pkg/textutil"
	"github.com/lima-vm/lima/pkg/version/versionutil"
	"github.com/sirupsen/logrus"
)

type Status = string

const (
	StatusUnknown       Status = ""
	StatusUninitialized Status = "Uninitialized"
	StatusInstalling    Status = "Installing"
	StatusBroken        Status = "Broken"
	StatusStopped       Status = "Stopped"
	StatusRunning       Status = "Running"
)

type Instance struct {
	Name string `json:"name"`
	// Hostname, not HostName (corresponds to SSH's naming convention)
	Hostname        string             `json:"hostname"`
	Status          Status             `json:"status"`
	Dir             string             `json:"dir"`
	VMType          limayaml.VMType    `json:"vmType"`
	Arch            limayaml.Arch      `json:"arch"`
	CPUType         string             `json:"cpuType"`
	CPUs            int                `json:"cpus,omitempty"`
	Memory          int64              `json:"memory,omitempty"` // bytes
	Disk            int64              `json:"disk,omitempty"`   // bytes
	Message         string             `json:"message,omitempty"`
	AdditionalDisks []limayaml.Disk    `json:"additionalDisks,omitempty"`
	Networks        []limayaml.Network `json:"network,omitempty"`
	SSHLocalPort    int                `json:"sshLocalPort,omitempty"`
	SSHConfigFile   string             `json:"sshConfigFile,omitempty"`
	HostAgentPID    int                `json:"hostAgentPID,omitempty"`
	DriverPID       int                `json:"driverPID,omitempty"`
	Errors          []error            `json:"errors,omitempty"`
	Config          *limayaml.LimaYAML `json:"config,omitempty"`
	SSHAddress      string             `json:"sshAddress,omitempty"`
	Protected       bool               `json:"protected"`
	LimaVersion     string             `json:"limaVersion"`
	Param           map[string]string  `json:"param,omitempty"`
}

// Inspect returns err only when the instance does not exist (os.ErrNotExist).
// Other errors are returned as *Instance.Errors.
func Inspect(instName string) (*Instance, error) {
	inst := &Instance{
		Name: instName,
		// TODO: support customizing hostname
		Hostname: identifierutil.HostnameFromInstName(instName),
		Status:   StatusUnknown,
	}
	// InstanceDir validates the instName but does not check whether the instance exists
	instDir, err := InstanceDir(instName)
	if err != nil {
		return nil, err
	}
	// Make sure inst.Dir is set, even when YAML validation fails
	inst.Dir = instDir
	yamlPath := filepath.Join(instDir, filenames.LimaYAML)
	y, err := LoadYAMLByFilePath(yamlPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		inst.Errors = append(inst.Errors, err)
		return inst, nil
	}
	inst.Config = y
	inst.Arch = *y.Arch
	inst.VMType = *y.VMType
	inst.CPUType = y.CPUType[*y.Arch]
	inst.SSHAddress = *y.SSH.Address
	inst.SSHLocalPort = *y.SSH.LocalPort // maybe 0
	inst.SSHConfigFile = filepath.Join(instDir, filenames.SSHConfig)
	inst.HostAgentPID, err = ReadPIDFile(filepath.Join(instDir, filenames.HostAgentPID))
	if err != nil {
		inst.Status = StatusBroken
		inst.Errors = append(inst.Errors, err)
	}

	if inst.HostAgentPID != 0 {
		haSock := filepath.Join(instDir, filenames.HostAgentSock)
		haClient, err := hostagentclient.NewHostAgentClient(haSock)
		if err != nil {
			inst.Status = StatusBroken
			inst.Errors = append(inst.Errors, fmt.Errorf("failed to connect to %q: %w", haSock, err))
		} else {
			ctx, cancel := context.WithTimeout(context.TODO(), 3*time.Second)
			defer cancel()
			info, err := haClient.Info(ctx)
			if err != nil {
				inst.Status = StatusBroken
				inst.Errors = append(inst.Errors, fmt.Errorf("failed to get Info from %q: %w", haSock, err))
			} else {
				inst.SSHLocalPort = info.SSHLocalPort
			}
		}
	}

	inst.CPUs = *y.CPUs
	memory, err := units.RAMInBytes(*y.Memory)
	if err == nil {
		inst.Memory = memory
	}
	disk, err := units.RAMInBytes(*y.Disk)
	if err == nil {
		inst.Disk = disk
	}
	inst.AdditionalDisks = y.AdditionalDisks
	inst.Networks = y.Networks

	// 0 out values since not configurable on WSL2
	if inst.VMType == limayaml.WSL2 {
		inst.Memory = 0
		inst.CPUs = 0
		inst.Disk = 0
	}

	protected := filepath.Join(instDir, filenames.Protected)
	if _, err := os.Lstat(protected); !errors.Is(err, os.ErrNotExist) {
		inst.Protected = true
	}

	inspectStatus(instDir, inst, y)

	tmpl, err := template.New("format").Parse(y.Message)
	if err != nil {
		inst.Errors = append(inst.Errors, fmt.Errorf("message %q is not a valid template: %w", y.Message, err))
		inst.Status = StatusBroken
	} else {
		data, err := AddGlobalFields(inst)
		if err != nil {
			inst.Errors = append(inst.Errors, fmt.Errorf("cannot add global fields to instance data: %w", err))
			inst.Status = StatusBroken
		} else {
			var message strings.Builder
			err = tmpl.Execute(&message, data)
			if err != nil {
				inst.Errors = append(inst.Errors, fmt.Errorf("cannot execute template %q: %w", y.Message, err))
				inst.Status = StatusBroken
			} else {
				inst.Message = message.String()
			}
		}
	}

	limaVersionFile := filepath.Join(instDir, filenames.LimaVersion)
	if version, err := os.ReadFile(limaVersionFile); err == nil {
		inst.LimaVersion = strings.TrimSpace(string(version))
		if _, err = versionutil.Parse(inst.LimaVersion); err != nil {
			logrus.Warnf("treating lima version %q from %q as very latest release", inst.LimaVersion, limaVersionFile)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		inst.Errors = append(inst.Errors, err)
	}
	inst.Param = y.Param
	return inst, nil
}

func inspectStatusWithPIDFiles(instDir string, inst *Instance, y *limayaml.LimaYAML) {
	var err error
	inst.DriverPID, err = ReadPIDFile(filepath.Join(instDir, filenames.PIDFile(*y.VMType)))
	if err != nil {
		inst.Status = StatusBroken
		inst.Errors = append(inst.Errors, err)
	}

	if inst.Status == StatusUnknown {
		switch {
		case inst.HostAgentPID > 0 && inst.DriverPID > 0:
			inst.Status = StatusRunning
		case inst.HostAgentPID == 0 && inst.DriverPID == 0:
			inst.Status = StatusStopped
		case inst.HostAgentPID > 0 && inst.DriverPID == 0:
			inst.Errors = append(inst.Errors, errors.New("host agent is running but driver is not"))
			inst.Status = StatusBroken
		default:
			inst.Errors = append(inst.Errors, fmt.Errorf("%s driver is running but host agent is not", inst.VMType))
			inst.Status = StatusBroken
		}
	}
}

// ReadPIDFile returns 0 if the PID file does not exist or the process has already terminated
// (in which case the PID file will be removed).
func ReadPIDFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, err
	}
	// os.FindProcess will only return running processes on Windows, exit early
	if runtime.GOOS == "windows" {
		return pid, nil
	}
	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			_ = os.Remove(path)
			return 0, nil
		}
		// We may not have permission to send the signal (e.g. to network daemon running as root).
		// But if we get a permissions error, it means the process is still running.
		if !errors.Is(err, os.ErrPermission) {
			return 0, err
		}
	}
	return pid, nil
}

type FormatData struct {
	Instance
	HostOS       string
	HostArch     string
	LimaHome     string
	IdentityFile string
}

var FormatHelp = "\n" +
	"These functions are available to go templates:\n\n" +
	textutil.IndentString(2,
		strings.Join(textutil.FuncHelp, "\n")+"\n")

func AddGlobalFields(inst *Instance) (FormatData, error) {
	var data FormatData
	data.Instance = *inst
	// Add HostOS
	data.HostOS = runtime.GOOS
	// Add HostArch
	data.HostArch = limayaml.NewArch(runtime.GOARCH)
	// Add IdentityFile
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return FormatData{}, err
	}
	data.IdentityFile = filepath.Join(configDir, filenames.UserPrivateKey)
	// Add LimaHome
	data.LimaHome, err = dirnames.LimaDir()
	if err != nil {
		return FormatData{}, err
	}
	return data, nil
}

type PrintOptions struct {
	AllFields     bool
	TerminalWidth int
}

// PrintInstances prints instances in a requested format to a given io.Writer.
// Supported formats are "json", "yaml", "table", or a go template.
func PrintInstances(w io.Writer, instances []*Instance, format string, options *PrintOptions) error {
	switch format {
	case "json":
		format = "{{json .}}"
	case "yaml":
		format = "{{yaml .}}"
	case "table":
		types := map[string]int{}
		archs := map[string]int{}
		for _, instance := range instances {
			types[instance.VMType]++
			archs[instance.Arch]++
		}
		all := options != nil && options.AllFields
		width := 0
		if options != nil {
			width = options.TerminalWidth
		}
		columnWidth := 8
		hideType := false
		hideArch := false
		hideDir := false

		columns := 1 // NAME
		columns += 2 // STATUS
		columns += 2 // SSH
		// can we still fit the remaining columns (7)
		if width == 0 || (columns+7)*columnWidth > width && !all {
			hideType = len(types) == 1
		}
		if !hideType {
			columns++ // VMTYPE
		}
		// only hide arch if it is the same as the host arch
		goarch := limayaml.NewArch(runtime.GOARCH)
		// can we still fit the remaining columns (6)
		if width == 0 || (columns+6)*columnWidth > width && !all {
			hideArch = len(archs) == 1 && instances[0].Arch == goarch
		}
		if !hideArch {
			columns++ // ARCH
		}
		columns++ // CPUS
		columns++ // MEMORY
		columns++ // DISK
		// can we still fit the remaining columns (2)
		if width != 0 && (columns+2)*columnWidth > width && !all {
			hideDir = true
		}
		if !hideDir {
			columns += 2 // DIR
		}
		_ = columns

		w := tabwriter.NewWriter(w, 4, 8, 4, ' ', 0)
		fmt.Fprint(w, "NAME\tSTATUS\tSSH")
		if !hideType {
			fmt.Fprint(w, "\tVMTYPE")
		}
		if !hideArch {
			fmt.Fprint(w, "\tARCH")
		}
		fmt.Fprint(w, "\tCPUS\tMEMORY\tDISK")
		if !hideDir {
			fmt.Fprint(w, "\tDIR")
		}
		fmt.Fprintln(w)

		u, err := user.Current()
		if err != nil {
			return err
		}
		homeDir := u.HomeDir

		for _, instance := range instances {
			dir := instance.Dir
			if strings.HasPrefix(dir, homeDir) {
				dir = strings.Replace(dir, homeDir, "~", 1)
			}
			fmt.Fprintf(w, "%s\t%s\t%s",
				instance.Name,
				instance.Status,
				fmt.Sprintf("%s:%d", instance.SSHAddress, instance.SSHLocalPort),
			)
			if !hideType {
				fmt.Fprintf(w, "\t%s",
					instance.VMType,
				)
			}
			if !hideArch {
				fmt.Fprintf(w, "\t%s",
					instance.Arch,
				)
			}
			fmt.Fprintf(w, "\t%d\t%s\t%s",
				instance.CPUs,
				units.BytesSize(float64(instance.Memory)),
				units.BytesSize(float64(instance.Disk)),
			)
			if !hideDir {
				fmt.Fprintf(w, "\t%s",
					dir,
				)
			}
			fmt.Fprint(w, "\n")
		}
		return w.Flush()
	default:
		// NOP
	}
	tmpl, err := template.New("format").Funcs(textutil.TemplateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("invalid go template: %w", err)
	}
	for _, instance := range instances {
		data, err := AddGlobalFields(instance)
		if err != nil {
			return err
		}
		data.Message = strings.TrimSuffix(instance.Message, "\n")
		err = tmpl.Execute(w, data)
		if err != nil {
			return err
		}
		fmt.Fprintln(w)
	}
	return nil
}

// Protect protects the instance to prohibit accidental removal.
// Protect does not return an error even when the instance is already protected.
func (inst *Instance) Protect() error {
	protected := filepath.Join(inst.Dir, filenames.Protected)
	// TODO: Do an equivalent of `chmod +a "everyone deny delete,delete_child,file_inherit,directory_inherit"`
	// https://github.com/lima-vm/lima/issues/1595
	if err := os.WriteFile(protected, nil, 0o400); err != nil {
		return err
	}
	inst.Protected = true
	return nil
}

// Unprotect unprotects the instance.
// Unprotect does not return an error even when the instance is already unprotected.
func (inst *Instance) Unprotect() error {
	protected := filepath.Join(inst.Dir, filenames.Protected)
	if err := os.RemoveAll(protected); err != nil {
		return err
	}
	inst.Protected = false
	return nil
}
