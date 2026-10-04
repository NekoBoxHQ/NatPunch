package proxy

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
)

// sameSiteWriter 在会话 Cookie 上补充 SameSite=Strict /（HTTPS 时）Secure（F2-5a）。
// beego 1.12 无 SameSite 配置项，用 ResponseWriter 包装在首次写出时修正 Set-Cookie。
// 保留 Hijacker/Flusher 透传：终端 WebSocket（terminal.go）依赖 Hijack。
type sameSiteWriter struct {
	http.ResponseWriter
	secure   bool
	wrote    bool
	hijacked bool
}

// newSameSiteHandler 包装面板 HTTP handler，为会话 Cookie 加固
func newSameSiteHandler(h http.Handler, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&sameSiteWriter{ResponseWriter: w, secure: secure}, r)
	})
}

func (w *sameSiteWriter) WriteHeader(code int) {
	if w.hijacked {
		// 连接已被 Hijack（如终端 WebSocket），底层 response 已失效：
		// 再写 header 会触发 net/http "WriteHeader on hijacked connection" 日志甚至 panic
		return
	}
	if !w.wrote {
		w.fixCookies()
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sameSiteWriter) Write(b []byte) (int, error) {
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	if !w.wrote {
		w.fixCookies()
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *sameSiteWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}

func (w *sameSiteWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *sameSiteWriter) fixCookies() {
	sc := w.Header()["Set-Cookie"]
	for i, c := range sc {
		if strings.Contains(c, "beegosessionID=") && !strings.Contains(c, "SameSite=") {
			v := c + "; SameSite=Strict"
			if w.secure {
				v += "; Secure"
			}
			w.Header()["Set-Cookie"][i] = v
		}
	}
}
