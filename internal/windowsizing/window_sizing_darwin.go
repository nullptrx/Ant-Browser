//go:build darwin

package windowsizing

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

// getDesktopWorkArea 返回主显示器尺寸（单位为点，Retina 下即系统“看起来像”的分辨率）。
// 菜单栏占用的高度由 AppKit 在显示窗口时自动约束，这里只需保证窗口不超出屏幕。
func getDesktopWorkArea() (DesktopWorkArea, bool) {
	bounds := C.CGDisplayBounds(C.CGMainDisplayID())
	width := int(bounds.size.width)
	height := int(bounds.size.height)
	if width <= 0 || height <= 0 {
		return DesktopWorkArea{}, false
	}
	return DesktopWorkArea{Width: width, Height: height}, true
}
