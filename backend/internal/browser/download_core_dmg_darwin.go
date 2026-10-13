//go:build darwin

package browser

import (
	"debug/macho"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

const dmgCoreSupported = true

// extractDmgCore 挂载 DMG 镜像，把其中的 .app 拷贝到 dest 后卸载镜像。
func extractDmgCore(archivePath, dest string, progressCb func(int, string)) error {
	report := func(progress int, msg string) {
		if progressCb != nil {
			progressCb(progress, msg)
		}
	}

	mountPoint, err := os.MkdirTemp("", "ant-core-dmg-*")
	if err != nil {
		return fmt.Errorf("创建挂载目录失败: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	report(5, "正在挂载 DMG 镜像...")
	attach := exec.Command("hdiutil", "attach", archivePath, "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mountPoint)
	// 带许可协议的镜像会在 stdin 等待确认，这里自动应答。
	attach.Stdin = strings.NewReader("Y\n")
	if output, err := attach.CombinedOutput(); err != nil {
		return fmt.Errorf("挂载 DMG 失败: %v %s", err, strings.TrimSpace(string(output)))
	}
	defer func() {
		if err := exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run(); err != nil {
			_ = exec.Command("hdiutil", "detach", mountPoint, "-force", "-quiet").Run()
		}
	}()

	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return fmt.Errorf("读取 DMG 内容失败: %w", err)
	}
	var apps []string
	for _, entry := range entries {
		// 跳过镜像里常见的 Applications 快捷方式等符号链接。
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			apps = append(apps, entry.Name())
		}
	}
	if len(apps) == 0 {
		return fmt.Errorf("DMG 中未找到 .app 应用")
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	for i, name := range apps {
		report(10+80*i/len(apps), "正在拷贝 "+name+"...")
		// ditto 会保留符号链接、扩展属性和代码签名。
		if output, err := exec.Command("ditto", filepath.Join(mountPoint, name), filepath.Join(dest, name)).CombinedOutput(); err != nil {
			return fmt.Errorf("拷贝 %s 失败: %v %s", name, err, strings.TrimSpace(string(output)))
		}
		if err := ensureAppSupportsCurrentArch(filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	report(100, "DMG 内容拷贝完成")
	return nil
}

// ensureAppSupportsCurrentArch 校验 .app 主程序包含当前 CPU 架构，DMG 文件名通常不标注架构。
func ensureAppSupportsCurrentArch(appPath string) error {
	want := map[string]macho.Cpu{"arm64": macho.CpuArm64, "amd64": macho.CpuAmd64}[goruntime.GOARCH]
	entries, err := os.ReadDir(filepath.Join(appPath, "Contents", "MacOS"))
	if err != nil {
		return fmt.Errorf("读取 %s 可执行文件失败: %w", filepath.Base(appPath), err)
	}
	var found []string
	for _, entry := range entries {
		cpus := machoCpus(filepath.Join(appPath, "Contents", "MacOS", entry.Name()))
		for _, cpu := range cpus {
			if cpu == want {
				return nil
			}
			found = append(found, strings.ToLower(strings.TrimPrefix(cpu.String(), "Cpu")))
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("%s 仅支持 %s 架构，与当前设备 %s 不匹配", filepath.Base(appPath), strings.Join(found, "/"), goruntime.GOARCH)
}

func machoCpus(path string) []macho.Cpu {
	if fat, err := macho.OpenFat(path); err == nil {
		defer fat.Close()
		cpus := make([]macho.Cpu, 0, len(fat.Arches))
		for _, arch := range fat.Arches {
			cpus = append(cpus, arch.Cpu)
		}
		return cpus
	}
	if file, err := macho.Open(path); err == nil {
		defer file.Close()
		return []macho.Cpu{file.Cpu}
	}
	return nil
}
