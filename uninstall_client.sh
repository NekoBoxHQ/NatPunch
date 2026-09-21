echo "==> 开始卸载 NatPunch 客户端 ..."
# 精确杀客户端进程（带 -vkey= 参数），不碰服务端
CLIENT_PIDS=$(ps w 2>/dev/null | grep -v grep | grep 'natpunch -server=' | grep ' -vkey=' | awk '{print $1}')
[ -n "$CLIENT_PIDS" ] && kill $CLIENT_PIDS 2>/dev/null; sleep 1
[ -n "$CLIENT_PIDS" ] && kill -9 $CLIENT_PIDS 2>/dev/null
# 只删客户端自启脚本，不 stop（避免停服务端）
if [ -f /etc/init.d/natpunch ]; then rm -f /etc/init.d/natpunch; rm -f /etc/rc.d/*natpunch* /etc/rc*.d/*natpunch* 2>/dev/null; fi
if command -v systemctl >/dev/null 2>&1; then rm -f /etc/systemd/system/natpunch.service /lib/systemd/system/natpunch.service; systemctl daemon-reload 2>/dev/null; fi
if command -v service >/dev/null 2>&1 && [ -d /usr/local/etc/rc.d ]; then service natpunch stop 2>/dev/null; rm -f /usr/local/etc/rc.d/natpunch; sysrc -x natpunch_enable 2>/dev/null; fi
if [ -d /Library/LaunchDaemons ]; then launchctl bootout system /Library/LaunchDaemons/com.natpunch.client.plist 2>/dev/null; launchctl unload /Library/LaunchDaemons/com.natpunch.client.plist 2>/dev/null; rm -f /Library/LaunchDaemons/com.natpunch.client.plist; fi
if [ -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist" ]; then launchctl unload "$HOME/Library/LaunchAgents/com.natpunch.client.plist" 2>/dev/null; rm -f "$HOME/Library/LaunchAgents/com.natpunch.client.plist"; fi
if [ -f /etc/rc.local ]; then sed -i '/natpunch -server=/d; /\/usr\/bin\/natpunch/d; /\/usr\/local\/bin\/natpunch/d' /etc/rc.local 2>/dev/null; fi
# 只删客户端配置，二进制如果服务端在用就保留
rm -f /etc/natpunch.conf /etc/natpunch/natpunch.conf /usr/local/etc/natpunch.conf
if [ ! -f /opt/natpunch/natpunch ]; then
    rm -f /usr/bin/natpunch /usr/local/bin/natpunch
fi
rm -rf /etc/natpunch /usr/local/etc/natpunch
rm -f /tmp/natpunch /tmp/nps-install.sh /tmp/natpunch.log /var/log/natpunch.log
if ps w 2>/dev/null | grep -v grep | grep -E '/usr/bin/natpunch|/usr/local/bin/natpunch'; then echo "==> 警告：仍检测到 natpunch 进程，请手动检查"; pgrep -af natpunch 2>/dev/null; else echo "==> NatPunch 客户端已完全卸载"; fi
