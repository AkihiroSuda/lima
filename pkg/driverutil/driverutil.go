package driverutil

import (
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/vz"
	"github.com/lima-vm/lima/pkg/wsl2"
)

// Drivers returns the available drivers.
func Drivers() []string {
	drivers := []string{limayaml.QEMU}
	if vz.Enabled {
		drivers = append(drivers, limayaml.VZ)
	}
	if wsl2.Enabled {
		drivers = append(drivers, limayaml.WSL2)
	}
	drivers = append(drivers, limayaml.EXT)
	return drivers
}
