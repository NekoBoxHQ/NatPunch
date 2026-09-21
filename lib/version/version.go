package version

var VERSION = "0.26.38" // 编译时可用 -X 注入构建时间戳，用于静态资源缓存刷新

// Compulsory minimum version, Minimum downward compatibility to this version
func GetVersion() string {
	return "0.26.0"
}
