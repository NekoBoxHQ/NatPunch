# Changelog

本项目为 GPLv3 许可的内网穿透项目，派生关系与上游差异见 NOTICE。

## v26.10.30（已发布）

### 变更

- **手机竖屏下 SSH 终端顶到边框：把外围留白全收掉。**

  终端页原本继承了面板各层的默认留白 —— `#page-wrapper` 15px、Bootstrap 的
  `.col-lg-12` 15px、`.ibox-content` 20px、`.wrapper-content` 15px…… 加起来在
  390px 宽的屏上差不多六七十像素，换算成终端列数就是十几列。

  现在这一页在 `max-width: 768px` 下把这些 padding / margin 全部归零，
  终端盒子圆角也去掉，直接顶到边框；终端高度 58vh → 66vh。
  注意 `.row` 在 Bootstrap 里是负外边距，父容器 padding 归零后必须一起归零，
  否则内容会溢出视口、能横向拖动。

  **这些规则写在 `web/views/terminal/index.html` 自己的 `<style>` 里**，
  只影响这一页，列表 / 表单等页面不受影响。

## v26.10.29（已发布）

### 修复

- **手机竖屏下 SSH 终端整屏折行错乱。**

  `web/views/terminal/index.html` 里有一句故意的「80 列下限」：

  ```js
  fitAddon.fit();
  if (term.cols < 80) { term.resize(80, term.rows); }
  ```

  注释写的是「窄容器下 banner 不折行错乱」，**实际效果正好相反**：手机竖屏时容器
  只放得下 ~45 列，强行按 80 列渲染，于是每一行都溢出再折回来，整屏变成一团
  （开机那张 iStoreOS / OpenWRT banner 尤其明显）。宽屏上容器本来就 ≥80 列，
  所以这个副作用一直没暴露。

  改动：

  - 删掉那行 80 列下限，让 xterm 按容器真实宽度定列数；真实列数经 `sendResize()`
    的 resize 控制帧发给客户端 PTY，`ls` / `top` / `vim` 才会按手机的宽度排版。
  - 窄屏（`max-width: 768px`）下把快捷命令面板改成**浮层**（`position:absolute`
    叠在终端右侧）。它固定宽 192px，在 390px 的屏上直接吃掉一半宽度。
  - 窄屏字号 14 → 12，终端高度由固定 500px 改成 `58vh`。
  - 补 `orientationchange` 监听：部分移动端旋屏只发这个事件，且视口尺寸是异步更新的，
    延 300ms 再 fit。

  **仍然不完美的**：开机那张 ASCII banner 本身是按 80 列画的，手机竖屏横过来也就
  55～60 列，它必然折 —— 除非把终端锁死 80 列再套横向滚动条（那样交互就没法用了）。
  它只在会话开头出现一次，`clear` 就没了。

## v26.10.28（已发布）

### 修复

- **隧道编辑页的「客户端」改成固定的，不再给下拉。**

  隧道编辑是从某个客户端进来的，放开选等于允许把隧道搬到别的客户端去 —— 那是
  另一回事；而且下拉里只有备注名，选错了也未必看得出来。改成「禁用输入框显示备注名
  + 隐藏域照旧提交 `client_id`」，服务端 `Edit` 的归属校验仍旧拿得到它。

  隧道管理 / HTTP代理 / SOCKS代理 三个入口的编辑**共用同一个 `edit.html`**
  （三个控制器都 `display("index/list")`，列表的编辑按钮统一指向 `/index/edit?id=N`，
  而 `IndexController` 只有一个 `Edit` 动作），所以一处改动三个模式都生效。

  **新增页保留下拉**：从「隧道管理 / HTTP代理 / SOCKS代理」这三个入口进来时
  `client_id` 是空的，必须让人选；只有从客户端进去才是预选的。

  顺带清掉编辑页里只为那个下拉存在的 `getClientList()`（`/client/list` 那次 AJAX）
  和一句指向不存在元素的 `$('#use_client').on('change')`。

## v26.10.27（已发布）

### 变更

- **客户端二进制瘦身 3.47MB（−21%）：不再把整个面板编进去。**

  `lib/goroutine` 里那段「IP 白名单授权页」原来是直接 `import web` 读
  `web/static/page/auth.html`；而 `web` 包用 `go:embed` 把 static + views
  （jQuery / bootstrap / echarts / 字体 / 全部视图，约 3.6MB）编进二进制。
  但那段代码**只可能在服务端走到** —— `lib/conn.CopyWaitGroup` 在服务端由
  `server/proxy/base.go` 传 `task`，客户端 `client/client.go` 恒传 `nil`。

  改成**服务端注入**（新增 `server/proxy/authpage.go`，在 `init()` 里给
  `goroutine.AuthPageHTML` 赋值），客户端里那个钩子保持 `nil`。
  linux/amd64 客户端 **16351392 → 12886176 字节**。升级是走隧道传的，这个直接省在升级上。
  新增 `server/proxy/authpage_test.go` 钉住服务端这侧的接线。

- **`web/static/page/languages.xml` 清掉 59 条零引用词条**（221 → 162），另补齐 2 条。

### 修复

- **`error()` 从不生效，`web/views/public/error.html` 一直是死模板。**

  `error()` 只设 `TplName`、不中断请求，4 个调用点后面全都紧跟 `display()` 或
  `AjaxOk` 把它盖掉 —— id 不存在时实际渲染的是 `index/edit.html` / `client/edit.html`，
  一片空字段，看着像"隧道 / 客户端数据全丢了"。GET 分支补 `return`；
  `index.go` 的 POST 分支改成 `AjaxErr`（原来 id 不存在反而回 `"modified success"`）；
  `client.go` 的 POST 分支删掉那句被 `AjaxErr` 完全覆盖的空转。

- **reply 词条大小写错配**：`Thenumberoftunnelsexceedsthelimit` 带大写 `T`，
  而 `language.js` 查表前会 `toLowerCase()` —— 「隧道数量超过限制」一直显示英文原文。

- **补上两条 reply 词条**（`client ID not found` / `task ID not found`）：此前没有对应
  `<lang id>`，`language.js` 查不到就把英文原样丢进弹窗。

### 清理

面板与引擎里**确认没有任何调用路径**的部分：

- 删除 `server/proxy/tcp_natpunchgui.go`、`transport_natpunchgui.go`：
  `natpunchgui` 构建标签没有任何构建会设置（Makefile / CI / 脚本里都没有），
  它们存在的理由是 `client/local.go` 要 `import server/proxy` —— 那个文件早已删除，
  现在 `go list -deps ./client` 里也没有 `server/proxy`。
  `tcp.go` / `transport.go` 上的 `//go:build !natpunchgui` 一并去掉。
- TCP隧道 / UDP隧道 弃用后的残留：`NewMode` 的 `case "tcp"` / `case "udp"`
  （双端隧道内部构造子模式副本时**不走** `NewMode`）、`GetTunnel` 的 tcp/udp 容错、
  `GetDashboardData` 的 `udpCount`（算了但从没被任何页面读）、`caseKeyForTunnelMode`
  的 tcp/udp 分支、编辑页的 `arr["tcp"] / ["udp"]` 与 KNOWN_MODES。
- 其它零引用函数 / 字段：`NewBaseServer`、`FlowAdd`、`FlowAddHost`、
  `SetReadDeadlineBySecond`、`WriteMain / WriteConfig / WriteChan`、
  `BufPool / BufPoolSmall / PutBufPoolCopy / GetBufPoolCopy / PutBufPoolUdp`、
  `NetPackager`、`GetPortByAddr`、`Base64Decoding` + `joinQuickCmd`、`LinkTimeout`、
  `SaveGlobal`、`MkidrDirAll`、`ReturnBucket`、`stopDocker`、`JsonDb.HostsTmp`、
  `connQueue.starving`。
- 全仓 `gofmt`：13 个文件此前不合规（都是 import 分组顺序），现在 `gofmt -l` 输出为空。

### 明确保留（不是死代码）

`lib/natpunch_mux/tc.go` 与 `rate.go` 的 `Rate`（只被 `//go:build integration` 的测试用，
删了等于砍测试）、`lib/config`（`status` 子命令可达）、`lib/install`（`update` 子命令可达）、
`tcpTrans`（客户端配置文件 `mode=tcpTrans` 可为）、`cmd/natpunch-client/sdk.go`
（`natpunchsdk` 构建）、`lib/cache`（HTTP 代理缓存只 `New` / `Get`、从不 `Add`，
实际永远是空的 —— 转发路径早在改写成裸字节透传时就没有解析响应那一步了。
`http_cache` 默认 `false`，不命中的分支不会被求值，运行期零成本。属功能问题不是死代码）。

⚠️ 两个 `staticcheck U1000` 的误报源，改这块时注意：

- **U1000 看不见被 build tag 排除的文件**。它会把 `lib/natpunch_mux/tc.go` 里只被
  `mux_test.go`（`//go:build integration`）调用的 `bandwidth` / `createNetwork` /
  `deleteNetwork` / `runDocker` 报成 unused —— 照删会让 `make integration` 编译不过。
  判据要补一条 `go vet -tags integration ./...`。
- `lib/common/logs.go` 的 `StoreMsg.Destroy` 是 beego `logs.Logger` 的接口方法，
  删了 `*StoreMsg` 就不再实现该接口，编译直接失败。

## v26.10.26（已发布）

### 修复
- **「使用场景」那行还是空的 —— 第三个原因：列表页那个「新增」链接传过来的 `type` 是空串。**

  `index/list.html` 的链接是 `index/add?type={{.type}}&client_id={{.client_id}}`，
  而在列表页上这两个变量没有值 —— 实际渲染出来就是 `index/add?type=&client_id=`。
  空串拼出来的选择器是 `#case`，什么都匹配不到，那行就永远空着。
  （旧代码是 `<select>`，空值会回退到第一个选项，正好是 tcp+udp，所以以前看着正常。）

  **这一行改成服务端决定**：控制器按模式算出词条 id（`case_key`），模板只渲染一个
  `<span id="usecase" langtag="{{.case_key}}">`，前端不再按模式 show/hide。
  这样既不吃"模式值一定能匹配到某个 span"这个前提，也不受模板把 `+` 转义成 `&#43;`
  的影响 —— 前两版分别栽在这两件事上。

  前端保留的部分：值只从隐藏域的 `value` 属性读（浏览器会还原实体）+ `KNOWN_MODES`
  白名单归一化。它决定显示哪些字段、以及提交上去存成什么 Mode。

## v26.10.25（已发布）

### 修复
- **上一版没修好「使用场景」不显示 —— 真正的根因是模板把 `+` 转义成了 `&#43;`。**

  Go 的 `html/template` 会把属性值里的 `+` 转义：隐藏域渲染出来是
  `value="tcp&#43;udp"`。**属性里的实体浏览器会还原成 `+`**，所以光看表单提交没问题；
  但两处 JS 从模板拿值 —— `$("#type").val('{{.t.Mode}}')` —— **`<script>` 里不还原实体**，
  拿到的是字面量 `"tcp&#43;udp"`。接着 `$('#case' + o)` 拼出 `#casetcp&#43;udp`，
  **jQuery 的选择器直接抛异常**，于是 `resetForm()` 从那一行往后全都执行不到 ——
  不光「使用场景」空白，连上一版加的兜底也被一起跳过（这就是"改了还是一样"的原因）。

  改法：
  - **不再从模板往 `<script>` 里塞值**：值只从隐藏域的 `value` 属性读（浏览器已还原实体）。
  - `resetForm()` 里加 `KNOWN_MODES` 白名单，并且把 `&#43;` / 空格都归一化回 `+`；
    认不出来就落回 `tcp+udp`。**不拿任意字符串去拼选择器**，也不会让它变成一条坏 Mode。
  - 服务端 `normalizeTunnelMode()`（v26.10.24 加的）继续兜住查询串里 `+`→空格的解码问题。

