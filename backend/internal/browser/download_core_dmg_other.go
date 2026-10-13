//go:build !darwin

package browser

import "fmt"

const dmgCoreSupported = false

func extractDmgCore(archivePath, dest string, progressCb func(int, string)) error {
	return fmt.Errorf("DMG 镜像仅支持在 macOS 上安装")
}
