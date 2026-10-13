package backend

import (
	"ant-chrome/backend/internal/logger"
	"strings"
)

type managedLaunchArgSpec struct {
	prefix     string
	takesValue bool
}

var managedLaunchArgSpecs = []managedLaunchArgSpec{
	{prefix: "--user-data-dir", takesValue: true},
	{prefix: "--remote-debugging-port", takesValue: true},
	{prefix: "--remote-debugging-address", takesValue: true},
	{prefix: "--remote-debugging-pipe", takesValue: false},
	{prefix: "--proxy-server", takesValue: true},
	{prefix: "--load-extension", takesValue: true},
	{prefix: "--disable-extensions-except", takesValue: true},
	{prefix: "--restore-last-session", takesValue: false},
}

// blockedLaunchArgSwitches 会让 Chromium 以任意命令启动子进程，或放开网页对 CDP 的访问；
// 启动参数可能来自 Launch API、导入的实例或备份，一律丢弃。
var blockedLaunchArgSwitches = map[string]struct{}{
	"renderer-cmd-prefix":     {},
	"utility-cmd-prefix":      {},
	"zygote-cmd-prefix":       {},
	"nacl-loader-cmd-prefix":  {},
	"gpu-launcher":            {},
	"ppapi-plugin-launcher":   {},
	"plugin-launcher":         {},
	"browser-subprocess-path": {},
	"nacl-gdb":                {},
	"remote-allow-origins":    {},
}

// launchArgSwitchName 按 Chromium 的解析规则提取开关名：接受 --、- 和 / 前缀，忽略大小写。
func launchArgSwitchName(arg string) string {
	name := strings.TrimSpace(arg)
	switch {
	case strings.HasPrefix(name, "--"):
		name = name[2:]
	case strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"):
		name = name[1:]
	default:
		return ""
	}
	if index := strings.IndexByte(name, '='); index >= 0 {
		name = name[:index]
	}
	return strings.ToLower(name)
}

func isBlockedLaunchArg(arg string) bool {
	_, blocked := blockedLaunchArgSwitches[launchArgSwitchName(arg)]
	return blocked
}

func sanitizeManagedLaunchArgs(args []string) ([]string, []string) {
	if len(args) == 0 {
		return nil, nil
	}

	sanitized := make([]string, 0, len(args))
	removed := make([]string, 0, 4)

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}

		if isBlockedLaunchArg(arg) {
			removed = appendUniqueString(removed, "--"+launchArgSwitchName(arg))
			continue
		}

		spec, matched := matchManagedLaunchArg(arg)
		if !matched {
			sanitized = append(sanitized, arg)
			continue
		}

		removed = appendUniqueString(removed, spec.prefix)
		if spec.takesValue && !strings.Contains(arg, "=") && i+1 < len(args) {
			next := strings.TrimSpace(args[i+1])
			if next != "" && !strings.HasPrefix(next, "-") {
				i++
			}
		}
	}

	return sanitized, removed
}

func matchManagedLaunchArg(arg string) (managedLaunchArgSpec, bool) {
	for _, spec := range managedLaunchArgSpecs {
		if strings.EqualFold(arg, spec.prefix) || strings.HasPrefix(strings.ToLower(arg), strings.ToLower(spec.prefix)+"=") {
			return spec, true
		}
	}
	return managedLaunchArgSpec{}, false
}

func logManagedLaunchArgOverrides(log *logger.Logger, profileId string, source string, managedArgs []string) {
	if log == nil || len(managedArgs) == 0 {
		return
	}
	log.Warn("忽略由系统接管或存在安全风险的浏览器启动参数",
		logger.F("profile_id", profileId),
		logger.F("source", source),
		logger.F("managed_args", managedArgs),
	)
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if strings.EqualFold(item, value) {
			return items
		}
	}
	return append(items, value)
}
