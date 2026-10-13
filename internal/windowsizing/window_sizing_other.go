//go:build !windows && !darwin

package windowsizing

func getDesktopWorkArea() (DesktopWorkArea, bool) {
	return DesktopWorkArea{}, false
}
