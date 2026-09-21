package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// startTray 初始化系统托盘（使用 Wails v3 内置托盘，三平台统一）
func (a *App) startTray() {
	tray := a.app.SystemTray.New()

	// 使用 PNG 图标（wails3 各平台均支持）
	if len(trayIcon) > 0 {
		tray.SetIcon(trayIcon)
	}
	tray.SetTooltip("NPS 客户端")

	menu := a.app.NewMenu()
	menu.Add("显示").OnClick(func(_ *application.Context) {
		a.showMainWindow()
	})
	menu.Add("退出").OnClick(func(_ *application.Context) {
		if !isQuitting() {
			setQuitting()
			a.app.Quit()
		}
	})
	tray.SetMenu(menu)

	// 左键单击托盘图标也显示主窗口
	tray.OnClick(func() {
		a.showMainWindow()
	})

	a.tray = tray
}

// showMainWindow 显示主窗口
func (a *App) showMainWindow() {
	if isQuitting() || a.window == nil {
		return
	}
	a.app.Event.Emit("tray-show")
	a.window.Show()
	a.window.Focus()
}