## v26.10.24（已发布）

### 修复
- **新增/编辑隧道页的「使用场景」显示不出来，而且模式值有被写坏的风险。**

  根因是**查询串里的 `+` 会被解码成空格**：新增入口的链接是 `?type=tcp+udp`，
  到后端手里已经变成 `"tcp udp"`。以前页面上是个 `<select>`，值对不上任何选项时会
  回退到第一项（正好是 tcp+udp），所以**一直没暴露**；v26.10.19 把下拉换成隐藏域之后
  没有这个回退，于是不仅「使用场景」那行空白，这个值还会随表单提交上去、把隧道的 Mode
  存成 `"tcp udp"` —— 服务端按 mode 起监听时认不出来，**那条隧道等于没建起来**。

  收口在三处：
  - `web/controllers/index.go` 新增 `normalizeTunnelMode()`：**四个**读 `type` 的地方
    （列表筛选 / 新增 GET / 新增 POST / 编辑 POST）全部走它，把空格归一化回 `+`。
  - 两个页面的 `resetForm()` 里同样归一化，并且**把干净的值写回表单**。
  - 认不出来的模式兜底成 `tcp+udp`。这同时修掉另一个隐患：`arr[o]` 原先在模式认不出来时
    是 `undefined`，`arr[o].length` 会抛异常，**整张表单的字段（端口 / 目标 …）都不显示**。

## v26.10.23（已发布）

### 变更
- **HTTP代理 的「使用场景」里端口写的是 8004，改成 8002。**
  三个入口的示例端口现在统一成：**双端隧道 8001 / HTTP代理 8002 / SOCKS代理 8003**。

## v26.10.22（已发布）

### 变更
- **双端隧道的「使用场景」那句跟另外两条对齐。**
  `info-casetcpudp` 原来是「同一端口同时监听 TCP 和 UDP，一条隧道打通两种协议」——
  跟 HTTP代理（「将公网服务器1.1.1.1的8004端口作为HTTP代理，访问内网网站。」）/
  SOCKS代理 那两条不是一个句式。改成：

  > 将公网服务器1.1.1.1的8001端口作为双端隧道，和客户端交互。

  （原来那句里的技术事实没丢：双端隧道就是同一个端口同时监听 TCP 和 UDP，
  见 `server/server.go` 的 `case "tcp+udp"`。）

## v26.10.21（已发布）

### 变更
- **编辑隧道页的「模式」也不再是下拉，改成固定显示。**
  上面那行「使用场景」已经把这条隧道是什么类型写清楚了；隧道是从哪个入口建的就在哪个入口用，
  编辑时不该再让人换类型。下拉换成隐藏域带 `type`（表单是 `$('form').serializeArray()` 提交的，
  少了它保存之后类型会丢），取值和提交逻辑没动。

  顺带把下拉里冒出来的 `TCP隧道` / `UDP隧道` 两个选项消掉了 —— 那两个模式早弃用了：
  **双端隧道本身就是同一个端口同时监听 TCP 和 UDP**
  （`server/server.go` 的 `case "tcp+udp"`；建隧道时也是单条 `createOne(mode, ...)`，
  服务端一次起两个监听）。后端仍然认得 `tcp` / `udp`，老配置不会因为这次改动失效。

## v26.10.20（已发布）

### 变更
- **编辑隧道页的客户端标签 / 下拉跟新增页对齐**：「客户端 ID」→「客户端」，
  下拉从 `1-广东DT` 改成只显示备注名（`option` 取值仍是 Id）。
  上一版只改了新增页，同一个字段两处显示不一样太扎眼。

  编辑页的「模式」下拉**保留**：新增页删掉是因为从哪个入口进来模式就已经定了；
  编辑页那个是在改一条已有隧道的类型，删掉就没法改了。

## v26.10.19（已发布）

### 变更
- **新增隧道 / 新增HTTP代理 / 新增SOCKD代理 三个页面：去掉「模式」下拉。**
  从哪个入口进来，模式就已经定了，再让人选一次是多余的。
  下拉换成隐藏域把 `type` 带上（`$('form').serializeArray()` 提交，少了它建出来的隧道会没有类型），
  取值和提交逻辑一个字没动。
- **同一页面的「客户端 ID」改叫「客户端」**（复用 `word-client` 词条）。
- **客户端下拉只显示备注名**：原来 `1-广东DT`，现在 `广东DT`。
  `option` 的**取值仍是 Id**，只是显示文本换成备注。

## v26.10.18（已发布）

### 变更
- **面板里的「逆向隧道」改叫「双端隧道」**（同一个端口同时吃 TCP 和 UDP，两套协议一个口），
  英文对应 `Reverse` → `Dual-end`。

  改的是显示词条，共 4 处：`scheme-tcpudp`（隧道类型名）、`info-feature1`（登录页那句特性说明）、
  统计图例两处。**取值和逻辑一个字没动。**

  文件是 `web/static/page/languages.xml`。它同时被 `//go:embed static` 编进二进制，
  而 `StaticHTTPFS` 的 `diskFirstFS` 又是**磁盘优先**（先找 `<工作目录>/web/static/`，
  找不到才用 embed 的）—— 也就是说：只往磁盘丢一个文件就能立刻生效、不用发版，
  但那份**会被下一次 `install_server.sh upgrade` 用发布包里的 `web/` 覆盖掉**，
  重装服务端更等于没改。所以还是得发一版把它焊进二进制。
  （对照：`web/views/` 下的模板**没有** disk-first，改模板必须发版，热更无效。）

## v26.10.17（已发布）

### 变更
- **面板「隧道管理」列表里，客户端那一列只显示名字。**
  - 表头：`客户端 ID` → `客户端`（复用已有的 `word-client` 词条，中英都对）。
  - 单元格：原来渲染成 `1-广东DT` 这种 `Id-备注`，现在直接显示备注 `广东DT`。
  - **只改显示**：`field` 仍是 `ClientId`，排序和筛选用的取值一个字没动。
  - 注意 views 是 `go:embed` 进去的（`web/embed.go` 的 `InitBeegoAssets` 明确写了
    "磁盘 web/ 目录不参与渲染"），所以这个改动必须重建二进制才生效。

## v26.10.16（已发布）

### 修复
- **客户端断一次线就永久失联**（服务端把它的「身份」删了）—— 这条最要紧。

  服务端在客户端连接断开时会走 `server.go` 的 `CloseClient` 分支，
  里面有一段：**如果这个客户端是 `NoStore`，就把它从库里删掉**。

  但那条库记录就是客户端的**身份** —— `GetIdByVerifyKey` 正是靠库里这条记录认 vkey 的。
  而验证发生在「客户端上报配置」**之前**，所以条目一没，客户端每次重连都卡在
  `Validation key ... incorrect`，**自己没有任何机会把条目重建回来**。结果就是
  **断一次线就永久失联**，只能跑到服务端手工补一条才恢复 —— 对一个专门管
  「无公网设备」的工具来说，这是最不能有的一种故障。

  `NoStore` 的语义本来就只是「不落盘」，不需要用删除来保证重启后不留垃圾：
  进程一起一落，内存里的临时条目自然就没了。连接断开只是暂时状态，不该毁身份。

  **2026-10-06 在 sg 上真机复现**：clientId 3 一断开就被删，之后一直
  `Validation key 41d6370f... incorrect`，在服务端补回一条正式条目才恢复。

- **自报配置注册进来的客户端不落盘**（同上一条的另一半）。
  `GetConfigInfo()` 会把这条路上来的客户端标成 `NoStore` —— 就算身份不再被删，
  不落盘也意味着**服务端一重启它就没了**，仍然回不来。
  现在这类客户端按普通客户端处理（落盘、出现在面板里）；
  `public_vkey` 那条合成条目不走这条路，仍然是 `NoStore`。

- **`install.sh` 覆盖安装会把承载自己的那条隧道切断**（从隧道进来的 SSH 上重装 = 白做）。

  原来的顺序是 `停止客户端 → 换二进制 → 写配置 → 注册自启 → 启动`，而且整个脚本
  `grep -cE 'setsid|systemd-run|nohup'` 是 **0** —— 没有任何脱离会话的手段。维护者的 SSH
  常常是「服务端 → 客户端隧道 → 目标机」进来的，第一句 `killall` 一执行隧道就断，
  脚本被 SIGHUP 带走，**写配置 / 注册 / 启动一步都执行不到**，重连回去还是旧配置旧版本。
  （真机实测：守护会把客户端拉回来 —— systemd 6 秒、procd 8 秒 —— 所以不会失联，
  但那次安装是白做的。）

  现在：
  - **顺序重排**：换二进制 → 写配置 → 写 unit / init.d + enable + daemon-reload → 才开始动 stop。
    前面几步都不碰运行中的进程：配置只在启动时读一次；`mv -f` 换的是目录项，跑着的进程
    还握着旧 inode。**就算脚本在这儿被带走，新版也已经躺在磁盘上了** —— systemd 用
    `EnvironmentFile`，任何一次重启都会读到新配置。
  - 二进制改成「先写 `$BIN.new.$$` 再 rename」：直接 `cp -f` 覆盖运行中的二进制会
    `ETXTBSY`（文本忙），rename 不受影响 —— 这正是它能被提到停服务之前的原因。
  - **只要客户端在跑就脱离**：把「停旧 + 起新 + 自检 + 回滚」写成
    `/tmp/natpunch_install_run.$$`，交给 `systemd-run --unit=... --collect`（无 systemd 退
    `setsid`，再退 `nohup`）执行，日志 `/tmp/natpunch-install.log`；改完**父进程等收尾脚本
    给结论、并照它的结果设退出码** —— 会话没被切断时结论直接打在终端上，被切断了也不影响
    脚本继续跑完。没有客户端在跑（全新安装）时照旧走前台，输出不变。
  - **这里故意不判断「SSH 是不是从隧道进来的」。** 一开始做的是这套（看 `SSH_CONNECTION`
    的对端是不是本机地址），但那个判断要靠推断 sshd 眼里的对端长什么样，而「SSH 真从隧道
    进来」这件事在测试机上**造不出来** —— 留下的是一个永远验不到的判断，判错的后果还是静默的。
    现在「客户端在跑」就是唯一条件（面板网页终端那条 PTY 也是客户端自己 fork 的，杀客户端
    一样会断），等于把「判错」这个可能整个消掉。

- **起不来自动回滚**。换二进制前留一份旧的、写配置前留一份旧的，自检不过就把两份换回去
  再起一次。NAT 机上「装坏了」等于盒子变黑、只能跑现场，这一步是必须的。

