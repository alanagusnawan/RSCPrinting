//go:build windows

package service

import "testing"

func TestRegistryCommand(t *testing.T) {
	got := registryCommand(`C:\app\print-agent.exe`, `C:\repo\configs\config.yaml`, `C:\repo\data`)
	want := `"C:\app\print-agent.exe" serve --config "C:\repo\configs\config.yaml" --data-dir "C:\repo\data" --hidden`
	if got != want {
		t.Fatalf("registryCommand = %q, want %q", got, want)
	}
}

func TestRegistryCommandMinimal(t *testing.T) {
	got := registryCommand(`C:\app\print-agent.exe`, "", "")
	want := `"C:\app\print-agent.exe" serve --hidden`
	if got != want {
		t.Fatalf("registryCommand = %q, want %q", got, want)
	}
}
