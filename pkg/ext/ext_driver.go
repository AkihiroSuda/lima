package ext

import (
	"github.com/lima-vm/lima/pkg/driver"
)

type LimaExtDriver struct {
	*driver.BaseDriver
}

func New(driver *driver.BaseDriver) *LimaExtDriver {
	return &LimaExtDriver{
		BaseDriver: driver,
	}
}