- **procd 的 respawn 不会重读配置** —— OpenWrt 的 instance 也包了一层 `sh -c`。
  原先 `procd_set_param command` 写的是**展开好的 flag**（真机 `ubus call service list`
  里能看到 `-server=... -vkey=... -tls_enable=true -tls_fingerprint=...` 这种字面量），
  而 **procd 的 respawn 是直接 exec 存下来的那份 argv、不会重跑 `start_service`** ——
  改了 `/etc/natpunch.conf` 之后，光让守护把进程拉回来，跑的还是旧 flag。
  这和 `needs_detach` 漏判叠在一起就是个**不收敛**的坑：前台路径在 `killall` 那里被会话
  带走，procd 却拿旧 argv 把进程拉回来 —— 再跑多少次都换不掉 flag。

  改成 `procd_set_param command /bin/sh -c '. /etc/natpunch.conf; exec /usr/bin/natpunch-client ...'`
  —— 单引号让 `$SERVER` / `$VKEY` / `$TLS_FLAG` 保持字面量存进去、由**每次 respawn 现读现展开**；
  `exec` 让 procd 眼里的主进程就是客户端本体（否则 stop / killall 只杀壳，真正的客户端
  活下来被 init 收养，下次启动就多一个同 vkey 的客户端）。和 systemd 那条
  `ExecStart=/bin/sh -c 'exec ...'` 是同一个形态，理由不同。

  真机验证：把配置里的指纹去掉 → `killall natpunch-client` → 让 procd 自己拉起 →
  `/proc/<pid>/cmdline` 里**确实没有指纹了**；加回去再 respawn，指纹也回来了。

- **自检必须验到「连上服务端」这一层 —— 否则它证明不了指纹生效。**
  「进程活着」和「真的连上了」是两件事：指纹不匹配时客户端**进程会稳稳地活着**
  （连不上就每 5 秒重试一次），只看活着会把一台实际上连不上的机器报成"安装成功"。
  现在收尾脚本会等它真正连上：验不到就明确报
  `client is RUNNING but never reached the server` 并让退出码非 0（**不自动回滚** ——
  连不上可能只是这台此刻网络不通，回滚会把"新版本已经装好"这个好消息一起撤掉）。

  这条判据本身在两个坑里各栽过一次，都是真机跑出来的：
  1. **先翻日志。** 只看日志尾部的话，上一轮成功过的那条 `Successful connection` 还在里面 →
     这一轮没连上也被判成功。改成以本轮的 `start vkey:` 为界之后，又在 OpenWrt 上翻车：
     **logread 的缓冲区整块停在几十分钟前**，等于一直在读旧记录。
     最终改成**看 socket**：连上就有一条指向桥端口的 ESTABLISHED，不吃日志也不受缓冲区影响。
  2. **看 socket 也不能采样一次。** 指纹不匹配时 TCP 是能连上的（pin 在握手过程中才失败），
     那一刻 socket 恰好是 ESTABLISHED —— 单次采样可能撞上这几十毫秒的窗口。
     改成 **6 次跨 9 秒、至少 4 次在**才算连上；客户端 5 秒才重试一次，握手失败的基本采不到。
     另外 ss 和 netstat 的列序相反（ss 状态在前、netstat 在后），
     顺手记一笔：别写成 `ESTAB.*:端口`，那在 netstat 上永远匹配不到。

  （顺带确认：**指纹是真的在生效** —— 用错指纹时服务端日志里是
  `connect error remote error: tls: bad certificate`，客户端握手就失败了。）

- **自检不能用 `systemctl is-active` 判活着。** `Restart=always` 的崩溃循环里进程每隔
  `RestartSec` 秒活一下，采样恰好落在那一下就会返回 `active`。真机上验过：一个
  `tls_strict=true` 却没有指纹的坏配置，`NRestarts` 已经涨到 10，自检却报
  "restarted with the new build" —— **在 NAT 机上这个假 OK 等于把黑盒子说成好的**。
  改成**隔 4 秒连采两次 PID，一样才算稳**（崩溃循环里每次采样都是新 PID），
  同一个坏配置随即被正确判出并回滚。

- **回滚备份泄漏**。每份 `natpunch-bin.old.*` 是 15MB。前台路径没人清、回滚成功后也没清
  —— 真机上跑两趟就攒了四份。现在：前台路径归 `install.sh` 的 EXIT 陷阱，脱离路径移交给
  收尾脚本（成功时删，**回滚也失败才留**），另外按**年龄**清历史遗留（不看 PID ——
  文件名里的 PID 是当时那个脚本的 `$$`，按它判断会删掉正在被收尾脚本使用的备份）。

### 测试
- `test/upgrade_logic_test.sh` 第 8 节新增两条：install.sh 的 procd `command` 必须是
  `sh -c '. /etc/natpunch.conf; exec ...'` 形态，且不许再出现写死 argv 的旧形态。
- `test/upgrade_logic_test.sh` 第 11 节新增 15 条断言：顺序（换二进制 / 写配置的行号必须早于
  重启段）、二进制走临时名 + rename（不许裸 `cp` 覆盖运行中的）、注册段不许夹启动动作、
  内嵌脚本 `sh -n` 干净、用位置参数取值（引号 heredoc 会挡展开）、自删同步、
  **不许用 `is-active` 判活着**、两次采样 PID、回滚存在、备份有主。
- 真机验证矩阵（每条都跑过，不是推演）：

  | 分支 | 机器 | 结果 |
  |---|---|---|
  | 前台（无客户端在跑） | sg / systemd | ✓ RC=0，输出完整，无残留 |
  | 脱离 · `systemd-run --collect` | sg / systemd | ✓ 独立单元，`--collect` 收干净 |
  | 脱离 · `setsid` | iStoreOS-DT / procd | ✓ 停+起，自删同步 |
  | 脱离 · `nohup`（把 `setsid` 挪走逼出来） | iStoreOS-DT / procd | ✓ 同上 |
  | 覆盖安装的结论与退出码 | sg + iStoreOS-DT | ✓ 成功 RC=0 / 回滚 RC=0，都打结论 |
  | 坏配置（`tls_strict` 无指纹） | sg / systemd | ✓ 判出崩溃循环 → 回滚 → 重连成功 → 备份收掉 |
  | procd respawn 重读配置 | iStoreOS-DT / procd | ✓ 改配置 + `killall` 后 `/proc/<pid>/cmdline` 跟着变 |

  过程中真机抓到的两个"静态看着都对"的错：自检用 `systemctl is-active` 会误报成功；
  父进程判成功时 grep 的字符串比收尾脚本打印的少了 `client` 一词，成功也 exit 1。
  两条都补了断言。

## v26.10.15（已发布）

### 修复
- **开了 TLS 却没配指纹的「半配置」，两条路径都会喊出来了。**

  v26.10.14 修的是「合成部署命令」那段代码，**救不到已经装好的客户端**：`update` 是保留配置的，
  `TLS_FLAG` 是安装那一刻写进 `/etc/natpunch.conf` 的，之后不会被重写。所以那些在
  v26.10.8 ~ v26.10.14 之间用面板按钮装出来的客户端，会一直带着 `TLS_FLAG='-tls_enable=true'`
  跑下去 —— **不掉线**，但桥接证书永远不被固定，日志一直刷那条警告。

  现在：
  - `install.sh`：TLS_FLAG 里开了 TLS 却没有 `-tls_fingerprint=` → 明确告警，并给出从服务端
    取指纹的命令。不做「没指纹就拒绝」（有人确实只想加密、不固定），但必须喊一声。
  - `uninstall_client.sh update`：**体检存量配置**。这段放在**版本比对之前** —— 受影响的机器
    多半已经是「最新版」，会从「已是最新版本，无需更新」那一步直接 return，放在后面就永远看不到。

### 测试
- `test/upgrade_logic_test.sh` 第 8 节新增三条断言：两个脚本都要有这段体检，且 update 那条必须
  落在版本比对之前。逻辑本身在真机上喂了四种输入验过：开了 TLS 没指纹 → 告警；
  两个都给 / 只给指纹 / 都没开 → 安静。

## v26.10.14（已发布）

### 修复
- **面板「OpenWrt 部署 / Linux 部署」给出的命令仍然不带证书指纹** —— v26.10.8 只修了一半。

  v26.10.8 把指纹写进了命令字符串，但**没有加引号**：

      ... | sh -s -- '<vkey>' <host> <port> -tls_enable=true -tls_fingerprint=<fp>

  而 `install.sh` 取的是 `TLS_FLAG="${4:-}"` —— 带空格的值会被 shell **拆成两个参数**，
  只有 `-tls_enable=true` 落进 `$4`，指纹成了 `$5` 被丢掉。**所以拿这两个按钮装的客户端，
  桥接证书从来没有被固定过**，日志一直照刷 `TLS 已启用但未配置 tls_fingerprint`。

  真机复现（sg.s-ui.com，照面板那条命令原样安装）：`/etc/natpunch.conf` 里
  `TLS_FLAG='-tls_enable=true'`，客户端日志 4 条指纹警告。

  修法：两条部署命令都给 `tlsFlag` 加单引号。

### 测试
- `test/upgrade_logic_test.sh` 第 8 节新增一条断言：**光「命令里带了 `-tls_fingerprint`」不够，
  还必须用单引号括起来**。只查"带没带"会漏掉这个 bug —— v26.10.8 就是这么漏过去的。

## v26.10.13（已发布）

### 修复
- **`lib/natpunch_mux/mux.go` 的无效 `break`（SA4011，隧道核心的 ping goroutine）**

      case <-s.closeChan:
          break          // 只跳出 select，循环会继续

  `break` 只跳出 `select`，循环会继续，拿着已经失效的 `data` 再跑一轮（`UnmarshalText` 失败、
  `latency` 变成一个巨大的垃圾值写进统计）。当前恰好被 `Close()` 的先后顺序掩盖着
  （先 `isClose.Store(true)` 再往 `closeChan` 发信号，下一轮开头就 `break` 了），但那是巧合不是
  约束 —— 顺序一换，`select` 会因 `closeChan` 常驻就绪而**空转烧 CPU**。改成 `return`。

### 清理（多余代码）
删掉一批确认无用的声明（删除前逐个读过上下文）：
- `server/proxy/websocket.go`：`flowIn` / `flowOut` / `once` 三个字段从没被写过，唯一的回写还被
  注释掉了（`//c.once.Do(func(){ c.host.Flow.Add(...) })`）。**注意：websocket 的流量统计本身是正常的**
  —— 它走的是 `goroutine.CopyBuffer(to, from, host.Client.Flow, ...)`，记账在 `lib/goroutine/pool.go`
  的 144~173 行。这三个字段只是早期那套设计的废弃残留，删掉不改变任何行为。

  > 更正：本条最初的说明写成「websocket 隧道的流量统计从来没接上」，**那是错的**。当时只查了这三个
  > 字段有没有被写，没去查这个功能有没有别的实现路径 —— 又是「用局部证据代替全链路」，
  > 和按关键词删 echarts 是同一个毛病。
- `server/proxy/https.go`：`handleHttps`（旧实现，唯一「引用」在被注释掉的旧代码里，现役走
  `handleHttps2` / `cert`）
- `server/proxy/socks5.go`：`maxUDPPacketSize` 常量
- `server/proxy/base.go`：`BaseServer.id` 字段
- `cmd/natpunch-client/main.go`：`-log` flag（help 写着 `stdout|file`，实际从没被读过；文件模式由
  `-log_path` 控制）—— 一个说了不做的 flag 比没有更坏
- `cmd/natpunch/natpunch.go`：`-server` flag（从没被读，安装脚本也不传）

