package file

import (
	"sort"
	"strings"
)

func lessBool(a, b bool, asc bool) bool {
	if a == b {
		return false
	}
	if asc {
		return !a && b // false first
	}
	return a && !b // true first
}

func lessInt(a, b int, asc bool) bool {
	if asc {
		return a < b
	}
	return a > b
}

func lessInt64(a, b int64, asc bool) bool {
	if asc {
		return a < b
	}
	return a > b
}

func lessString(a, b string, asc bool) bool {
	if asc {
		return strings.ToLower(a) < strings.ToLower(b)
	}
	return strings.ToLower(a) > strings.ToLower(b)
}

// SortClients sorts clients in-place by the given field (bootstrap-table sort name).
func SortClients(list []*Client, sortField, order string) {
	if sortField == "" || len(list) < 2 {
		if sortField == "" && len(list) > 1 {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		}
		return
	}
	asc := order != "desc"
	sort.SliceStable(list, func(i, j int) bool {
		a, b := clientSortSnapshot(list[i]), clientSortSnapshot(list[j])
		switch sortField {
		case "Id":
			return lessInt(a.id, b.id, asc)
		case "Remark":
			return lessString(a.remark, b.remark, asc)
		case "Version":
			return lessString(a.version, b.version, asc)
		case "VerifyKey":
			return lessString(a.verifyKey, b.verifyKey, asc)
		case "Addr":
			return lessString(a.addr, b.addr, asc)
		case "LocalAddr":
			return lessString(a.localAddr, b.localAddr, asc)
		case "InletFlow":
			return lessInt64(a.inlet, b.inlet, asc)
		case "ExportFlow":
			return lessInt64(a.outlet, b.outlet, asc)
		case "NowRate":
			return lessInt64(a.rate, b.rate, asc)
		case "Status":
			return lessBool(a.status, b.status, asc)
		case "IsConnect":
			return lessBool(a.connected, b.connected, asc)
		default:
			return lessInt(a.id, b.id, true)
		}
	})
}

type clientSortValues struct {
	id                                          int
	rate                                        int64
	inlet, outlet                               int64
	remark, version, verifyKey, addr, localAddr string
	status, connected                           bool
}

func clientSortSnapshot(c *Client) clientSortValues {
	if c == nil {
		return clientSortValues{}
	}
	c.RLock()
	v := clientSortValues{
		id:        c.Id,
		remark:    c.Remark,
		version:   c.Version,
		verifyKey: c.VerifyKey,
		addr:      c.Addr,
		localAddr: c.LocalAddr,
		status:    c.Status,
		connected: c.IsConnect,
	}
	flow, rt := c.Flow, c.Rate
	c.RUnlock()
	if flow != nil {
		_, v.inlet, v.outlet = flow.Snapshot()
	}
	if rt != nil {
		v.rate = rt.GetNowRate()
	}
	return v
}

// SortTunnels sorts tunnels in-place by the given field.
func SortTunnels(list []*Tunnel, sortField, order string) {
	if sortField == "" || len(list) < 2 {
		if sortField == "" && len(list) > 1 {
			// 默认：按客户端 ID 升序（同客户端排在一起），再按 Id 升序
			sort.SliceStable(list, func(i, j int) bool {
				a, b := tunnelSortSnapshot(list[i]), tunnelSortSnapshot(list[j])
				ci, cj := a.clientID, b.clientID
				if ci != cj {
					return ci < cj
				}
				return a.id < b.id
			})
		}
		return
	}
	asc := order != "desc"
	sort.SliceStable(list, func(i, j int) bool {
		a, b := tunnelSortSnapshot(list[i]), tunnelSortSnapshot(list[j])
		switch sortField {
		case "Id":
			return lessInt(a.id, b.id, asc)
		case "ClientId":
			return lessInt(a.clientID, b.clientID, asc)
		case "Remark":
			return lessString(a.remark, b.remark, asc)
		case "Client.VerifyKey", "VerifyKey":
			return lessString(a.verifyKey, b.verifyKey, asc)
		case "Mode":
			return lessString(a.mode, b.mode, asc)
		case "Port":
			return lessInt(a.port, b.port, asc)
		case "Target":
			return lessString(a.target, b.target, asc)
		case "Password":
			return lessString(a.password, b.password, asc)
		case "Status":
			return lessBool(a.status, b.status, asc)
		case "RunStatus":
			return lessBool(a.runStatus, b.runStatus, asc)
		case "IsConnect", "Client.IsConnect":
			return lessBool(a.connected, b.connected, asc)
		default:
			return lessInt(a.id, b.id, true)
		}
	})
}

