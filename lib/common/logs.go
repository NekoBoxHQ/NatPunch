package common

import (
	"github.com/astaxie/beego/logs"
	"time"
)

const MaxMsgLen = 5000

var logMsgs string

func init() {
	logs.Register("store", func() logs.Logger {
		return new(StoreMsg)
	})
}

func GetLogMsg() string {
	return logMsgs
}

type StoreMsg struct {
}

func (lg *StoreMsg) Init(config string) error {
	return nil
}

func (lg *StoreMsg) WriteMsg(when time.Time, msg string, level int) error {
	m := when.Format("2006-01-02 15:04:05") + " " + msg + "\r\n"
	if len(logMsgs) > MaxMsgLen {
		start := MaxMsgLen - len(m)
		if start <= 0 {
			start = MaxMsgLen
		}
		logMsgs = logMsgs[start:]
	}
	logMsgs += m
	return nil
}

// Destroy 是 beego logs.Logger 接口要求的方法，本实现无资源要释放，留空即可。
// （不是死代码 —— 删了 *StoreMsg 就不再实现 logs.Logger，编译直接失败。）
func (lg *StoreMsg) Destroy() {
	return
}

func (lg *StoreMsg) Flush() {
	return
}