### 明确保留（linter 说 unused，但删了才是 bug）
- `lib/common/pidfile.go` 的 `heldPidFile`：只赋值不读**正是它的作用** —— 让 `*os.File` 保持可达、
  不被 GC 回收（回收会 finalize 掉 fd，`flock` 随之释放，**单实例保护静默失效**）
- `cmd/natpunch/natpunch.go` 的 `confPath`：flag 包需要它注册，否则 `-conf_path=` 会让 `Parse` 报错；
  值由后面的 argv 手工扫描取
- `lib/natpunch_mux` 的 6 条（`TrafficControl` / `createNetwork` / `runDocker` / …）：被
  `mux_test.go`（`//go:build integration`）使用，而那个文件默认不编译，所以分析器看不见

这三类说明：**「unused」清单里有一半是假阳性，而且假阳性都出现在代码故意做某件不显眼的事的地方** ——
和当初按「搜不到关键词」删掉 echarts 是同一个坑。

### 说明
`:golangci.yml` 主动关掉了 `unused` / `gosimple` / `errcheck`（理由写的是「上游存量风格债（百级噪音）」），
所以这些检查从没跑过。本次用 staticcheck 过了一遍：**110 条 → 96 条**，`U1000` 16 → 8、`SA4011` 1 → 0，
剩下 8 条即上面「明确保留」的三类；其余 ~70 条是风格（`S1023` 多余 return、`ST1005` 错误串大小写等）。

## v26.10.12（已发布）

### 修复
- **客户端建连路径上没有超时** —— 链路一丢包就卡住两分多钟，而这正是这个工具要面对的常态。

  客户端连服务端用的是**不带超时**的 `net.Dial` / `tls.Dial`；内核要耗完 SYN 重传才返回，
  Linux 上能卡 **130 秒以上**。而外层重连是

      for { Start(); sleep 5 }

  结构 —— `Start()` 卡在拨号里，整个重连就停摆：不重试、也不理会 `closeClient`。链路抖动下这等于失联。

  **改动**
  - 新增 `connectTimeout = 10s` 与 `bridgeDialer`（同时显式设 `KeepAlive 15s`，让**半开**的连接
    被内核探出来，而不是干等 mux 的 60 秒 `disconnect_timeout`）
  - **明文 / TLS / SOCKS5 / HTTP 代理**四条拨号路径全部收口到它：`tls.Dial` → `tls.DialWithDialer`、
    `net.Dial` → `bridgeDialer.Dial`、`proxy.FromURL` 的 forward dialer 也一并换掉
  - 服务端 bridge 的 **LocalProxy** 分支同样是裸 `net.Dial("tcp", link.Host)`，改用
    `net.DialTimeout` + `link.Option.Timeout`，与「转发到目标」那条路径保持同一口径
  - 10 秒的取舍：跨运营商 / 3G 握手上限通常 1~3 秒，10 秒够用；真连不上时又能较快回到重试

### 清理（多余代码）
- `client/control.go`：删掉 `NewConn` 里 20 行注释掉的 proxyproto 实验代码
- `server/proxy/https.go`：删掉 `HttpsServer.Start()` 里 **54 行被取代的旧实现**（上游那套
  `https_just_proxy` / `httpsListenerMap` / 默认证书选择），功能已由上面 40~89 行的
  SNI → 查 host → 选证书 → `handleHttps2` / `cert` 取代

## v26.10.11（已发布）

### 修复
- **Go 那条 `update` 路径换完二进制后不重启**（`natpunch update` / `natpunch-client update`）。
  这两个子命令是一次性进程：只把磁盘上的二进制换掉就退出，**正在跑的服务内存里还是旧代码**，
  结尾只打印一句「更新成功，请重启服务」。在典型场景下这是致命的 —— 这两个命令经常是从
  「服务端 → 客户端隧道 → 目标机」的 SSH 上敲的，照它说的去重启，客户端一停、承载这次 SSH
  的隧道就断了，等于让你把自己踢下线，服务停在「已停止」再没人拉起来。
  shell 那条路（`uninstall_client.sh update`）早就有「脱离会话 + 看门狗」，Go 这条一直缺。

  现在：替换成功后生成 `/tmp/natpunch_restart.<pid>`，交给一个**脱离当前会话与 cgroup** 的
  进程执行 —— 有 systemd 就用 `systemd-run --unit=... --collect` 起独立单元（本次 SSH 会话随
  隧道断开被清场也带不走它），否则用 setsid。两个设计点：
  - **只在服务本来就是运行中时才重启** —— 有人可能是故意停掉的，换个二进制顺手把它拉起来
    属于改变别人机器的运行状态，不是升级该干的事
  - **拿不到脱离手段时不硬来** —— 原地重启比不重启更糟（会切断调用方会话、服务停在半路）

- **客户端 `udp5` 分支漏了 `return`**，每小时刷 20 多条误导性告警：
  `[W] connect to 127.0.0.1 error dial udp5: unknown network udp5`。
  `handleChan` 是个 if 链（http / udp5 / shellresize / shell → 最后落到 `net.DialTimeout`），
  **只有 udp5 那条没有 return**；`udp5` 是项目自己的哨兵值（UDP over SOCKS5），不是合法的 Go
  网络类型，那一跳必然失败，还会对已经关闭的连接再 Close 一次。真机上 6 小时 21 条，
  且长得极像「连接失败」，排查时会被它带偏。补上 return，与三个兄弟分支对齐。

### 文档
- `server/proxy/transport.go`：给 `tcpTrans` 补上「为什么保留」的说明 —— 面板上建不出这种隧道，
  但客户端 `-local_type=tcpTrans` / 配置文件 `mode=tcpTrans` 依然能建出来，属于「配置 / CLI 可达」，
  与 file / secret / p2p 同一标准。写明这一点，免得下次按「界面里没入口」当死代码删掉。

### 测试
- 新增 `lib/install/restart_test.go`：重启脚本必须 `sh -n` 干净、自删必须**同步**且限定
  `/tmp/natpunch_restart.*` 形态、必须只在服务运行时才重启（含反向断言：不许再出现「请重启服务」老文案）。

### 真机验证
- 重启脚本在 sg 上用 `systemd-run --collect`（与真实路径同机制）跑过两个分支：
  服务没在跑 → 日志「未在运行，不自动重启」+ 同步自删 + 无残留单元；
  服务在跑（拿 cron 当靶子）→ 真重启成功，前后都是 `active`。
- 服务端升级到 v26.10.10 后 6 小时内 **0 条崩溃日志、0 条 `/terminal/ws`**（终端 panic 的闸门守住了）。

## v26.10.10（已发布）

### 修复
- **面板首页（仪表盘）的七张图全成了空白框** —— 负载 / 核心(CPU) / 内存 / 连接数 / 带宽 /
  流量统计 / 连接类型，标题在、图没了（"图形界面没有了"）。真机实测
  `https://<面板>/static/js/echarts.min.js` → **HTTP 404**，而 `style.css` 是 200（203920 字节），
  其它静态资源也都正常，只有这一个 404。

  根因是 `76fe94e`「按面板实际入口清理不可达的页面与接口」里的一条判断写错了：

  > `static/js/echarts.min.js`：面板没有任何图表用它（仪表盘是数字条 + CSS），
  > 却每个页面都在加载，从内嵌资源里去掉。

  `views/index/index.html` 里确实搜不到 "echarts" 字样 —— 但那只说明**关键词搜索判断不出
  这个库有没有被用**：那张模板只负责往
  `chartdatas['load'|'cpu'|'memory'|'connections'|'bandwidth'|'flow'|'counts']` 里塞配置，
  真正的 `echarts.init` 在 `static/js/language.js` 的 `setLang` 里用**一个通用循环**统一调用：

  ```js
  charts[key] = echarts.init(document.getElementById(key));
  charts[key].setOption(chartdatas[key], true);
  ```

  仪表盘上那 7 个 `style="height:300px"` 的 div 就是给它们准备的。
  「搜不到关键词」只是因为这个库被一个通用循环用掉了，不等于没人用。
  而且 `setLang` 是「翻译整页 `langtag`」和「画图表」**共用的同一个函数**，
  异常抛在它中间，页面上其它依赖它的处理会一起断掉 —— 影响面比"少几张图"更大。

  **改动**
  - 恢复 `web/static/js/echarts.min.js`（383139 字节，取自 `76fe94e^`）
  - `layout.html` 加回 `<script src=".../static/js/echarts.min.js?v=...">`，
    并在旁边注明"模板里搜不到 echarts 不等于没人用"，防止再被误删
  - `language.js`：画图前显式判 `typeof echarts`，缺失时跳过并在控制台点名，
    不再让一个缺失的静态资源把整段翻译流程一起带崩；顺带把 `chartdatas[key]` 非 object 时
    会撞上的 `charts[key].setOption`（原文缺花括号，`charts[key]` 还是 undefined）改成跳过

### 测试
- `test/config_keys_test.sh` 新增第 4 节「面板静态资源对账」——**机器核对，不靠关键词猜**：
  - 模板里引用的每个 `/static/...` 路径，文件必须存在（这一条能直接抓住本例）
  - 有模板填 `chartdatas` 时，`layout.html` 必须加载 echarts、文件必须存在、
    `language.js` 必须真的调 `echarts.init`
  - 两种失败形态都实测会红：删掉文件 / 只删 `layout.html` 里那行 `<script>`

## v26.10.9（已发布）

### 修复
- **升级流程会在 `/tmp` 里攒 `natpunch_apply.<pid>`，只增不减**（真机审计发现：sg 上 4 份，
  其中 16:31 / 16:39 / 16:44 是三次**成功**升级各留一份；路由器上另有 2 份老版本留下的
  `natpunch_update.*` 目录 —— 那台 `/tmp` 是 tmpfs，占的是内存）。

  根因：update 的 apply 挂在 `systemd-run --unit=... --collect` 的独立单元里跑，而它的自删
  写成 `( sleep 1; rm -f "$0" ) &` —— 主进程一退，systemd 立刻按 cgroup 清场，那个还在 sleep
  的子 shell 被一起 SIGTERM，`rm` 永远执行不到。同机上的看门狗 GUARD 用的是同步 `rm -f "$0"`，
  一份都没剩，正好是这组对照。已在 sg 上用真实机制做 A/B：同样交给 `systemd-run --collect`，
  旧写法 4 秒后文件还在，EXIT 陷阱写法当场就没了。

  另外自删原先只写在文件末尾，`BIN_SRC`/`TMP_DIR` 无效、写二进制失败、启动失败这三条
  **快速退出路径**都是直接 `exit` 走的，一样会留。

  改法：挂到 EXIT 陷阱的 `cleanup()` —— 清 `TMP_DIR` + 只删 `/tmp/natpunch_apply.*` 形态的
  `$0`（不做无条件 `rm -f "$0"`，免得被别的形态调起来时删错东西）。不挂 `INT/TERM`：
  那两个信号下陷阱跑完 shell 还会继续往下执行，半路把正在读的脚本删掉更危险。

- `do_update` 里顺带扫掉历史遗留的 apply / guard / update 临时物。判据用**年龄**
  （`find -mmin +60`），**不是**「那个 PID 还在不在」：文件名里的 PID 是**主脚本**的 `$$`，
  而 apply / guard 是主脚本 fork 出去、早就换了 PID 的另外两个进程 —— 主脚本一退那个 PID
  就是死的，按它判断会把**正在跑**的看门狗当遗留物删掉，而 `sh` 是边读边执行脚本文件的，
  删了后半段就读不到，那条保险丝会在最需要它的那 90 秒里悄没声地失效。后缀另要求纯数字，
  否则 `natpunch_update.*` 会连本次要写的 `natpunch_update.log` 一起匹配上。
  已在 busybox（路由器）和 GNU find（sg）上实测：老目录被扫掉，新目录和日志都留着。

