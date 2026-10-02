package version

var VERSION = "v26.9.4" // 默认版本号；发布时由 release.yml 用 -ldflags -X 注入实际 tag（如 v26.9.4）

// Compulsory minimum version, Minimum downward compatibility to this version
func GetVersion() string {
	return "0.26.0"
}
