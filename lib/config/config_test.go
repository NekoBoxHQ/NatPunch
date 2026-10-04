package config

import (
	"ehang.io/nps/lib/file"
	"log"
	"reflect"
	"regexp"
	"testing"
)

func TestReg(t *testing.T) {
	content := `
[common]
server=127.0.0.1:8284
tp=tcp
vkey=123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile=122123
header_user-Agent=122123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile="122123"
header_user-Agent=122123
[tunnel1]
type=udp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
[tunnel2]
type=tcp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
`
	re, err := regexp.Compile(`\[.+?\]`)
	if err != nil {
		t.Fail()
	}
	log.Println(re.FindAllString(content, -1))
}

func TestDealCommon(t *testing.T) {
	s := `server_addr=127.0.0.1:8284
conn_type=tcp
vkey=123`
	// 期望值须与 dealCommon 的构造方式一致（含 Client 实例化）；
	// 比较用 DeepEqual：CommonConfig 含 Client 指针字段，结构体 == 会按指针地址比较（恒不等）
	f := new(CommonConfig)
	f.Server = "127.0.0.1:8284"
	f.Tp = "tcp"
	f.VKey = "123"
	f.Client = file.NewClient("", true, true)
	f.Client.Cnf = new(file.Config)
	if c := dealCommon(s); !reflect.DeepEqual(*c, *f) {
		t.Fail()
	}
}

func TestGetTitleContent(t *testing.T) {
	s := "[common]"
	if getTitleContent(s) != "common" {
		t.Fail()
	}
}
