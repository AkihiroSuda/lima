package guestagent

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/elastic/go-libaudit/v2"
	"github.com/elastic/go-libaudit/v2/auparse"
	"github.com/lima-vm/lima/pkg/guestagent/api"
	"github.com/lima-vm/lima/pkg/guestagent/iptables"
	"github.com/lima-vm/lima/pkg/guestagent/procnettcp"
	"github.com/lima-vm/lima/pkg/guestagent/procnetunix"
	"github.com/sirupsen/logrus"
	"github.com/yalue/native_endian"
)

func New(newTicker func() (<-chan time.Time, func()), iptablesIdle time.Duration) (Agent, error) {
	a := &agent{
		newTicker: newTicker,
	}

	auditClient, err := libaudit.NewMulticastAuditClient(nil)
	if err != nil {
		return nil, err
	}
	auditStatus, err := auditClient.GetStatus()
	if err != nil {
		return nil, err
	}
	if auditStatus.Enabled == 0 {
		if err = auditClient.SetEnabled(true, libaudit.WaitForReply); err != nil {
			return nil, err
		}
	}

	go a.setWorthCheckingIPTablesRoutine(auditClient, iptablesIdle)
	return a, nil
}

type agent struct {
	// Ticker is like time.Ticker.
	// We can't use inotify for /proc/net/tcp, so we need this ticker to
	// reload /proc/net/tcp.
	newTicker func() (<-chan time.Time, func())

	worthCheckingIPTables   bool
	worthCheckingIPTablesMu sync.RWMutex
	latestIPTables          []iptables.Entry
	latestIPTablesMu        sync.RWMutex
}

// setWorthCheckingIPTablesRoutine sets worthCheckingIPTables to be true
// when received NETFILTER_CFG audit message.
//
// setWorthCheckingIPTablesRoutine sets worthCheckingIPTables to be false
// when no NETFILTER_CFG audit message was received for the iptablesIdle time.
func (a *agent) setWorthCheckingIPTablesRoutine(auditClient *libaudit.AuditClient, iptablesIdle time.Duration) {
	var latestTrue time.Time
	go func() {
		for {
			time.Sleep(iptablesIdle)
			a.worthCheckingIPTablesMu.Lock()
			// time is monotonic, see https://pkg.go.dev/time#hdr-Monotonic_Clocks
			elapsedSinceLastTrue := time.Since(latestTrue)
			if elapsedSinceLastTrue >= iptablesIdle {
				logrus.Debug("setWorthCheckingIPTablesRoutine(): setting to false")
				a.worthCheckingIPTables = false
			}
			a.worthCheckingIPTablesMu.Unlock()
		}
	}()
	for {
		msg, err := auditClient.Receive(false)
		if err != nil {
			logrus.Error(err)
			continue
		}
		switch msg.Type {
		case auparse.AUDIT_NETFILTER_CFG:
			a.worthCheckingIPTablesMu.Lock()
			logrus.Debug("setWorthCheckingIPTablesRoutine(): setting to true")
			a.worthCheckingIPTables = true
			latestTrue = time.Now()
			a.worthCheckingIPTablesMu.Unlock()
		}
	}
}

type eventState struct {
	ports   []api.IPPort
	sockets []string
}

func comparePorts(old, neww []api.IPPort) (added, removed []api.IPPort) {
	mRaw := make(map[string]api.IPPort, len(old))
	mStillExist := make(map[string]bool, len(old))

	for _, f := range old {
		k := f.String()
		mRaw[k] = f
		mStillExist[k] = false
	}
	for _, f := range neww {
		k := f.String()
		if _, ok := mRaw[k]; !ok {
			added = append(added, f)
		}
		mStillExist[k] = true
	}

	for k, stillExist := range mStillExist {
		if !stillExist {
			if x, ok := mRaw[k]; ok {
				removed = append(removed, x)
			}
		}
	}
	return
}

func compareSockets(old, neww []string) (added, removed []string) {
	mRaw := make(map[string]string, len(old))
	mStillExist := make(map[string]bool, len(old))

	for _, k := range old {
		mStillExist[k] = false
	}
	for _, k := range neww {
		if _, ok := mStillExist[k]; !ok {
			added = append(added, k)
		}
		mStillExist[k] = true
	}

	for k, stillExist := range mStillExist {
		if !stillExist {
			if x, ok := mRaw[k]; ok {
				removed = append(removed, x)
			}
		}
	}
	return
}