type tunnelSortValues struct {
	id, clientID, port                        int
	remark, verifyKey, mode, target, password string
	status, runStatus, connected              bool
}

func tunnelSortSnapshot(t *Tunnel) tunnelSortValues {
	if t == nil {
		return tunnelSortValues{}
	}
	t.RLock()
	v := tunnelSortValues{
		id:        t.Id,
		remark:    t.Remark,
		mode:      t.Mode,
		port:      t.Port,
		password:  t.Password,
		status:    t.Status,
		runStatus: t.RunStatus,
	}
	client, target := t.Client, t.Target
	t.RUnlock()
	if client != nil {
		client.RLock()
		v.clientID = client.Id
		v.verifyKey = client.VerifyKey
		v.connected = client.IsConnect
		client.RUnlock()
	}
	if target != nil {
		target.RLock()
		v.target = target.TargetStr
		target.RUnlock()
	}
	return v
}

// SortHosts sorts hosts in-place by the given field.
func SortHosts(list []*Host, sortField, order string) {
	if sortField == "" || len(list) < 2 {
		if sortField == "" && len(list) > 1 {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Id < list[j].Id })
		}
		return
	}
	asc := order != "desc"
	sort.SliceStable(list, func(i, j int) bool {
		a, b := hostSortSnapshot(list[i]), hostSortSnapshot(list[j])
		switch sortField {
		case "Id":
			return lessInt(a.id, b.id, asc)
		case "ClientId":
			return lessInt(a.clientID, b.clientID, asc)
		case "Remark":
			return lessString(a.remark, b.remark, asc)
		case "Client.VerifyKey", "VerifyKey":
			return lessString(a.verifyKey, b.verifyKey, asc)
		case "Host":
			return lessString(a.host, b.host, asc)
		case "Scheme":
			return lessString(a.scheme, b.scheme, asc)
		case "Target":
			return lessString(a.target, b.target, asc)
		case "Location":
			return lessString(a.location, b.location, asc)
		case "IsClose", "Status":
			// IsClose: false=open, true=closed — sort by open status for "Status" display
			return lessBool(a.closed, b.closed, asc)
		case "IsConnect", "Client.IsConnect":
			return lessBool(a.connected, b.connected, asc)
		default:
			return lessInt(a.id, b.id, true)
		}
	})
}

type hostSortValues struct {
	id, clientID             int
	remark, verifyKey, host  string
	scheme, target, location string
	closed, connected        bool
}

func hostSortSnapshot(h *Host) hostSortValues {
	if h == nil {
		return hostSortValues{}
	}
	h.RLock()
	v := hostSortValues{
		id:       h.Id,
		remark:   h.Remark,
		host:     h.Host,
		scheme:   h.Scheme,
		location: h.Location,
		closed:   h.IsClose,
	}
	client, target := h.Client, h.Target
	h.RUnlock()
	if client != nil {
		client.RLock()
		v.clientID = client.Id
		v.verifyKey = client.VerifyKey
		v.connected = client.IsConnect
		client.RUnlock()
	}
	if target != nil {
		target.RLock()
		v.target = target.TargetStr
		target.RUnlock()
	}
	return v
}