### 测试
- `test/upgrade_logic_test.sh` 新增第 10 节：APPLY 必须有 EXIT 陷阱、`rm -f "$0"` 只能有一处
  且不带后台子 shell、清扫判据必须是年龄而不是 PID 存活（含反向断言）。

## v26.10.8（已发布）

### 修复
- **面板「OpenWrt 部署 / Linux 部署」按钮没下发证书指纹**（真机上发现：客户端日志 24 小时
  刷了 136 条 `TLS 已启用但未配置 tls_fingerprint：仅防被动窃听，不防中间人`）。
  同一个页面上手工那条「TLS 命令」早就带了 `-tls_fingerprint=`，两个部署按钮一直漏着 ——
  拿它们装的客户端，桥接证书不会被固定。

  补这条时踩到两个连在一起的坑：
  - `install.sh` 原来把第 4 个参数 `TLS_FLAG` 当**单段**校验，带空格的
    `'-tls_enable=true -tls_fingerprint=xxx'` 会被直接 die 掉。现在逐段校验
    （每段必须 `-` 开头、字符集限定 `A-Za-z0-9._=-`），空格作为分隔符本身。
  - **systemd 不按空格拆分变量**：`ExecStart=... ${TLS_FLAG}` 配两段值，会把两个 flag 当成
    **一个参数**传进去（在真机上用一次性 unit 实测确认）。所以 unit 的 ExecStart 改成
    `/bin/sh -c 'exec <客户端> ... ${TLS_FLAG}'` —— `exec` 保证 systemd 的主进程就是客户端
    （配合 `KillMode=process` 才杀得干净，v26.10.5 那条），保留 shell 又保证多 flag 能展开。
    **两者缺一不可**：不 exec 会漏杀客户端；没有 shell 则带指纹的部署命令会被悄悄弄坏。
  - 更新路径的单元迁移同步改成这个形态；替换行改从 heredoc 写的**文件**里读，不再用
    `awk -v` 传值（awk -v 会处理反斜杠转义 —— mawk 不折 `\/` 那次就是这么中的）。
    已在装 mawk 的机器上实测：两种旧形态都能正确迁移、不产生反斜杠、行仍在 `[Service]` 里。
- `test/upgrade_logic_test.sh` 第 8 节改成钉新形态，并新增两条断言：`install.sh` 必须逐段校验
  `TLS_FLAG`、面板部署按钮必须下发 `-tls_fingerprint`。

## v26.10.7（已发布）

### 工程化
- **三个脚本统一用 `pick_highest_version` 挑版本，不再用 `sort -V`**。busybox 的 sort 直到
  1.32 才支持 `-V`，更老的 OpenWrt 上会静默退化成字典序，于是 `v26.9.9` 排在 `v26.9.111`
  后面 —— 「升级」反而降级。`uninstall_client.sh` 早就有这份 busybox 安全的实现，
  `install.sh` / `install_server.sh` 一直没跟上。三个脚本各自独立下发（raw 一行 `wget|sh`）、
  没法共享文件，所以是三份复制 —— 自检里加了一条**比对三份代码体的 md5**，只改一处就会红，
  另加一条禁止任何脚本再出现非注释的 `sort -V`。
- 新增 `web/controllers/terminal_test.go`：把「面板终端 panic 的闸门」钉住 —— 同一个不存在的
  模板名，`EnableRender=true` 必须 panic、`false` 必须安静返回 nil。它验证的是 beego 的
  `Render()` 在闸门关闭时确实不再去渲染（也就不会再 panic），已在本机用真 beego 跑通。
  配套的自检第 9 节再钉「`Ws()` 有没有用这个闸门」（直接从源码里取 `Ws()` 的函数体看）。

## v26.10.6（已发布）

### 修复
- **v26.10.5 的 ExecStart 改写会在 Debian 上写出坏单元**（当天自己踩的，当天修）。
  那条改写用了 awk 正则字面量 + 替换串里写 `\/`：

  ```awk
  /^ExecStart=\/bin\/sh -c /{ print "ExecStart=\/usr\/bin\/..." }
  ```

  GNU awk 把字符串里的 `\/` 折成 `/`，**mawk 不折** —— 它原样写进文件，于是单元变成
  `ExecStart=\/usr\/bin\/natpunch-client ...`，systemd 直接起不来（客户端只能靠
  `uninstall_client.sh` 里"直接后台跑二进制"那条兜底活着，自启与崩溃拉起都没了）。
  实机上就是这么中的：`which awk` → `/usr/bin/mawk`。

  改成 `-v new='...'` 传新行 + `$0 ~ "^ExecStart=/bin/sh -c "` 字符串匹配，
  全程不出现反斜杠，mawk / gawk / busybox awk 行为一致。已在装 mawk 的机器上实测通过。

- **改写失败必须回滚**。原先只打印一句警告就留着 —— 于是"报警告"和"单元已坏"可以同时成立。
  现在校验不过就把刚做的备份 `cp` 回去：宁可维持现状（客户端会漏杀，但不影响启动与连接），
  绝不留一个起不来的单元。这条是直接从上面那次事故里学来的。

## v26.10.5（已发布）

### 修复
- **systemd 客户端 stop / restart 之后会漏杀客户端本体**（真机上抓到，systemd 独有）。
  单元里 `ExecStart=/bin/sh -c '...'` 包了一层，systemd 眼里的「主进程」是那个 shell；
  而同一份单元里为了「stop 时别连带杀掉面板 SSH 场景下的更新子脚本」写了 `KillMode=process`
  —— 它只杀主进程，于是真正的 `natpunch-client` 活下来、被 init 收养（PPID=1），
  下一次启动就多一个**同 vkey 的客户端**，一起连服务端、抢同一条隧道。

  实测现场（sg.s-ui.com，一次 `update` 之后）：

  ```
  2467131  PPID=1        29:18  /usr/bin/natpunch-client ...          ← 上上个，还在连服务端
  2481249  PPID=1        01:15  /bin/sh -c /usr/bin/natpunch-client … ← 单元的 MainPID（只是壳）
  2481251  PPID=2481249         /usr/bin/natpunch-client ...          ← 当前这个
  ```

  systemd 只能记一句 `Unit process <pid> (natpunch-client) remains running after unit stopped.`
  日志里两个 PID 同时打 `Successful connection with server`。

  两处一起改：
  - `install.sh`：`ExecStart` 改为**直接 exec 客户端本体**（不再包 `sh -c`），变量展开交给
    systemd 自己做。这样 `KillMode=process` 杀的就是客户端本身，同时仍然不碰面板 shell。
  - `uninstall_client.sh`：`ensure_killmode_process` 顺带把存量单元里的 `sh -c` 包裹改回直接
    exec（它本来就在改单元，只是以前只补 `KillMode`）；并在 stop 之后补一次
    `kill_client_pids` 清扫 —— 存量机器第一次走新流程时正在跑的还是旧单元，那一次仍会漏杀，
    靠这一句兜住。

  **procd（OpenWrt）没有这个问题**：procd 按进程组清场，实机上路由器始终只有一个客户端。

- `test/upgrade_logic_test.sh` 加第 8 节守住这三条（单元里不能出现 `sh -c` 包裹的 ExecStart、
  必须是直接 exec、更新路径必须能改存量单元）。已验证对修复前的文件全部失败。

## v26.10.4（已发布）

### 修复
- **升级流程从不刷新自启单元**（`install_server.sh`）。`register_autostart` 只在 `install` 分支里被调用，
  `upgrade()` 完全不碰 unit —— 于是**单元文件里的加固改动永远到不了已安装的机器**。
  真机上就是这么积下来的：服务端的 unit 从装完那天（10-01）起就没刷新过，procd 的 `retry` 一直停在
  5、systemd 一直缺 `StartLimitIntervalSec=0` —— 而这两个值正是 v26.10.3 刚改对的。
  现在 `upgrade()` 在 `start` 之前调一次 `register_autostart`
  （必须在 start 之前：它带 `daemon-reload`，写晚了 systemd 还是按旧单元起进程）。
- `test/upgrade_logic_test.sh` 加第 7 节守住这条：`upgrade()` 里必须出现 `register_autostart`。

### 已知未修（留待单独设计）
- **客户端那条链路有同样的问题**：unit 是 `install.sh` 写的，而升级走的是 `uninstall_client.sh`，
  两者互不知道对方写不写单元（后者只引用单元路径做 start/stop）。没在本版一并改，是因为那段代码
  跑在"停掉客户端再拉起来"的分离进程里 —— **单元写坏 = 客户端起不来 = 失联**，
  风险等级和服务端这一行不一样，得单独设计（先备份旧单元 + 起不来就回滚）。

## v26.10.3（已发布）

> 本版是拿两台真机（Debian 12 + systemd 的服务端、iStoreOS + procd 的客户端）逐项比对之后改的。

### 修复
- **面板终端每开一次，服务端就 panic 一次**。beego 在 action 返回后有这么一段判断：

  ```go
  if !context.ResponseWriter.Started && context.Output.Status == 0 {
      if BConfig.WebConfig.AutoRender { execController.Render() }
  }
  ```

  而 WebSocket 的 hijack 不经过 beego 的 `Write` / `WriteHeader` —— `Started` 一直是 false、
  `Status` 也还是 0，于是它认定"这个响应还没写过"，按 `控制器名/动作名` 去渲染
  `views/terminalcontroller/ws.tpl`。那个模板不存在 → `template.go:75` panic →
  被 beego 的 recover 抓住，打一整串 `[C]` 级别堆栈。

  **成功的终端会话结束时也会走一遍这个判断**，所以是"每开一次终端 panic 一次"。
  功能不受影响（连接已经 hijack 走了，用户无感），但日志被灌满 `[C]` 堆栈。
  实测：当天 24 条 `[C]` 全部来自 `/terminal/ws`；把堆栈行拆掉之后，当天**真实的 `[E]` 错误是 0 条** ——
  也就是说这些堆栈一直在掩盖真错误，排查时第一眼就会被它带偏。

  `Ws()` 开头加 `s.EnableRender = false` 统一收口（`Render()` 的首行就是
  `if !c.EnableRender { return nil }`）。原有的两处 `SetStatus(403/400)` 之所以没这个问题，
  是因为 `Output.Status != 0` 让 beego 跳过了渲染 —— 但它们只覆盖 Upgrade 之前的路径，
  Upgrade 之后的三个 `return` 都管不到。

### 加固
- **服务端 procd `respawn` 的 retry 从 5 改成 0**（`install_server.sh`）。retry > 0 时
  procd 崩够次数就**永久放手**；客户端那份（`install.sh`）早就改成 0 了，服务端一直漏着。
  服务端被守护放弃 = **所有客户端同时掉线且不会自愈**，只能人工上机把服务拉起来，
  比客户端那边严重。
- **服务端 systemd 单元补 `StartLimitIntervalSec=0`**（`install_server.sh`）。此前靠
  `RestartSec=3` 恰好不踩默认的 `10s / Burst=5`，但那是巧合不是设计 —— 谁把 `RestartSec`
  调到 2 秒以内就会被 systemd 打成 failed 并永久停止重启。
