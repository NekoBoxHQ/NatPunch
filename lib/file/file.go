package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/astaxie/beego/logs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
)

func NewJsonDb(runPath string) *JsonDb {
	return &JsonDb{
		RunPath:           runPath,
		TaskFilePath:      filepath.Join(runPath, "conf", "tasks.json"),
		HostFilePath:      filepath.Join(runPath, "conf", "hosts.json"),
		ClientFilePath:    filepath.Join(runPath, "conf", "clients.json"),
		GlobalFilePath:    filepath.Join(runPath, "conf", "global.json"),
		QuickCmdsFilePath: filepath.Join(runPath, "conf", "quick_cmds.json"),
	}
}

type JsonDb struct {
	Tasks             sync.Map
	Hosts             sync.Map
	Clients           sync.Map
	Global            *Glob
	Quick             *QuickCmds
	quickMu           sync.Mutex // 护住 Quick 指针（Get/Save/落盘都要过它）
	RunPath           string
	ClientIncreaseId  int32  //client increased id
	TaskIncreaseId    int32  //task increased id
	HostIncreaseId    int32  //host increased id
	TaskFilePath      string //task file path
	HostFilePath      string //host file path
	ClientFilePath    string //client file path
	GlobalFilePath    string //global file path
	QuickCmdsFilePath string //quick cmds file path
}

// QuickCmdItem 快捷命令项
type QuickCmdItem struct {
	Label string `json:"label"`
	Cmd   string `json:"cmd"`
}

// QuickCmds 快捷命令持久化数据（面板服务器存储）
type QuickCmds struct {
	Customs []QuickCmdItem `json:"customs"`
	Hidden  []string       `json:"hidden"`
}

