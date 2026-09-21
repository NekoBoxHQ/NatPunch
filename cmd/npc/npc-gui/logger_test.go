package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRingBuffer(t *testing.T) {
	r := newRingBuffer(3)
	r.add(ConnectionLog{Message: "1"})
	r.add(ConnectionLog{Message: "2"})
	r.add(ConnectionLog{Message: "3"})
	r.add(ConnectionLog{Message: "4"})
	all := r.all()
	if len(all) != 3 || all[0].Message != "2" || all[2].Message != "4" {
		t.Fatalf("unexpected ring buffer contents: %v", all)
	}
	r.reset()
	if len(r.all()) != 0 {
		t.Fatal("reset failed")
	}
}

func TestRotatingFileWriterRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := newRotatingFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.maxSize = 10
	w.maxBackups = 2
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("missing .1 backup:", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatal("missing .2 backup:", err)
	}
	if err := w.truncate(); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatal("truncate failed")
	}
}

func TestClientLogStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	fw, err := newRotatingFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	store := &clientLogStore{clientId: "test", ring: newRingBuffer(logBufferCap), file: fw}
	defer fw.Close()

	store.log("info", "hello %s", "world")
	store.log("error", "boom")
	store.log("info", "连接成功")

	snap := store.snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(snap))
	}
	if snap[0].Type != "info" || snap[0].Message != "hello world" {
		t.Fatalf("bad entry: %+v", snap[0])
	}
	if snap[1].Type != "error" {
		t.Fatalf("bad type: %+v", snap[1])
	}
	if snap[2].Type != "success" {
		t.Fatalf("expected success type, got %+v", snap[2])
	}

	// 从文件恢复历史
	fw2, err := newRotatingFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	store2 := &clientLogStore{clientId: "test", ring: newRingBuffer(logBufferCap), file: fw2}
	defer fw2.Close()
	store2.loadHistoryLocked()
	snap2 := store2.snapshot()
	if len(snap2) != 3 {
		t.Fatalf("expected 3 logs after reload, got %d", len(snap2))
	}
	if snap2[0].Message != "hello world" || snap2[2].Type != "success" {
		t.Fatalf("bad reloaded entries: %+v", snap2)
	}

	// 清空
	if err := store.clear(); err != nil {
		t.Fatal(err)
	}
	if len(store.snapshot()) != 0 {
		t.Fatal("clear failed")
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatal("file not truncated after clear")
	}
}

func TestIsSuccessMessage(t *testing.T) {
	cases := map[string]bool{
		"客户端连接成功":                           true,
		"Successful connection with server": true,
		"connection failed":                 false,
		"启动 NPC 客户端":                        false,
	}
	for msg, want := range cases {
		if got := isSuccessMessage(msg); got != want {
			t.Fatalf("isSuccessMessage(%q) = %v, want %v", msg, got, want)
		}
	}
}