- **升级看门狗自删**（`uninstall_client.sh`）。`/tmp/natpunch_guard.<pid>` 原先**没有任何地方
  删它**，每升级一次就留一份（真机上已经攒了 3 份）。现在两条退出路径都会删掉自己。

### 工程化
- `test/upgrade_logic_test.sh` 第 4 节从"只查 `install.sh`"扩成**两个脚本都查**，并加了
  反向断言：任何 `retry>0` 的 `respawn` 写法都算失败。上一版就是因为只查客户端，服务端那句
  `procd_set_param respawn 3600 5 5` 才一路漏到真机上、要靠人工看 `ubus` 才发现。
  已验证新断言对修复前的 `install_server.sh` **全部失败**（不是写完就算数的摆设）。

## v26.10.2（已发布）

### 修复
- **面板终端的提示符与电脑 SSH 不一致**。面板起 shell 时用 `os.UserHomeDir()` 取家目录，
  而它读的是**客户端进程**的 `$HOME` —— `natpunch-client` 由 procd / systemd 拉起，
  那里 `HOME` 就是 `/`。于是 shell 以 `/` 为家，而 bash 在 `HOME="/"` 时不做 `~` 替换，
  提示符显示成 `root@host:/#`；同一台机器用电脑 SSH 登进去却是 `root@host:~#`。
  现改为优先查 `/etc/passwd` 里该用户的真实家目录（`CGO_ENABLED=0` 下 `os/user` 走纯 Go
  解析 `/etc/passwd`，OpenWrt 一样可用），明确拒绝 `/`，最后兜底 `/root`。
- **面板终端右键无法复制**（`web/views/terminal/index.html`）。`contextmenu` 处理器无条件
  `e.preventDefault()`，把浏览器自带的右键菜单整个吃掉；而它接下去依赖的
  `navigator.clipboard.readText()` 只在安全上下文（HTTPS / localhost）才存在 ——
  面板通常是 `http://ip:8081`，该 API 根本不存在。
  两者叠加的结果是**右键既不能复制也不能粘贴，等于彻底失效**。
  现改为：有选中内容时放行浏览器菜单（「复制」恢复可用）；只有没选中、且剪贴板 API 确实
  存在时才当右键粘贴。另补 `Ctrl+Shift+C` / `Ctrl+Insert` 复制，走 `execCommand('copy')`
  —— 这是 HTTP 下面板唯一还能用的复制路径（`Ctrl+C` 要留给 SIGINT，不能占用）。

### 工程化
- **面板 shell 的环境不再整体继承守护进程**（新增 `client/shellenv.go`）。面板 shell 此前是
  用 `append(os.Environ(), ...)` 起的，有两个问题：
  - 环境数组里出现重复项时 `getenv` 取的是**第一个**匹配项，直接 append 的注入值会被继承
    来的旧值静默顶掉 —— 也就是说 `HOME` / `PS1` / `TERM` 三行注入**本来就是无效写法**。
  - 客户端是被 procd / systemd 拉起的，那份环境与「电脑 SSH 新登录」完全不是一回事。

  现改为 `HOME` / `PS1` / `TERM` / `HOSTNAME` 四个键先摘掉再追加，其余变量原样保留。
  `HOSTNAME` 用 `os.Hostname()`（内核真名）覆盖：bash 与 busybox ash 都只在 `$HOSTNAME`
  未设置时才用内核名补上，继承来的陈旧值会被原样留着，而任何用 `$HOSTNAME` 拼提示符的
  profile 都会显示它。
  （附一条实测确认的结论：提示符的 `\h` / `\H` 读的是**内核 hostname**，不吃 `$HOSTNAME`
  —— bash 5.x 与 busybox `libbb/lineedit.c` 的 `safe_gethostname()` 皆然。）
- 新增 `client/shellenv_test.go`：4 个单测覆盖上述行为，重点断言环境数组里**不出现重复键**
  以及 `pickHome` 拒绝 `/`。已验证该测试对修复前的实现全部失败（`pickHome(["/"]) = "/"`、
  四个键重复），不是写完就算的摆设。

## v26.10.1（已发布）

### 修复
- **v26.9.112 里服务端不会启用 TLS 桥接（严重）**。重写 `cmd/natpunch/natpunch.go` 时漏掉了
  `bridge.ServerTlsEnable = beego.AppConfig.DefaultBool("tls_enable", false)`，
  于是 `ServerTlsEnable` 恒为 false：
  - 服务端**不会在 `tls_bridge_port` 上建监听**（`bridge/bridge.go` 的 `if ServerTlsEnable`）；
  - 面板的「TLS 一键命令」永远不出现、也不下发证书指纹（`web/controllers/base.go` 的 `useTls`）。

  表现：客户端若在 unit 里带着 `-tls_enable=true`（此前通过面板 TLS 一键命令装的），
  升级服务端后就再也连不上 —— 它一直在敲 8025，而那里没有监听；
  `uninstall_client.sh update` 会保留原配置，所以**升级客户端也修不好**。
  重新走一次面板的安装命令（v26.9.112 的面板会给不带 TLS 的命令）可以立刻绕开。

  已补回该行，并新增配置键对账门禁（见下）防止同类漏读再次发生。
- 清掉 p2p / secret / file 三种已删隧道模式遗留的两处死代码
  （`bridge.SendLinkInfo` 里的 file 模式特判、`GetTaskStatus` 里的 secret 端口分支）。

### 工程化
- 新增 `test/config_keys_test.sh` 并接入 CI：`conf/natpunch.conf` 里生效的每个键、
  以及 `cmd/natpunch/natpunch.go` 里默认配置模板的每个键，代码里都必须真的有人读它；
  关键键（`tls_enable` / `bridge_*` / `log_*` / `web_*`）单独点名。
  这类"漏读一行配置"编译得过、单测与 lint 也全绿，只有真去连才会暴露 —— 本版就是踩了这个。
- `conf/natpunch.conf` 补上 `max_global_conn=0`（默认模板里有、随包配置漏了）。
- README 更正「安装/更新走 `releases/latest/download`」这个已不准确的说法：实际取的是
  **版本号最高**的发布，`/latest` 只在接口不可达时兜底。

## v26.9.112（已发布）

### 变更
- **客户端标识全面统一为 `natpunch-client`**：源码标识、构建目录、服务单元名、面板提示、日志与临时文件名里残留的旧客户端标识全部改名，与服务端 `natpunch` 对称。README / LICENSE / NOTICE 的上游归属声明按要求保持不变。
- **修复：面板给出的客户端启动命令与实际二进制名不符**。面板此前显示 `npc.exe` / `./npc`，而发布包里的可执行文件叫 `natpunch-client`，照抄命令必然「找不到文件」；现改为 `natpunch-client.exe` / `./natpunch-client`。
- **修复：菜单里的「更新客户端」必然失败**。`lib/install` 在更新包中查找的是名为 `npc` 的文件，而发布包内是 `natpunch-client`，因此 100% 报「更新包中未找到可执行文件」。改名后与 `os.Executable()` 自替换、SHA256 校验链路一致。
- **修复文档**：`docs/config-reference.md` 里客户端配置路径写成 `conf/npc.conf`，实际是 `conf/natpunch.conf`。
- 构建标签 `npcgui` / `npcsdk` 改名为 `natpunchgui` / `natpunchsdk`，构建目录 `cmd/npc/` 改为 `cmd/natpunch-client/`（入口文件 `main.go`）。

### 兼容
- 环境变量新增 `NATPUNCH_SERVER_ADDR` / `NATPUNCH_SERVER_VKEY`，同时继续识别旧的 `NPC_SERVER_ADDR` / `NPC_SERVER_VKEY` —— 容器 / 编排里既有变量若被静默丢弃，表现是「服务起来了但连不上」，属于最难排查的一类故障。
- vkey 临时文件改名为 `natpunch-client-vkey.txt`，读取时回退旧名 `npc_vkey.txt`，升级后首次启动不会因读不到而直接退出。
- **自行构建 GUI / SDK 的命令需同步改**：`go build -tags natpunchgui ...`、`go build -tags natpunchsdk ...`（CI 与 Makefile 已更新）。
- 安装脚本里对 OpenWrt `firewall.allow-npc-download` / `/opt/npc_download` 的清理**保持不变**：那是上游安装器留下的历史残留，改名会让清理失效。

### 平台与代码清理（只保留 OpenWrt / Linux）
- **删除全部 Windows 专用代码**：`lib/common/pid_windows.go`、`lib/natpunch_mux/sysGetsock_windows.go`、`server/proxy/transport_windows.go` 三个平台垫片，以及 22 处 `IsWindows()` 分支（日志/安装/临时目录路径、`.exe` 后缀、`splitStr` 的 CRLF 分叉、`tasklist` / `taskkill` 等）。其中 `pid_windows.go` 的 `IsProcessAlive` 恒返回 false，等于 Windows 上单实例保护形同虚设。
- **删除二进制自带的「服务自安装」整套**：`install` / `start` / `stop` / `restart` / `uninstall` / `status` / `reload` / `service` 子命令、两个交互式菜单（客户端 `printSlogan`/`inputCmd`/`systemService`/`systemPro`，服务端 `-server` 管理脚本）、`lib/daemon` 整个包，以及 `github.com/kardianos/service` 依赖（连带 `fatih/color`、`go-colorable`、`go-isatty`）。
  开机自启与守护从来都由 `install.sh` / `install_server.sh` 写的 procd / systemd 单元负责，这层自安装是纯冗余；它还带来一个隐患：二进制自装的服务名与脚本装的单元名不同，两者可能各跑一个实例、抢同一个 vkey。
  保留 `-version`（安装器用来比对版本）、`update`（就地替换二进制）、`register` / `nat` / `status`（诊断）。
- **客户端无参数启动不再进交互菜单**：原来找不到配置文件就打印菜单等 stdin 输入，而服务化部署下没有 stdin，表现是「进程起来了但什么都没干」。现在明确报错并以非零码退出。
- **面板展示的客户端命令改为绝对路径** `/usr/bin/natpunch-client`（`install.sh` 的安装位置）。原来的 `./natpunch-client` 只在当前工作目录恰好是二进制所在目录时才有效，而 SSH 进去默认在 `/root`。
- 合计 **−1499 / +381 行**。

