package file

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NekoBoxHQ/NatPunch/lib/common"
	"github.com/NekoBoxHQ/NatPunch/lib/rate"
)

// 回归测试：clients.json 落盘 / 列表接口的 json.Marshal 与「客户端上线状态更新」并发。
//
// 旧实现里 dealClientData 直接写 v.IsConnect / v.Version / v.LastOnlineTime，
// MarshalJSON 这边也不加锁：string 头是 (ptr,len) 两个字，读方能拿到「新指针 + 旧长度」，
// 轻则列表乱码，重则把相邻堆内存当字符串尾序列化出去；32 位上 int64 还会撕裂。
// 现在写方持写锁、MarshalJSON 持读锁，本测试在 -race 下必须干净。
func TestClientMarshalConcurrentWithUpdates(t *testing.T) {
	c := NewClient("vkey-abcdefgh", false, false)
	c.Id = 1
	c.Rate = rate.NewRate(0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.Lock()
			c.Addr = "39.109.56." + string(rune('0'+i%10)) + ":1234"
			c.LocalAddr = "192.168.100." + string(rune('0'+i%10))
			c.IsConnect = i%2 == 0
			c.Version = "v26.10.57"
			c.LastOnlineTime = "2026-10-30 01:00:00"
			c.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			b, err := json.Marshal(c)
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			if len(b) == 0 {
				t.Error("empty json")
				return
			}
		}
	}()
	wg.Wait()
}

// 回归测试：快捷命令的「读」与「保存」并发（原来是裸指针换手）。
func TestQuickCmdsConcurrent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0755); err != nil {
		t.Fatal(err)
	}
	db := &DbUtils{JsonDb: NewJsonDb(dir)}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			db.SaveQuickCmds(&QuickCmds{
				Customs: []QuickCmdItem{{Label: "df", Cmd: "df -h"}},
				Hidden:  []string{},
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if q := db.GetQuickCmds(); q == nil {
				t.Error("nil quick cmds")
				return
			}
		}
	}()
	wg.Wait()
}

// 回归测试：tasks.json 里 Client 为 null 时不能把整个服务打崩。
//
// 旧实现直接 post.Client.Id -> nil 解引用 panic，发生在 GetDb() 初始化阶段，
// 结果是面板**根本起不来**，只能手工去删 tasks.json。
func TestLoadTaskWithNullClientDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf"), 0755); err != nil {
		t.Fatal(err)
	}
	db := NewJsonDb(dir)
	db.Clients.Store(1, NewClient("vkey-12345678", false, false))

	// 一条正常、一条 Client=null、一条引用不存在的客户端
	body := `{"Id":1,"Mode":"tcp","Client":{"Id":1}}` + "\n" + common.CONN_DATA_SEQ +
		`{"Id":2,"Mode":"tcp","Client":null}` + "\n" + common.CONN_DATA_SEQ +
		`{"Id":3,"Mode":"tcp","Client":{"Id":99}}` + "\n" + common.CONN_DATA_SEQ
	if err := os.WriteFile(filepath.Join(dir, "conf", "tasks.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("加载含 null Client 的 tasks.json 时 panic: %v", r)
		}
	}()
	db.LoadTaskFromJsonFile()

	if _, ok := db.Tasks.Load(1); !ok {
		t.Error("正常条目应当被加载")
	}
	if _, ok := db.Tasks.Load(2); ok {
		t.Error("Client 为 null 的条目应当被跳过")
	}
	if _, ok := db.Tasks.Load(3); ok {
		t.Error("引用不存在客户端的条目应当被跳过")
	}
}
