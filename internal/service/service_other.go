//go:build !windows

package service

func installWindowsRegistryAutorun(_, _, _ string) error {
	return nil
}

func uninstallWindowsRegistryAutorun() error {
	return nil
}

func ensureWindowsSpooler() error {
	return nil
}
