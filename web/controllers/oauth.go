package controllers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

// GitHub OAuth 登录（仅管理员）
//
// 面板原有的两条登录路径（natpunch.conf 里的单管理员账号、clients.json 里的客户端账号）
// 都只认本地口令，且都是"先验证本地凭据、再写会话"。GitHub 登录是**新增的第三条入口**，
// 不改动前两条的任何行为：只有 GitHub 用户名命中 github_admin_login 白名单的账号才放行，
// 且只能拿到管理员会话（不做客户端账号映射，客户端账号仍走本地口令）。
//
// 下面四个键**缺任意一个即视为未启用**（fail-closed）。宁可登录按钮不出现，
// 也不要出现"配置不全却把陌生 GitHub 账号当管理员放进来"的降级路径。
//
//	github_oauth_enable = true
//	github_client_id    = <OAuth App 的 Client ID>
//	github_client_secret= <OAuth App 的 Client Secret>
//	github_admin_login  = <允许登录的 GitHub 用户名，逗号分隔>
const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserAPIURL   = "https://api.github.com/user"
	// GitHub API 要求带 User-Agent，缺了直接 403
	githubUserAgent = "NatPunch"
	// 上游响应体读取上限，避免异常上游把内存吃光
	githubMaxBodyBytes = 64 * 1024
)

// state 放**独立的 Lax Cookie**，而不是会话里 —— 这是本功能最容易踩死的地方：
//
// 面板的会话 Cookie 被 F2-5a 加固成 SameSite=Strict（server/proxy/samesite.go 的
// fixCookies），而 GitHub 回调是**从 github.com 跨站跳回来的顶层导航**。Strict 的
// Cookie 在这种请求里浏览器根本不发送 —— 存进会话的 state，回调时读出来永远是空串，
// 于是每次登录都停在"登录校验失败"，且没有任何报错线索。
//
// Lax 恰好允许顶层 GET 导航带上 Cookie（回调收得到），又不像 None 那样对跨站子请求开口。
// 安全性不因此打折：state 是 128bit 随机数、只写给发起登录的那个浏览器，攻击者构造的
// URL 里只能放他自己那份 state，对不上，CSRF / 授权码注入的防线依旧成立。
const (
	githubStateCookieName = "natpunch_oauth_state"
	githubStateCookieTTL  = 600 // 秒，够走完一次授权往返
)

// 外部 HTTP 调用必须有超时：否则回调请求会一直挂在 GitHub 上，
// 白占着 goroutine 与文件描述符。
var githubHTTPClient = &http.Client{Timeout: 10 * time.Second}

type githubUser struct {
	Id    int64  `json:"id"`
	Login string `json:"login"`
}

// githubOAuthConf 读取并校验 GitHub 登录配置。
// enabled 为 false 时其余返回值无意义；adminLogins 已统一小写、去空白。
func githubOAuthConf() (enabled bool, clientId, clientSecret, callbackURL string, adminLogins []string) {
	if b, err := beego.AppConfig.Bool("github_oauth_enable"); err != nil || !b {
		return false, "", "", "", nil
	}
	clientId = strings.TrimSpace(beego.AppConfig.String("github_client_id"))
	clientSecret = strings.TrimSpace(beego.AppConfig.String("github_client_secret"))
	callbackURL = strings.TrimSpace(beego.AppConfig.String("github_callback_url"))
	for _, v := range strings.FieldsFunc(beego.AppConfig.String("github_admin_login"), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			adminLogins = append(adminLogins, v)
		}
	}
	if clientId == "" || clientSecret == "" || len(adminLogins) == 0 {
		warnGithubOAuthIncomplete()
		return false, "", "", "", nil
	}
	return true, clientId, clientSecret, callbackURL, adminLogins
}

var githubOAuthWarnOnce sync.Once

// warnGithubOAuthIncomplete 开关打开了但没配全时留一条日志。
// 这种情况的表现是"登录按钮不出现、功能静默失效"，光看面板根本查不出原因，
// 所以必须给个线索；sync.Once 保证只吼一次，不会刷爆日志。
func warnGithubOAuthIncomplete() {
	githubOAuthWarnOnce.Do(func() {
		logs.Warn("GitHub OAuth 开关已打开（github_oauth_enable=true），但 github_client_id / github_client_secret / github_admin_login 有未填项，该功能不会生效")
	})
}

// githubRedirectURI 回调地址：优先取配置项；留空时按当前请求推导。
//
// 推导结果**必须与 GitHub OAuth App 里登记的 Authorization callback URL 完全一致**
// （scheme / host / path 一个字符都不能差），否则 GitHub 在授权页就直接报错。
// 这里只影响"我们告诉 GitHub 往哪跳"，攻击者伪造 Host 只会让 GitHub 校验失败，
// 不构成开放重定向。
func (self *LoginController) githubRedirectURI(configured string) string {
	if configured != "" {
		return configured
	}
	base := strings.TrimSuffix(beego.AppConfig.String("web_base_url"), "/")
	return self.githubRequestScheme() + "://" + self.Ctx.Request.Host + base + "/login/github/callback"
}

