package ext

import (
	"github.com/lima-vm/lima/pkg/driver"
)

const Enabled = true

type LimaExtDriver struct {
	*driver.BaseDriver
}

func New(driver *driver.BaseDriver) *LimaExtDriver {
	return &LimaExtDriver{
		BaseDriver: driver,
	}
}
