package driverutil

import (
	"github.com/lima-vm/lima/pkg/driver"
	"github.com/lima-vm/lima/pkg/ext"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/qemu"
	"github.com/lima-vm/lima/pkg/vz"
	"github.com/lima-vm/lima/pkg/wsl2"
)

func CreateTargetDriverInstance(base *driver.BaseDriver) driver.Driver {
	limaDriver := base.Instance.Config.VMType
	if *limaDriver == limayaml.VZ {
		return vz.New(base)
	}
	if *limaDriver == limayaml.WSL2 {
		return wsl2.New(base)
	}
	if *limaDriver == limayaml.EXT {
		return ext.New(base)
	}
	return qemu.New(base)
}
