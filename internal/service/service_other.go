//go:build !windows

package service

func installWindowsRegistryAutorun(exe string) error {
	return nil
}

func uninstallWindowsRegistryAutorun() error {
	return nil
}
