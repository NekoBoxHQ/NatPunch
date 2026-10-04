//go:build npcsdk

// SDK 入口（CGO c-shared 构建）：普通 go build / go vet / go test 不编译本文件，
// 避免与 npc.go 的 main() 冲突（main redeclared，CI 在 CGO_ENABLED=1 下会失败）。
// 构建 SDK 库（需 gcc）：
//   go build -tags npcsdk -buildmode=c-shared -o natpunch_sdk.so ./cmd/npc
package main

import (
	"C"
	"ehang.io/nps/client"
	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/version"
	"github.com/astaxie/beego/logs"
)

var cl *client.TRPClient

//export StartClientByVerifyKey
func StartClientByVerifyKey(serverAddr, verifyKey, connType, proxyUrl *C.char) int {
	_ = logs.SetLogger("store")
	if cl != nil {
		cl.Close()
	}
	cl = client.NewRPClient(C.GoString(serverAddr), C.GoString(verifyKey), C.GoString(connType), C.GoString(proxyUrl), nil, 60)
	cl.Start()
	return 1
}

//export GetClientStatus
func GetClientStatus() int {
	if cl != nil {
		return cl.Status()
	}
	return 0
}

//export CloseClient
func CloseClient() {
	if cl != nil {
		cl.Close()
	}
}

//export Version
func Version() *C.char {
	return C.CString(version.VERSION)
}

//export Logs
func Logs() *C.char {
	return C.CString(common.GetLogMsg())
}

func main() {
	// Need a main function to make CGO compile package as C shared library
}