func (a *agent) collectEvent(ctx context.Context, st eventState) (api.Event, eventState) {
	var (
		ev  api.Event
		err error
	)
	newSt := st
	newSt.ports, err = a.LocalPorts(ctx)
	if err != nil {
		ev.Errors = append(ev.Errors, err.Error())
		ev.Time = time.Now()
		return ev, newSt
	}
	ev.LocalPortsAdded, ev.LocalPortsRemoved = comparePorts(st.ports, newSt.ports)

	newSt.sockets, err = a.LocalSockets(ctx)
	if err != nil {
		ev.Errors = append(ev.Errors, err.Error())
		ev.Time = time.Now()
		return ev, newSt
	}
	ev.LocalSocketsAdded, ev.LocalSocketsRemoved = compareSockets(st.sockets, newSt.sockets)

	ev.Time = time.Now()
	return ev, newSt
}

func isEventEmpty(ev api.Event) bool {
	var empty api.Event
	// ignore ev.Time
	copied := ev
	copied.Time = time.Time{}
	return reflect.DeepEqual(empty, copied)
}

func (a *agent) Events(ctx context.Context, ch chan api.Event) {
	defer close(ch)
	tickerCh, tickerClose := a.newTicker()
	defer tickerClose()
	var st eventState
	for {
		var ev api.Event
		ev, st = a.collectEvent(ctx, st)
		if !isEventEmpty(ev) {
			ch <- ev
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-tickerCh:
			if !ok {
				return
			}
			logrus.Debug("tick!")
		}
	}
}

func (a *agent) LocalPorts(ctx context.Context) ([]api.IPPort, error) {
	if native_endian.NativeEndian() == binary.BigEndian {
		return nil, errors.New("big endian architecture is unsupported, because I don't know how /proc/net/tcp looks like on big endian hosts")
	}
	var res []api.IPPort
	tcpParsed, err := procnettcp.ParseFiles()
	if err != nil {
		return res, err
	}

	for _, f := range tcpParsed {
		switch f.Kind {
		case procnettcp.TCP, procnettcp.TCP6:
		default:
			continue
		}
		if f.State == procnettcp.TCPListen {
			res = append(res,
				api.IPPort{
					IP:   f.IP,
					Port: int(f.Port),
				})
		}
	}

	a.worthCheckingIPTablesMu.RLock()
	worthCheckingIPTables := a.worthCheckingIPTables
	a.worthCheckingIPTablesMu.RUnlock()
	logrus.Debugf("LocalPorts(): worthCheckingIPTables=%v", worthCheckingIPTables)

	var ipts []iptables.Entry
	if a.worthCheckingIPTables {
		ipts, err = iptables.GetPorts()
		if err != nil {
			return res, err
		}
		a.latestIPTablesMu.Lock()
		a.latestIPTables = ipts
		a.latestIPTablesMu.Unlock()
	} else {
		a.latestIPTablesMu.RLock()
		ipts = a.latestIPTables
		a.latestIPTablesMu.RUnlock()
	}

	for _, ipt := range ipts {
		// Make sure the port isn't already listed from procnettcp
		found := false
		for _, re := range res {
			if re.Port == ipt.Port {
				found = true
			}
		}
		if !found {
			res = append(res,
				api.IPPort{
					IP:   ipt.IP,
					Port: ipt.Port,
				})
		}
	}

	return res, nil
}

func (a *agent) Info(ctx context.Context) (*api.Info, error) {
	var (
		info api.Info
		err  error
	)
	info.LocalPorts, err = a.LocalPorts(ctx)
	if err != nil {
		return nil, err
	}
	info.LocalSockets, err = a.LocalSockets(ctx)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func (a *agent) LocalSockets(ctx context.Context) ([]string, error) {
	var res []string
	parsed, err := procnetunix.ParseFile()
	if err == nil {
		for _, f := range parsed {
			switch f.State {
			case procnetunix.StateUnconnected, procnetunix.StateConnecting, procnetunix.StateConnected:
				res = append(res, f.Path)
			}
		}
	}
	return res, err
}