// githubRequestScheme 面板对外的协议。
//
// 先看 web_open_ssl：面板自身就在跑 HTTPS 时协议已然确定，不该让请求头（客户端可控）
// 把它降级成 http —— 那会让 state Cookie 丢掉 Secure 标记。
// 再回退到 X-Forwarded-Proto：反代终结 TLS、面板自身不开 TLS 的场景靠它。
func (self *LoginController) githubRequestScheme() string {
	if beego.AppConfig.String("web_open_ssl") == "true" {
		return "https"
	}
	if proto := self.Ctx.Request.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}

// githubSetStateCookie 下发一次性 state（HttpOnly + SameSite=Lax；HTTPS 下加 Secure）。
func (self *LoginController) githubSetStateCookie(state string) {
	http.SetCookie(self.Ctx.ResponseWriter, &http.Cookie{
		Name:  githubStateCookieName,
		Value: state,
		// Path 取 "/"：子路径部署（web_base_url）下也必须能送到 /login/github/callback，
		// 路径算错的表现是"登录永远提示校验失败"，不值得为省这点作用域冒风险
		Path:     "/",
		MaxAge:   githubStateCookieTTL,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   self.githubRequestScheme() == "https",
	})
}

// githubClearStateCookie 清掉 state Cookie（保证单次有效，重放拿不到第二次）。
func (self *LoginController) githubClearStateCookie() {
	http.SetCookie(self.Ctx.ResponseWriter, &http.Cookie{
		Name:     githubStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   self.githubRequestScheme() == "https",
	})
}

func (self *LoginController) githubStateFromRequest() string {
	c, err := self.Ctx.Request.Cookie(githubStateCookieName)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// GithubLogin GET /login/github：生成 state 并跳转到 GitHub 授权页。
func (self *LoginController) GithubLogin() {
	webBaseUrl := beego.AppConfig.String("web_base_url")
	enabled, clientId, _, configuredCallback, _ := githubOAuthConf()
	if !enabled {
		// 未启用（或配置不全）：不给出任何差异提示，径直回登录页
		self.Redirect(webBaseUrl+"/login/index", 302)
		return
	}
	state, err := githubRandomState()
	if err != nil {
		logs.Error("GitHub 登录：生成 state 失败 %v", err)
		self.failGithubLogin("init")
		return
	}
	// state 存进一次性 Cookie，回调时比对（单次有效，回调里取完即清）
	self.githubSetStateCookie(state)

	redirectURI := self.githubRedirectURI(configuredCallback)
	// 把推导出的回调地址打进日志：登记 OAuth App 时直接照抄，省去猜的功夫
	logs.Info("GitHub 登录：跳转授权页，客户端 IP [%s]，回调地址 [%s]", self.loginIP(), redirectURI)

	q := url.Values{}
	q.Set("client_id", clientId)
	q.Set("redirect_uri", redirectURI)
	// read:user 只够读公开资料；邮箱/私有资料一律不要
	q.Set("scope", "read:user")
	q.Set("state", state)
	q.Set("allow_signup", "false")
	self.Redirect(githubAuthorizeURL+"?"+q.Encode(), 302)
}

// GithubCallback GET /login/github/callback：校验 state → code 换 token → 读用户 → 白名单放行。
func (self *LoginController) GithubCallback() {
	webBaseUrl := beego.AppConfig.String("web_base_url")
	enabled, clientId, clientSecret, configuredCallback, adminLogins := githubOAuthConf()
	if !enabled {
		self.Redirect(webBaseUrl+"/login/index", 302)
		return
	}
	ip := self.loginIP()
	if self.loginBlocked(ip) {
		logs.Warn("GitHub 登录：该 IP 失败次数过多，拒绝，客户端 IP [%s]", ip)
		self.failGithubLogin("rate")
		return
	}

	// state 单次有效：取出即清，重放拿不到第二次
	want := self.githubStateFromRequest()
	self.githubClearStateCookie()
	got := self.GetString("state")
	if want == "" || got == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		self.recordLoginFailure(ip, true)
		logs.Warn("GitHub 登录：state 校验失败，客户端 IP [%s]", ip)
		self.failGithubLogin("state")
		return
	}

	if e := self.GetString("error"); e != "" {
		// 用户在授权页点了取消，算正常流程，不计失败次数
		logs.Info("GitHub 登录：授权未完成（%s），客户端 IP [%s]", e, ip)
		self.failGithubLogin("denied")
		return
	}
	code := self.GetString("code")
	if code == "" {
		self.recordLoginFailure(ip, true)
		logs.Warn("GitHub 登录：回调缺少 code，客户端 IP [%s]", ip)
		self.failGithubLogin("nocode")
		return
	}

	token, err := githubAccessToken(clientId, clientSecret, code, self.githubRedirectURI(configuredCallback))
	if err != nil {
		// 不打印 code / token / 响应体（可能含凭据）
		self.recordLoginFailure(ip, true)
		logs.Error("GitHub 登录：换取 access_token 失败 %v，客户端 IP [%s]", err, ip)
		self.failGithubLogin("token")
		return
	}
	user, err := githubFetchUser(token)
	if err != nil {
		self.recordLoginFailure(ip, true)
		logs.Error("GitHub 登录：读取用户信息失败 %v，客户端 IP [%s]", err, ip)
		self.failGithubLogin("user")
		return
	}

	if !githubIsAdmin(user.Login, adminLogins) {
		self.recordLoginFailure(ip, true)
		logs.Warn("GitHub 登录：账号 [%s] (id %d) 不在管理员白名单，拒绝，客户端 IP [%s]", user.Login, user.Id, ip)
		self.failGithubLogin("notadmin")
		return
	}

	// 与账号密码登录共用同一套会话写入（含 F2-5a 会话固定防护），
	// 避免两条登录路径将来出现权限差异
	self.establishAdminSession()
	self.finishLogin(ip)
	logs.Info("GitHub 登录成功：账号 [%s] (id %d)，客户端 IP [%s]", user.Login, user.Id, ip)
	self.Redirect(webBaseUrl+"/index/index", 302)
}