### 安全修复（全面审计后）
- **跨租户越权（严重）**：`server.GetTunnel` 的 `type=tcp+udp` 分支完全没有 `clientId` 归属过滤，非管理员只要把 `type` 传成 `tcp+udp` 就能列出**全部客户端**的 tcp/udp 隧道，而返回体每一行都内嵌完整 `Client`（含明文 `VerifyKey` 与 `WebPassword` 哈希）。已补齐过滤；同时 `IndexController.GetTunnel` 在非管理员拿不到会话 `clientId` 时改为拒绝，不再退化成 0（0 在 `GetTunnel` 里表示「不限客户端」）。
- **跨租户越权（中）**：`tunnelBelongsToMe` 用 Tasks / Hosts 两套彼此独立的自增 id 做「或」判定，两者都从 1 起各自增长、必然重号 —— 自己名下有 `Host #N` 就能操作别人的 `Task #N`。`web/` 里没有任何地方操作 Host，该分支没有正当用途，已改为只查 Tasks。
- **远程 DoS（严重）**：`lib/conn/conn.go` 的 `GetHostInfo` / `GetConfigInfo` / `GetTaskInfo` 在底层读取失败时仍解引用指针（此时为 nil），任何通过 vkey 校验的客户端发一个畸形帧即可打崩服务端进程（调用点在无 `recover` 的 goroutine 里）。已改为检测错误与 nil 并返回，并上报此前被吞掉的 `json.Unmarshal` 错误（畸形配置原被当成「零值客户端」照单全收）。
- **远程 DoS（高）**：客户端对服务端下发的 `RemoteAddr` 直接 `strings.Split(...,":")[1]`，不含冒号时越界 panic —— 该 goroutine 无 `recover`，整个以 root 运行的客户端会退出。改用 `net.SplitHostPort`，解析失败时回退到真实远端地址。
- **存储型 XSS（高）**：客户端列表的 `Version` / `LocalAddr` 由客户端上报、绕过了入库时的 HTML 转义，而 bootstrap-table 默认不转义单元格 → 管理员打开列表页即执行脚本。两列已加显式转义 formatter。
- **验证码可绕过（中）**：登录校验验证码失败时只调用了 `ServeJSON`（它不终止执行）而缺 `return`，凭据正确时仍会建立会话，`open_captcha` 等于没开。
- **服务端 OOM / 任意文件读（中）**：HTTPS 监听按客户端注册 host 时上报的 `CertFilePath` / `KeyFilePath` 做无界读取（指向 `/dev/zero`、`/proc/kcore` 或大文件即可）。新增 `common.ReadCertFile`：只认普通文件、上限 1MB。
- **资源泄漏（中）**：客户端 `handleUdpMonitor` 是永不退出的 `for-select`，而每次重连都会重新 `StartLocalServer` → 每重连一次泄漏一个 goroutine + ticker，新旧实例还会一起抢 `udpConn` / `udpConnStatus`。改为由停止通道收口，`CloseLocalServer`（每次重连前都会调用）统一结束。
- **数据竞争**：SOCKS5 UDP 的 `clientAddr`（改 `atomic.Value`，用户端首包到达前丢弃下行包而非传 nil）；`UdpModeServer.removeSession` 对 `sess.target` 的无同步读（改为与 sweeper 同一口径，经 `ready` 判定）；客户端 `s.signal` / `s.tunnel`（改 `atomic.Pointer`，`handleMain` 持本地引用，消除关闭时的 nil 解引用）；`Client.IpWhiteList` 的并发读写（读侧 `RLock` 取快照，写侧 copy-on-write）；`common.in()` 不再就地排序调用方切片（改线性扫描）。
- **凭据处理**：`vkey`、SOCKS5 账密、配置模式重连 vkey 的比较改用常量时间（新增 `common.ConstantTimeStrEq`）；`IpWhiteAuth` 日志里的完整 vkey 只保留前 4 位；`GetClient` 对非管理员同时抹掉 `IpWhitePass`（此前只抹了 `VerifyKey`）；客户端 vkey 临时文件改用 `O_NOFOLLOW` 打开（`/tmp` 若可被非特权用户写入，预置同名符号链接即可让 root 客户端覆盖任意文件）。
- **更新 / 文件路径加固**：更新路径 4 处 `http.Get` 改用带 60s 超时的 client（原来走 `DefaultClient`，卡住的连接会让更新永久挂起）；`chMod` / `CopyDir` 不再吞掉 `os.Chmod` 与拷贝错误；新建文件先以 0600 落地，目录用 0755 而非 0777。

### 签名链与并发收紧（第二轮）
- **签名校验改为 fail-closed**。原来「拿不到 `SHA256SUMS.minisig` 就告警放行」是整条签名链的降级口子：SHA256 比对的 `SHA256SUMS` 与包来自同一渠道，属于自洽校验 —— 攻击者控制发布渠道时只要不提供签名文件，就能让签名这层形同虚设。现在 `lib/install` 与三个 shell 安装器（`install.sh` / `install_server.sh` / `uninstall_client.sh`）在拿不到可信签名时一律中止。
- **内置校验器哈希不符也改为中止**（原为警告后放行）。校验器与包同源下载，把请求换成乱码即可逼出「跳过签名校验」的降级路径。
- **`uninstall_client.sh` 补上内置静态校验器**。此前它只有系统 `minisign` 一条路，而 OpenWrt 上没有 minisign 包 —— 也就是说**客户端更新路径一直只做了 SHA256**，等于把「用网上下载的二进制覆盖本地 root 二进制」完全托付给 HTTPS。现在与 `install.sh` 同级：校验器哈希必须与脚本内嵌信任锚一致才执行。CI 的信任锚比对同步纳入该脚本，避免以后发版时锚点过期反而把升级卡死。
- **无签名环境留有显式逃生开关** `NATPUNCH_ALLOW_UNSIGNED=1`（默认不启用）。自建发布链等场景可显式放行，日志会明确标注「不防发布渠道被控」。
- **`server/proxy/http.go` keep-alive 换 host 的跨代复用**：转发 goroutine 原来闭包引用外层的 `connClient` / `host` / `isReset`，而这三者在换 host 时都会被重新赋值 —— 旧 goroutine 退出时的 `defer connClient.Close()` 关掉的其实是**新**连接；共享的 `isReset` 也可能被新一代替成 false，导致旧 goroutine 反过来关掉仍在使用的客户端连接。改为每一代把句柄、host、重置标记显式传进 goroutine，重置标记用独立的 `atomic.Bool`。
- 新增回归断言：`test/upgrade_logic_test.sh` 现在会检查三个脚本都走统一的签名失败收口、不存在「跳过签名校验」的放行分支、校验器哈希不符是硬失败、以及逃生开关存在。

### 已知未修（需单独决策）
- TLS 未配置 `tls_fingerprint` 时仍以 `InsecureSkipVerify` 放行（只防被动窃听，不防中间人），需 `tls_strict=true` 才拒绝启动。**这次没有改默认值**：现网客户端是以 `-tls_enable=true` 起的，把默认改成 fail-closed 会让它们升级后直接拒绝启动 —— 正是「不可以无缘无故不启动」要避免的那类故障。要收紧请在客户端显式加 `-tls_strict=true -tls_fingerprint=<服务端桥接证书指纹>`。

### 界面冗余清理（以面板实际入口为准）
逐项核对「面板上真有入口的才算功能」，删掉全部不可达的页面与接口：

- **隧道列表的 8 个无入口筛选页**：`/index/tcp`、`/index/udp`、`/index/file`、`/index/secret`、`/index/p2p`、`/index/host`、`/index/help`，以及无任何前端调用的 `/index/getonetunnel` 与 `/index/reorder`。菜单本来就只挂了 `tcpudp` / `http` / `socks5` 三个；`/index/all` 由客户端列表页的「隧道」按钮进入，保留。
  （注：表头里的 `sortable` 是 bootstrap-table 的列排序，与 reorder 接口无关，全仓库没有任何地方调用它。`server.ReorderTasks` 与 `CheckUserAuth` 里针对 reorder 的守卫分支随之删除。）
- **全局配置页** `GlobalController` + `views/global/index.html`：没有任何入口（`CheckUserAuth` 里非管理员本来就被 deny），连同路由注册一并删除。
- **`AuthController.GetTime`**：没有任何前端引用。
- **`index/list.html` 里 `row.Mode == "p2p"` / `"secret"` 的快令区块**：该模板只被 tcpudp（过滤 tcp+udp/tcp/udp）、http（httpProxy）、socks5 三个页面使用，这两个条件**恒为假**。
- **`static/js/echarts.min.js`**：面板没有任何图表用它（仪表盘是数字条 + CSS），却每个页面都在加载，从内嵌资源里去掉。
- **`conf/natpunch_client.conf`**：无人引用的上游样例配置（里面还留着原作者的本机路径与 IP），且它示范的 `file`/`secret`/`p2p` 模式已不在界面上。

**保留**（有入口或有配置 / CLI 可达）：终端（客户端列表页的 SSH 按钮进入）、注册页（`allow_user_register=true` 时登录页出现入口）、`/index/all`、`/auth/ipwhiteauth`（客户端 IP 白名单 API，不是界面功能）。

**本次未动**：`file` / `secret` / `p2p` 三种隧道模式在服务端与客户端的实现。它们在界面上确实建不出来，但客户端的配置文件与 `-local_type=` 仍能创建（随包的客户端样例里就写着 `mode=file`），属于配置 / CLI 可达，不是「界面弃用」。

### 移除 p2p / secret / file 三种隧道模式
这三种模式的面板上都没有入口，只能靠客户端配置文件或 CLI 触发；服务端的 p2p 打洞协调器在随包配置里本身就是注释掉的（`#p2p_port`），默认从未启动。按「界面没有的就是弃用的」一并删除：

- **服务端**：`server/proxy/p2p.go` 整文件（UDP 打洞协调）、`NewP2PServer` 的启动、`p2p_ip` / `p2p_port` 配置、`SecretChan` 通道及其处理分支、`bridge` 里 secret / file / p2p 三个工作分支、file 模式的隧道分发。
- **客户端**：`client/local.go` 整文件（本地监听 / p2p 打洞 / 本地文件服务）、`handleP2PUdp` 等整套打洞辅助（`client/control.go` 尾部）、`-password` / `-local_type` / `-local_port` / `-target` 四个 CLI 参数及其启动分支、`bridge.Client` 的 file mux 与 `p2pAddr` 字段。
- **协议与配置**：`WORK_SECRET` / `WORK_FILE` / `WORK_P2P*` / `NEW_UDP_CONN` 常量、`conn.Secret` 类型、`config.LocalServer` 结构及配置文件里 `[secret_*]` / `[p2p_*]` 段的解析。

客户端现在只有一条启动路径：`-server` + `-vkey`（`install.sh` 装出来的就是这条），或 `-config` 配置文件模式。

**没有动 `udp5`**：SOCKS5 的 UDP 转发与它共用这个连接类型（`server/proxy/socks5.go` 构造、`client/client.go` 的 `udp5 → handleUdp` 分支），原样保留。UDP 隧道、SOCKS5、HTTP 代理、终端均不受影响。

## v26.9.111（已发布）

### 变更
- **CI 增加 `-race` 门禁**：`check` job 新增 `go test -race ./...`。本项目修复集中于并发路径（pidfile 原子化、sourcepool 池淘汰、p2p 清扫、mux 队列、globalconn 计数），无 `-race` 时数据竞争无法被自动发现。
- **Go 自更新增加 minisign 签名校验**：`lib/install` 用 `go-minisign` + 内嵌公钥校验 `SHA256SUMS.minisig`，与 install.sh / install_server.sh 同策略（有签名强制校验、未提供则告警放行）。此前仅比对未签名的 SHA256SUMS，等价于只信任 HTTPS 传输。
- **修复「从不限速改为限速」不生效**：`UpdateClient` 改为比较旧值决定是否重建 Rate，并停掉被替换对象的 ticker（避免 goroutine 泄漏）。
- **客户端 web 密码统一哈希**：修复 bridge pub 模式注册以明文覆盖已哈希值；`NewClient` / `UpdateClient` 集中处理，`$2` 前缀不二次哈希。
- **并发修复**：`UdpModeServer.sweeper` 改用 `ready` 通道判定会话就绪，消除对 `sess.target` 的无同步读取；`sourcePoolSet` 在 `Close` 后不再被 `Submit` 复活无人清扫的池；`HttpsListener.Close` 排空 `acceptConn` 缓冲，避免最多 8 个已建立连接泄漏。
- **pid 文件改用 flock（Unix）**：锁由内核在进程退出时自动释放，消除陈旧文件与 PID 复用误判；不支持文件锁的平台退回 `O_CREATE|O_EXCL` 路径。
- `quick_cmds.json` 权限由 0644 收紧为 0600，与其余写入口一致。

