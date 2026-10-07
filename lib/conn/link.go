package conn

import "time"

type Link struct {
	ConnType     string //连接类型
	Host         string //目标
	Crypt        bool   //加密
	Compress     bool
	LocalProxy   bool
	RemoteAddr   string
	ProtoVersion string
	Cols         int    //shell pty 列数
	Rows         int    //shell pty 行数
	ShellID      string //shell 会话标识（服务端生成，用于 resize 定位 pty）
	Option       Options
}

type Option func(*Options)

type Options struct {
	Timeout time.Duration
}

var defaultTimeOut = time.Second * 5

func NewLink(connType string, host string, crypt bool, compress bool, remoteAddr string, localProxy bool, protoVersion string, opts ...Option) *Link {
	options := newOptions(opts...)

	return &Link{
		RemoteAddr:   remoteAddr,
		ConnType:     connType,
		Host:         host,
		Crypt:        crypt,
		Compress:     compress,
		LocalProxy:   localProxy,
		ProtoVersion: protoVersion,
		Option:       options,
	}
}

func newOptions(opts ...Option) Options {
	opt := Options{
		Timeout: defaultTimeOut,
	}
	for _, o := range opts {
		o(&opt)
	}
	return opt
}
