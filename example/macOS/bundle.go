package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateVMBundle creates macOS VM bundle path if not exists.
func CreateVMBundle() error {
	return os.MkdirAll(GetVMBundlePath(), 0777)
}

// GetVMBundlePath gets macOS VM bundle path.
func GetVMBundlePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err) //
	}
	return filepath.Join(home, "/VM.bundle/")
}

// GetAuxiliaryStoragePath gets a path for auxiliary storage.
func GetAuxiliaryStoragePath() string {
	return filepath.Join(GetVMBundlePath(), "AuxiliaryStorage")
}

// GetDiskImagePath gets a path for disk image.
func GetDiskImagePath() string {
	return filepath.Join(GetVMBundlePath(), "Disk.img")
}

// GetHardwareModelPath gets a path for hardware model.
func GetHardwareModelPath() string {
	return filepath.Join(GetVMBundlePath(), "HardwareModel")
}

// GetMachineIdentifierPath gets a path for machine identifier.
func GetMachineIdentifierPath() string {
	return filepath.Join(GetVMBundlePath(), "MachineIdentifier")
}

// GetVersionedRestorePath returns a path for restore images organized by macOS version
func GetVersionedRestorePath(version string) string {
	// Use default if no version is provided
	if version == "" {
		version = "unknown"
	}
	
	// First ensure the base Restore directory exists
	baseRestorePath := filepath.Join(GetVMBundlePath(), "Restore")
	if err := os.MkdirAll(baseRestorePath, 0777); err != nil {
		// Fall back to default path if base directory creation fails
		return filepath.Join(GetVMBundlePath(), "RestoreImage.ipsw")
	}
	
	// Create the restore image file name: RestoreImage-macOS-15.3.1-24D70.ipsw
	fileName := fmt.Sprintf("RestoreImage-macOS-%s.ipsw", version)
	
	return filepath.Join(baseRestorePath, fileName)
}

// ListAvailableRestoreVersions returns a list of macOS versions with restore images
func ListAvailableRestoreVersions() []string {
	baseRestorePath := filepath.Join(GetVMBundlePath(), "Restore")
	
	// If the Restore directory doesn't exist, return empty list
	if _, err := os.Stat(baseRestorePath); os.IsNotExist(err) {
		return []string{}
	}
	
	// Read the directory entries
	entries, err := os.ReadDir(baseRestorePath)
	if err != nil {
		return []string{}
	}
	
	var versions []string
	
	// Look for files matching the pattern RestoreImage-macOS-x.y.z-build.ipsw
	prefix := "RestoreImage-macOS-"
	suffix := ".ipsw"
	
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		
		// Extract version from filename
		version := strings.TrimPrefix(name, prefix)
		version = strings.TrimSuffix(version, suffix)
		
		versions = append(versions, version)
	}
	
	return versions
}

// GetRestoreImagePath gets a path for restore image file.
func GetRestoreImagePath() string {
	// Return the path to the symlink inside the Restore directory
	baseRestorePath := filepath.Join(GetVMBundlePath(), "Restore")
	
	// Ensure the directory exists
	if err := os.MkdirAll(baseRestorePath, 0777); err != nil {
		// Fall back to bundle root if directory creation fails
		return filepath.Join(GetVMBundlePath(), "RestoreImage.ipsw")
	}
	
	return filepath.Join(baseRestorePath, "RestoreImage.ipsw")
}

// CreateFileAndWriteTo creates a new file and write data to it.
func CreateFileAndWriteTo(data []byte, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file %q: %w", path, err)
	}
	defer f.Close()

	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}
	return nil
}
