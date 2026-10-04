package version

// VERSION 默认值：本地/未打 tag 构建时显示 (dev)，避免误报正式版本号。
// 发布时由 release.yml 用 -ldflags -X github.com/NekoBoxHQ/NatPunch/lib/version.VERSION=$TAG 注入实际 tag（阶段四 F4-6）。
var VERSION = "(dev)"

// Compulsory minimum version, Minimum downward compatibility to this version
func GetVersion() string {
	return "0.26.0"
}
