package controllers

import (
	"github.com/astaxie/beego/cache"
	"github.com/astaxie/beego/utils/captcha"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/file"
	"ehang.io/nps/lib/version"
	"ehang.io/nps/server"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

type LoginController struct {
	beego.Controller
}

var ipRecord sync.Map
var cpt *captcha.Captcha

type record struct {
	hasLoginFailTimes int
	lastLoginTime     time.Time
}

func init() {
	// use beego cache system store the captcha data
	store := cache.NewMemoryCache()
	cpt = captcha.NewWithFilter("/captcha/", store)
}

func (self *LoginController) Index() {
	// Try login implicitly, will succeed if it's configured as no-auth(empty username&password).
	webBaseUrl := beego.AppConfig.String("web_base_url")
	if self.doLogin("", "", false) {
		self.Redirect(webBaseUrl+"/index/index", 302)
	}
	self.Data["web_base_url"] = webBaseUrl
	self.Data["register_allow"], _ = beego.AppConfig.Bool("allow_user_register")
	self.Data["captcha_open"], _ = beego.AppConfig.Bool("open_captcha")
	self.Data["version"] = version.VERSION
	self.TplName = "login/index.html"
}

func (self *LoginController) Verify() {
	username := self.GetString("username")
	password := self.GetString("password")
	captchaOpen, _ := beego.AppConfig.Bool("open_captcha")
	if captchaOpen {
		if !cpt.VerifyReq(self.Ctx.Request) {
			self.Data["json"] = map[string]interface{}{"status": 0, "msg": "the verification code is wrong, please get it again and try again"}
			self.ServeJSON()
		}
	}
	if self.doLogin(username, password, true) {
		self.Data["json"] = map[string]interface{}{"status": 1, "msg": "login success"}
	} else {
		self.Data["json"] = map[string]interface{}{"status": 0, "msg": "username or password incorrect"}
	}
	self.ServeJSON()
}

func (self *LoginController) doLogin(username, password string, explicit bool) bool {
	startIprecordCleaner()
	ip, _, _ := net.SplitHostPort(self.Ctx.Request.RemoteAddr)
	if v, ok := ipRecord.Load(ip); ok {
		vv := v.(*record)
		if (time.Now().Unix() - vv.lastLoginTime.Unix()) >= 60 {
			vv.hasLoginFailTimes = 0
		}
		if vv.hasLoginFailTimes >= 10 {
			return false
		}
	}
	var auth bool
	if common.VerifyPassword(beego.AppConfig.String("web_password"), password) && username == beego.AppConfig.String("web_username") {
		self.SetSession("isAdmin", true)
		self.DelSession("clientId")
		self.DelSession("username")
		auth = true
		// 存量明文管理员密码：首次成功登录后原地迁移为 bcrypt（F2-3）
		migrateAdminPasswordToBcrypt(password)
		server.Bridge.Register.Store(common.GetIpByAddr(self.Ctx.Input.IP()), time.Now().Add(time.Hour*time.Duration(2)))
	}
	b, err := beego.AppConfig.Bool("allow_user_login")
	if err == nil && b && !auth {
		file.GetDb().JsonDb.Clients.Range(func(key, value interface{}) bool {
			v := value.(*file.Client)
			if !v.Status || v.NoDisplay {
				return true
			}
			if v.WebUserName == "" && v.WebPassword == "" {
				if username != "user" || v.VerifyKey != password {
					return true
				} else {
					auth = true
				}
			}
			if !auth && common.VerifyPassword(v.WebPassword, password) && v.WebUserName == username {
				auth = true
				// 存量明文客户端密码：首次成功登录后原地迁移为 bcrypt（F2-3）
				if !strings.HasPrefix(v.WebPassword, "$2") {
					if h, err := common.HashPassword(password); err == nil {
						v.WebPassword = h
						file.GetDb().JsonDb.Clients.Store(v.Id, v)
						file.GetDb().JsonDb.StoreClientsToJsonFile()
					}
				}
			}
			if auth {
				self.SetSession("isAdmin", false)
				self.SetSession("clientId", v.Id)
				self.SetSession("username", v.WebUserName)
				return false
			}
			return true
		})
	}
	if auth {
		// 会话固定防护：登录成功后重建会话 ID（F2-5a），数据由 beego 迁移保留
		self.SessionRegenerateID()
		self.SetSession("auth", true)
		ipRecord.Delete(ip)
		return true

	}
	if v, load := ipRecord.LoadOrStore(ip, &record{hasLoginFailTimes: 1, lastLoginTime: time.Now()}); load && explicit {
		vv := v.(*record)
		vv.lastLoginTime = time.Now()
		vv.hasLoginFailTimes += 1
		ipRecord.Store(ip, vv)
	}
	return false
}
func (self *LoginController) Register() {
	if self.Ctx.Request.Method == "GET" {
		self.Data["web_base_url"] = beego.AppConfig.String("web_base_url")
		self.Data["version"] = version.VERSION
		self.TplName = "login/register.html"
	} else {
		if b, err := beego.AppConfig.Bool("allow_user_register"); err != nil || !b {
			self.Data["json"] = map[string]interface{}{"status": 0, "msg": "register is not allow"}
			self.ServeJSON()
			return
		}
		if self.GetString("username") == "" || self.GetString("password") == "" || self.GetString("username") == beego.AppConfig.String("web_username") {
			self.Data["json"] = map[string]interface{}{"status": 0, "msg": "please check your input"}
			self.ServeJSON()
			return
		}
		t := &file.Client{
			Id:          int(file.GetDb().JsonDb.GetClientId()),
			Status:      true,
			Cnf:         &file.Config{},
			WebUserName: self.GetString("username"),
			WebPassword: self.GetString("password"),
			Flow:        &file.Flow{},
		}
		if err := file.GetDb().NewClient(t); err != nil {
			self.Data["json"] = map[string]interface{}{"status": 0, "msg": err.Error()}
		} else {
			self.Data["json"] = map[string]interface{}{"status": 1, "msg": "register success"}
		}
		self.ServeJSON()
	}
}

func (self *LoginController) Out() {
	self.SetSession("auth", false)
	self.Redirect(beego.AppConfig.String("web_base_url")+"/login/index", 302)
}

var ipCleanerOnce sync.Once

// migrateAdminPasswordToBcrypt 把 natpunch.conf 中的明文 web_password 原地替换为 bcrypt（F2-3）。
// 已为 bcrypt 或替换失败时静默跳过（登录不受影响）。
func migrateAdminPasswordToBcrypt(plain string) {
	cfg := beego.AppConfig.String("web_password")
	if strings.HasPrefix(cfg, "$2") {
		return
	}
	confFile := filepath.Join(common.GetRunPath(), "conf", "natpunch.conf")
	data, err := os.ReadFile(confFile)
	if err != nil {
		logs.Warn("migrate admin password: read conf error %v", err)
		return
	}
	h, err := common.HashPassword(plain)
	if err != nil {
		logs.Warn("migrate admin password: hash error %v", err)
		return
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "web_password=") {
			lines[i] = "web_password=" + h
			replaced = true
			break
		}
	}
	if !replaced {
		return
	}
	if err := os.WriteFile(confFile, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		logs.Warn("migrate admin password: write conf error %v", err)
		return
	}
	logs.Info("管理员面板密码已迁移为 bcrypt（natpunch.conf）")
}

// startIprecordCleaner 确定性周期清理登录 IP 记录表（F2-1）。
// 旧实现是 math/rand 概率触发（rand.Seed 已废弃且不可预测），改为每分钟一次。
func startIprecordCleaner() {
	ipCleanerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				ipRecord.Range(func(key, value interface{}) bool {
					v := value.(*record)
					if time.Now().Unix()-v.lastLoginTime.Unix() >= 60 {
						ipRecord.Delete(key)
					}
					return true
				})
			}
		}()
	})
}