func (s *JsonDb) LoadTaskFromJsonFile() {
	loadSyncMapFromFile(s.TaskFilePath, func(idx int, v string) {
		post := new(Tunnel)
		if json.Unmarshal([]byte(v), &post) != nil {
			logs.Error("load %s: 第 %d 条不是合法 JSON，已跳过（下次保存会把它从文件里丢掉）", s.TaskFilePath, idx)
			return
		}
		// 防御：tasks.json 里 Client 为 null（手工改坏 / 极端情况下的半截写入）
		// 会让下一行直接 nil 解引用 panic —— 那意味着整个面板起不来，只能手工删文件。
		// 这里跳过坏条目，其余隧道照常加载。
		if post.Client == nil {
			logs.Error("load %s: 隧道 %d 缺少 Client 字段，已跳过", s.TaskFilePath, post.Id)
			return
		}
		// 注意：GetClient 失败时返回的 c 是 nil，**不能**在 err != nil 分支里再读
		// post.Client.Id —— 那行赋值已经把 post.Client 覆盖成 nil 了（自己踩过一次）。
		cid := post.Client.Id
		cl, cerr := s.GetClient(cid)
		if cerr != nil {
			logs.Error("load %s: 隧道 %d 引用的客户端 %d 不存在，已跳过", s.TaskFilePath, post.Id, cid)
			return
		}
		post.Client = cl
		s.Tasks.Store(post.Id, post)
		if post.Id > int(s.TaskIncreaseId) {
			s.TaskIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadClientFromJsonFile() {
	loadSyncMapFromFile(s.ClientFilePath, func(idx int, v string) {
		post := new(Client)
		if json.Unmarshal([]byte(v), &post) != nil {
			logs.Error("load %s: 第 %d 条不是合法 JSON，已跳过（下次保存会把它从文件里丢掉）", s.ClientFilePath, idx)
			return
		}
		post.Rate = NewClientRate(post)
		post.Rate.Start()
		post.NowConn = 0
		// 重启后清空内网地址缓存，待客户端重连后重新上报（避免残留旧公网出口值）
		post.LocalAddr = ""
		s.Clients.Store(post.Id, post)
		if post.Id > int(s.ClientIncreaseId) {
			s.ClientIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadHostFromJsonFile() {
	loadSyncMapFromFile(s.HostFilePath, func(idx int, v string) {
		post := new(Host)
		if json.Unmarshal([]byte(v), &post) != nil {
			logs.Error("load %s: 第 %d 条不是合法 JSON，已跳过（下次保存会把它从文件里丢掉）", s.HostFilePath, idx)
			return
		}
		if post.Client == nil {
			logs.Error("load %s: Host %d 缺少 Client 字段，已跳过", s.HostFilePath, post.Id)
			return
		}
		// 同上：GetClient 失败时返回 nil，先存下 id 再查。
		cid := post.Client.Id
		cl, cerr := s.GetClient(cid)
		if cerr != nil {
			logs.Error("load %s: Host %d 引用的客户端 %d 不存在，已跳过", s.HostFilePath, post.Id, cid)
			return
		}
		post.Client = cl
		s.Hosts.Store(post.Id, post)
		if post.Id > int(s.HostIncreaseId) {
			s.HostIncreaseId = int32(post.Id)
		}
	})
}

func (s *JsonDb) LoadGlobalFromJsonFile() {
	loadSyncMapFromFileWithSingleJson(s.GlobalFilePath, func(v string) {
		post := new(Glob)
		if json.Unmarshal([]byte(v), &post) != nil {
			return
		}
		s.Global = post
	})
}

func (s *JsonDb) LoadQuickCmdsFromJsonFile() {
	s.Quick = &QuickCmds{Customs: []QuickCmdItem{}, Hidden: []string{}}
	if !common.FileExists(s.QuickCmdsFilePath) {
		return
	}
	b, err := common.ReadAllFromFile(s.QuickCmdsFilePath)
	if err != nil {
		return
	}
	if json.Unmarshal(b, s.Quick) != nil {
		return
	}
	if s.Quick.Customs == nil {
		s.Quick.Customs = []QuickCmdItem{}
	}
	if s.Quick.Hidden == nil {
		s.Quick.Hidden = []string{}
	}
}

func (s *JsonDb) GetClient(id int) (c *Client, err error) {
	if v, ok := s.Clients.Load(id); ok {
		c = v.(*Client)
		return
	}
	err = errors.New("未找到客户端")
	return
}

var hostLock sync.Mutex

func (s *JsonDb) StoreHostToJsonFile() error {
	hostLock.Lock()
	defer hostLock.Unlock()
	return storeSyncMapToFile(&s.Hosts, s.HostFilePath)
}

var taskLock sync.Mutex

func (s *JsonDb) StoreTasksToJsonFile() error {
	taskLock.Lock()
	defer taskLock.Unlock()
	return storeSyncMapToFile(&s.Tasks, s.TaskFilePath)
}

var clientLock sync.Mutex

func (s *JsonDb) StoreClientsToJsonFile() error {
	clientLock.Lock()
	defer clientLock.Unlock()
	return storeSyncMapToFile(&s.Clients, s.ClientFilePath)
}

var globalLock sync.Mutex

func (s *JsonDb) StoreGlobalToJsonFile() error {
	globalLock.Lock()
	defer globalLock.Unlock()
	return storeGlobalToFile(s.Global, s.GlobalFilePath)
}

var quickCmdsLock sync.Mutex

func (s *JsonDb) StoreQuickCmdsToJsonFile() {
	quickCmdsLock.Lock()
	defer quickCmdsLock.Unlock()
	s.quickMu.Lock()
	q := s.Quick
	s.quickMu.Unlock()
	if q == nil {
		return
	}
	b, err := json.Marshal(q)
	if err != nil {
		return
	}
	// 0600：与 clients.json / hosts.json 的写入口径一致（原 os.Create 为 0644，见复评🟡）
	file, err := os.OpenFile(s.QuickCmdsFilePath+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		logs.Error("store quick cmds: create tmp file error: %v", err)
		return
	}
	if _, err := file.Write(b); err != nil {
		logs.Error("store quick cmds: write error: %v", err)
		file.Close()
		os.Remove(s.QuickCmdsFilePath + ".tmp")
		return
	}
	_ = file.Sync()
	_ = file.Close()
	if err := os.Rename(s.QuickCmdsFilePath+".tmp", s.QuickCmdsFilePath); err != nil {
		logs.Error(err, "store quick cmds err, data will lost")
	}
}

var clientIdLock sync.Mutex

func (s *JsonDb) GetClientId() int32 {
	clientIdLock.Lock()
	defer clientIdLock.Unlock()
	used := make(map[int]bool)
	s.Clients.Range(func(key, value interface{}) bool {
		used[value.(*Client).Id] = true
		return true
	})
	for i := int32(1); ; i++ {
		if !used[int(i)] {
			s.ClientIncreaseId = i
			return i
		}
	}
}

var taskIdLock sync.Mutex

// GetTaskId 最小可用分配：删除后新建复用空号（内部 Id 全局唯一）
func (s *JsonDb) GetTaskId() int32 {
	taskIdLock.Lock()
	defer taskIdLock.Unlock()
	used := make(map[int]bool)
	s.Tasks.Range(func(key, value interface{}) bool {
		used[value.(*Tunnel).Id] = true
		return true
	})
	for i := int32(1); ; i++ {
		if !used[int(i)] {
			s.TaskIncreaseId = i
			return i
		}
	}
}

func (s *JsonDb) GetHostId() int32 {
	return atomic.AddInt32(&s.HostIncreaseId, 1)
}

func loadSyncMapFromFile(filePath string, f func(idx int, value string)) {
	if !common.FileExists(filePath) {
		return
	}
	b, err := common.ReadAllFromFile(filePath)
	if err != nil {
		// 读不动就别装作"库里没数据"继续跑：随后的 Store* 会用空库覆盖掉真实数据。
		// 直接 panic 让 systemd/procd 把进程拉起来重试，比静默丢数据强。
		panic(fmt.Sprintf("read %s failed: %v (refuse to start with a possibly empty db)", filePath, err))
	}
	for i, v := range strings.Split(string(b), "\n"+common.CONN_DATA_SEQ) {
		if strings.TrimSpace(v) == "" {
			continue
		}
		f(i, v)
	}
}

func loadSyncMapFromFileWithSingleJson(filePath string, f func(value string)) {
	if !common.FileExists(filePath) {
		return
	}

	b, err := common.ReadAllFromFile(filePath)
	if err != nil {
		panic(err)
	}

	f(string(b))
}

func storeSyncMapToFile(m *sync.Map, filePath string) error {
	// 0600：JSON 库文件含 VerifyKey/WebPassword 等敏感字段（阶段三 #13）
	file, err := os.OpenFile(filePath+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	// first create a temporary file to store
	if err != nil {
		logs.Error("store to file: create tmp file error: %v", err)
		return err
	}
	defer func() {
		file.Close()
		os.Remove(filePath + ".tmp")
	}()
	var writeErr bool
	m.Range(func(key, value interface{}) bool {
		var b []byte
		var err error
		switch value.(type) {
		case *Tunnel:
			obj := value.(*Tunnel)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		case *Host:
			obj := value.(*Host)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		case *Client:
			obj := value.(*Client)
			if obj.NoStore {
				return true
			}
			b, err = json.Marshal(obj)
		default:
			return true
		}
		if err != nil {
			return true
		}
		_, err = file.Write(b)
		if err != nil {
			logs.Error("store to file: write error: %v", err)
			writeErr = true
			return false
		}
		_, err = file.Write([]byte("\n" + common.CONN_DATA_SEQ))
		if err != nil {
			logs.Error("store to file: write separator error: %v", err)
			writeErr = true
			return false
		}
		return true
	})
	if writeErr {
		return errors.New("store to file: write error")
	}
	_ = file.Sync()
	_ = file.Close()
	err = os.Rename(filePath+".tmp", filePath)
	if err != nil {
		logs.Error(err, "store to file err, data will lost")
		return err
	}
	return nil
}

func storeGlobalToFile(m *Glob, filePath string) error {
	// 0600：全局配置含 ServerUrl 等（阶段三 #13）
	file, err := os.OpenFile(filePath+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	// first create a temporary file to store
	if err != nil {
		logs.Error("store global to file: create tmp file error: %v", err)
		return err
	}
	defer func() {
		file.Close()
		os.Remove(filePath + ".tmp")
	}()

	var b []byte
	b, err = json.Marshal(m)
	if err != nil {
		logs.Error("store global to file: marshal error: %v", err)
		return err
	}
	_, err = file.Write(b)
	if err != nil {
		logs.Error("store global to file: write error: %v", err)
		return err
	}
	_ = file.Sync()
	_ = file.Close()
	err = os.Rename(filePath+".tmp", filePath)
	if err != nil {
		logs.Error(err, "store to file err, data will lost")
		return err
	}
	return nil
}