### 工程化 / CI
- master 推送现在会触发 `check`（vet / test / -race / govulncheck / lint）；`build` / `minisign-check` / `release` 仅由 `v*` 标签触发，避免 master 推送误建 release。
- 新增回归测试：pidfile 四项（存活拒绝 / 陈旧接管 / 空文件保守拒绝 / 并发仅一个成功）、sourcepool 两项（Close 后拒绝提交 / 重复 Close 幂等）。

## v26.9.110（已发布）

### 变更
- **单实例 pid 文件原子化**：以 `O_CREATE|O_EXCL` 主张所有权，消除原「读文件判断存活 → 无条件覆盖」的 TOCTOU 竞态（两个进程可同时通过检查、各持内存副本互写 clients.json）。
- `web/controllers/base.go` fail-closed 加固：`isAdmin` 缺失/非法的会话不再放行动作执行。
- `CheckUserAuth` 对 `index/reorder` 一律拒绝：该动作参数为 `ids`（无 `id`），且会重写全部任务的 Sort，无法按归属校验。

## v26.9.109（已发布）
- CI release 上传加固：softprops 偶发 `other side closed` 中断上传，新增 `gh` 兜底步骤——校验资产数、对缺失项重试补传（v26.9.108 实测踩坑）。

## v26.9.108（已发布）
- 校验器信任锚更新为 CI 实测哈希（此前锚点由本地构建填入，与 CI 产物不符）。

## v26.9.107（已发布）
- 校验器产物改用 `-buildvcs=false -trimpath` 构建，使产物跨 VCS 状态可复现（Go 默认会把 git revision/modified 编进二进制，导致本地与 CI 哈希必然不同、锚点永远对不上）。
- release job 补 `actions/checkout`，使信任锚守卫真正生效（此前缺该步骤，守卫会因读不到脚本而静默走 warning 分支）。
- 守卫把本次产物的锚点值写入 job summary，便于直接粘贴。

## v26.9.106（已发布）
- **校验器信任锚内嵌脚本**：不再用同渠道下载的 SHA256SUMS 校验校验器（镜像被控时可同时替换包 / 清单 / 校验器，构成循环信任）。
- **`max_clients` 配额豁免合成条目**：`GetClientCount` 跳过 `NoStore` 客户端，修复 `max_clients=N` 实际只能注册 N-1 个。
- **p2p 条目硬上限**：`p2pMaxEntries=4096`，防「不同 key 高速发包」绕过 TTL 清扫造成内存增长。

## v26.9.105（已发布）
- 复评「发布前必修 3 项」+ 建议项 + 文档对齐：
  - 安装脚本在发布未提供 `.minisig` 时改为告警放行（原实现因内置公钥非空而恒为真，导致无签名发布的安装全部失败）。
  - 文档与代码对齐：integration 门禁、重置 VKEY 入口、安装器权限实际值、`max_global_conn` 覆盖范围等。

## v26.9.104（已发布）
- **程序级单实例保护**：直接运行服务时若已有实例则退出，根治双进程各自持内存互写 clients.json 导致 vkey 丢失。

## v26.9.103（已发布）
- 双进程互覆盖数据库根因修复——systemd 优先单进程管理。

## v26.9.102（已发布）
- **vkey 不可变防呆**（升级掉线保护）：编辑客户端留空即保持原值、手填 <8 位拒绝、查重；移除面板「重置 VKEY」入口（避免误操作导致批量掉线）。存量 40bit vkey 的手动轮换步骤见 `docs/security-hardening.md`。
- 升级前备份 conf 目录；clients.json 缺失/为空时自动从备份恢复。

## v26.9.101（已发布）
- CI integration job 移除（路径过期 + 默认 skip）；安装菜单空输入优化。

## v26.9.100（已发布）
- 版本发现改为按版本号取最高（`releases/latest` 按发布时间排序，跨分支时可能取到较低版本）。

## v26.9.99（已发布）
- **全仓库 nps/NPS 标识清零**，仅 LICENSE / NOTICE 保留法律声明。
- `lib/crypt` 移除旧版前缀快速命令的解析兼容（存量客户端用面板重新生成命令即可）。

## v26.9.98（已发布）

### 变更
- **README 完全品牌化**：README / README_zh 移除顶部与 License 章节的显著派生声明，发布说明模板同步；上游相关仅保留在 LICENSE / NOTICE（法律声明）。
- **OpenWrt 签名校验落地（静态校验器）**：新增 `cmd/minisign-check`（Go 版 go-minisign 库），CI 随发布物静态交叉编译 4 架构（`minisign-check-linux-<arch>`）；`install.sh` / `install_server.sh` 签名校验升级为三级——系统 minisign → 自动下载内置静态校验器 → 降级 SHA256 兜底。**工具可得但校验失败即终止**，仅工具完全不可得才警告跳过（OpenWrt 无 minisign 软件包场景首次获得完整签名校验能力）。
- CI 新增 shell 语法门禁（`sh -n install.sh install_server.sh`）。

## v26.9.97（已发布）

### 变更
- **项目完全 NatPunch 化**：module path 全面改为 `github.com/NekoBoxHQ/NatPunch`（go.mod + 全部 import）；`cmd` 目录改名 `cmd/natpunch`。
- 清理代码 / 配置 / 界面残留上游标识：Windows 服务名、服务安装/卸载/启停菜单、HTTP 代理 404 页、桥接证书 CN、日志路径与文件名、`/etc` 兼容路径、默认配置模板等。
- GPLv3 合规声明保留于 LICENSE / NOTICE（法律义务），`docs/comparison` 保留同类工具对比。

### 工程化 / 依赖 / 合规 / 文档
- CI 门禁：`check`（vet / go test / govulncheck / golangci-lint）→ `build`（linux amd64/arm64/armv7/mipsle × server/client 共 8 组合，产物架构自检）→ `release`（SHA256SUMS + minisign 签名 + 发布说明带 GPL 声明）。go-version 1.26。mux 集成测试（需 Docker+tc）为本地可选项（`NP_MUX_INTEGRATION=1`），不在 CI 门禁内。
- 依赖升级：x/net v0.59.0、x/crypto v0.57.0、x/text v0.42.0、x/sys v0.48.0、x/time v0.16.0、golang/snappy v1.0.0、ants/v2 v2.12.1。go.mod 升 go 1.26.0。
- kcp-go 保持 v5.4.20+incompatible（v5.6+ 需改 module path 为 /v5，列为后续项；无已知漏洞）。
- govulncheck：代码 0 漏洞，import 包 0 漏洞；残余 1 个 module 级（openpgp，未使用、无修复版，不可达）。
- 构建矩阵补 armv7/mipsle；install_server.sh / install.sh 未知架构显式报错。
- 版本号一致：`lib/version.VERSION` 默认 `(dev)`，发布由 -ldflags 注入 tag。
- 文档体系：docs/（architecture、config-reference、deploy、security-hardening、comparison）、CHANGELOG、SECURITY、CONTRIBUTING、Makefile。
- GPLv3 声明式合规：README 顶部声明派生自上游、LICENSE 补版权行、新增 NOTICE。
- README 措辞："静默管理/免凭据 SSH" 调整为"自动化运维管理/授权终端 + 审计日志"，新增 AUP 段落。
- 终端审计日志：`TERMINAL AUDIT`（操作人 / 时间 / 目标客户端 / 来源 IP）。

## v26.9.96（已发布）

### 变更
- 客户端列表页移除服务端桥接证书指纹展示条（TLS 一键命令仍自动携带指纹，功能不变）。
- install.sh：TLS_FLAG 写入 /etc/natpunch.conf 时整体加单引号——init.d 用 `. /etc/natpunch.conf` source 配置，值含空格时无引号会被拆成多条命令执行（多参数 TLS_FLAG 安装崩溃修复）。
- minisign 签名启用：CI 签名改 apt C 版 minisign（原 go-minisign `@v0.1.0` 子目录版本不存在，是未验证的死代码路径，密钥一配必炸）；发布方公钥内置 `install.sh` / `install_server.sh`（`MINISIGN_PUBKEY` 环境变量可覆盖），目标机有 minisign 工具即强制校验签名。

## 阶段三（commit 5aa65a0）稳定性
- mux 死锁修复（connStatusOkCh/FailCh 有缓冲 + select 超时）；写队列容量上限（默认 4096 chunk，超限断连并打独立日志）。
- 连接数原子 CAS + 全局连接上限 `max_global_conn`（默认 0 = 不限）。
- 客户端重连前显式关闭旧连接；包级全局状态收敛为实例字段。
- 健康检查 map 重建加锁；LRU 缓存加锁重写（Clear 复位）。
- 限速按 burst 分块；UDP/SOCKS5 数据面复查流量上限。
- pprof 强制回环；安装权限收敛（0700/0600/0640）；JSON 库文件 0600。
- daemon kill 弃用 shell 拼接（pid 解析校验 + exec 纯参数直传；Windows 保留 taskkill）。
- 库代码 os.Exit(0) 清除（返回 error；net.ListenTCP nil 陷阱规避）。
- 外部 IP 获取 HTTPS + 缓存；HTTP keep-alive 按建立时 host 记账。
- 读写返回值补齐；日志/接口 DTO 脱敏；listener 未就绪 Close 守卫等补入项。

## 阶段二（commit 9261ad5）安全基线
- 删除零调用方 AES-CBC/PKCS5 死代码；密钥生成改 crypto/rand；VKEY 提升 128bit；基础认证常量时间比较。
- 隧道 TLS 三态指纹（tls_fingerprint / tls_strict）；桥接证书持久化 `conf/bridge.pem|key` 并与面板证书隔离。
- 面板密码 bcrypt 化 + 存量明文在线迁移。
- 会话加固（SameSite=Strict / HTTPS Secure / SessionRegenerateID）。
- 终端权限收紧（管理员/本人客户端）+ WebSocket Origin 白名单 + 终端页存储型 XSS 修复。
- 升级完整性：compareVersion 按段比较；SHA256 强制校验 + minisign 分级签名；弃用 unpackit 改标准库安全解包（防路径逃逸、512MB 上限）。
- IDOR 显式 RBAC；GET=渲染 / POST=变更。

## 阶段一（commit 95bf47d）止血
- 删除 auth_key query 参数认证链路（未授权接管漏洞）；面板认证回归纯会话；isAdmin fail-closed 三路分支。
- 修复 SOCKS5 长度字段 nil 解引用远程崩溃；UDP 报文长度字段上界校验（防越界/OOM）。
- p2p map / connMap.Close 并发加锁；流量限制 nil task 防御。
- UDP/p2p goroutine 池化（按源池 + TTL 淘汰 + 4096 源上限）。
- 注册上限 max_clients / max_tunnels_per_client（默认 0 = 不限）；public_vkey 随机化；UpdateClient Rate goroutine 泄漏修复。
- 安装脚本密码强制输入；发布物 SHA256SUMS。

## 基线（commit 24cb16c）
- 上游全量导入（硬分叉基线）。
