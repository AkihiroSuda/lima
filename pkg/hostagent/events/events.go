package events

import (
	"time"
)

type Status struct {
	Running bool `json:"running,omitempty"`
	// When Degraded is true, Running must be true as well
	Degraded bool `json:"degraded,omitempty"`
	// When Exiting is true, Running must be false
	Exiting bool `json:"exiting,omitempty"`

	Errors []string `json:"errors,omitempty"`

	SSHIPAddress string `json:"sshIPAddress,omitempty"`
	SSHLocalPort int    `json:"sshLocalPort,omitempty"`
}

type Event struct {
	Time   time.Time `json:"time,omitempty"`
	Status Status    `json:"status,omitempty"`
}
