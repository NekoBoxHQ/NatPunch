package client

import (
	"container/heap"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NekoBoxHQ/NatPunch/lib/conn"
	"github.com/NekoBoxHQ/NatPunch/lib/file"
	"github.com/NekoBoxHQ/NatPunch/lib/sheap"
	"github.com/astaxie/beego/logs"
	"github.com/pkg/errors"
)

var (
	isStart bool
	// serverConn 会被 heathCheck（重连时）写、check（检查 goroutine）读 —— 裸指针是 data race。
	serverConnMu sync.RWMutex
	serverConn   *conn.Conn
)

func setServerConn(c *conn.Conn) {
	serverConnMu.Lock()
	serverConn = c
	serverConnMu.Unlock()
}

func getServerConn() *conn.Conn {
	serverConnMu.RLock()
	defer serverConnMu.RUnlock()
	return serverConn
}

func heathCheck(healths []*file.Health, c *conn.Conn) bool {
	setServerConn(c)
	if isStart {
		for _, v := range healths {
			// 重建 HealthMap 必须加锁：session 的 check goroutine 可能正在并发写（阶段三 #8）
			v.Lock()
			v.HealthMap = make(map[string]int)
			v.Unlock()
		}
		return true
	}
	isStart = true
	h := &sheap.IntHeap{}
	for _, v := range healths {
		if v.HealthMaxFail > 0 && v.HealthCheckTimeout > 0 && v.HealthCheckInterval > 0 {
			v.HealthNextTime = time.Now().Add(time.Duration(v.HealthCheckInterval) * time.Second)
			heap.Push(h, v.HealthNextTime.Unix())
			v.Lock()
			v.HealthMap = make(map[string]int)
			v.Unlock()
		}
	}
	go session(healths, h)
	return true
}

func session(healths []*file.Health, h *sheap.IntHeap) {
	for {
		if h.Len() == 0 {
			logs.Error("health check error")
			break
		}
		rs := heap.Pop(h).(int64) - time.Now().Unix()
		if rs <= 0 {
			// 原来是 continue：这一条被弹出后再也不 Push 回去 → 该目标的定时检查
			// 从此消失；全部丢光后 h.Len()==0，健康检查永久停摆。
			// 已经到点就尽快跑（1 秒后），并且照常把下一次排进堆。
			rs = 1
		}
		timer := time.NewTimer(time.Duration(rs) * time.Second)
		<-timer.C
		timer.Stop()
		for _, v := range healths {
			if v.HealthNextTime.Before(time.Now()) {
				v.HealthNextTime = time.Now().Add(time.Duration(v.HealthCheckInterval) * time.Second)
				//check
				go check(v)
				//reset time
				heap.Push(h, v.HealthNextTime.Unix())
			}
		}
	}
}

// work when just one port and many target
func check(t *file.Health) {
	arr := strings.Split(t.HealthCheckTarget, ",")
	var err error
	var rs *http.Response
	for _, v := range arr {
		if t.HealthCheckType == "tcp" {
			var c net.Conn
			c, err = net.DialTimeout("tcp", v, time.Duration(t.HealthCheckTimeout)*time.Second)
			if err == nil {
				c.Close()
			}
		} else {
			client := &http.Client{}
			client.Timeout = time.Duration(t.HealthCheckTimeout) * time.Second
			rs, err = client.Get("http://" + v + t.HttpHealthUrl)
			if rs != nil {
				// 响应体必须读完并关闭：原来只看 StatusCode 不关 Body，
				// 每个检查周期泄漏一个 TCP 连接（几小时后 fd 就满了）。
				_, _ = io.Copy(io.Discard, rs.Body)
				_ = rs.Body.Close()
			}
			if err == nil && rs != nil && rs.StatusCode != 200 {
				err = errors.New("status code is not match")
			}
		}
		t.Lock()
		if err != nil {
			t.HealthMap[v] += 1
		} else {
			// 成功一次就把失败计数清零。原来只在「计数已经 >= MaxFail」时清零，
			// 于是间歇性失败会一直累加：3 次失败不连续也会被判下线（误报）。
			if t.HealthMap[v] >= t.HealthMaxFail {
				//send recovery add
				if sc := getServerConn(); sc != nil {
					sc.SendHealthInfo(v, "1")
				}
			}
			t.HealthMap[v] = 0
		}

		if t.HealthMap[v] > 0 && t.HealthMap[v]%t.HealthMaxFail == 0 {
			//send fail remove
			if sc := getServerConn(); sc != nil {
				sc.SendHealthInfo(v, "0")
			}
		}
		t.Unlock()
	}
}