// failGithubLogin 带**错误码**回登录页，文案由 Index 按码查表（githubOAuthErrorText）得出。
//
// 为什么不直接把文案塞进 URL：那样任何人都能构造
// https://<面板>/login/index?oauth_error=<任意文本>，在**真登录页**上弹出他想要的文字
// （比如"账号已停用，请联系管理员"）。纯文本、不进 DOM、无 XSS，但一个能在真域名登录页上
// 弹自定义提示的入口就是社会工程载荷 —— 改成错误码后，表里没有的码一律丢弃。
func (self *LoginController) failGithubLogin(code string) {
	self.Redirect(beego.AppConfig.String("web_base_url")+"/login/index?oauth_error="+url.QueryEscape(code), 302)
}

// githubOAuthErrorText 错误码 → 固定文案。只认这张表，其余（含空串）一律返回空串。
func githubOAuthErrorText(code string) string {
	switch code {
	case "rate":
		return "尝试次数过多，请稍后再试"
	case "init":
		return "登录初始化失败，请重试"
	case "state":
		return "登录校验失败，请重新发起登录"
	case "denied":
		return "已取消 GitHub 授权"
	case "nocode":
		return "GitHub 未返回授权码"
	case "token":
		return "GitHub 授权校验失败，请重新登录"
	case "user":
		return "读取 GitHub 账号信息失败，请重新登录"
	case "notadmin":
		return "该 GitHub 账号没有管理员权限"
	}
	return ""
}

// githubAccessToken 用授权码换 access_token。
// Accept: application/json 是必须的 —— 不带该头部时 GitHub 返回 form-encoded 文本。
func githubAccessToken(clientId, clientSecret, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientId)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequest(http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubMaxBodyBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", errors.New("token endpoint: invalid json")
	}
	if tr.AccessToken == "" {
		if tr.Error == "" {
			tr.Error = "empty access_token"
		}
		return "", errors.New(tr.Error)
	}
	return tr.AccessToken, nil
}

// githubFetchUser 读取授权账号。以 **id** 为准（数字、稳定、不可改），
// 不依赖 login（改名即失效、可能被抢注），也不碰 email（可能为空或未验证）。
func githubFetchUser(token string) (githubUser, error) {
	var u githubUser
	req, err := http.NewRequest(http.MethodGet, githubUserAPIURL, nil)
	if err != nil {
		return u, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubMaxBodyBytes))
	if err != nil {
		return u, err
	}
	if resp.StatusCode != http.StatusOK {
		return u, fmt.Errorf("user api status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return u, errors.New("user api: invalid json")
	}
	if u.Id == 0 || u.Login == "" {
		return u, errors.New("user api: empty id/login")
	}
	return u, nil
}

// githubIsAdmin GitHub 用户名是否在白名单内（不区分大小写）。
func githubIsAdmin(login string, adminLogins []string) bool {
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" {
		return false
	}
	for _, a := range adminLogins {
		if a == login {
			return true
		}
	}
	return false
}

// githubRandomState 生成 128bit 随机 state（crypto/rand，非 math/rand）。
func githubRandomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
