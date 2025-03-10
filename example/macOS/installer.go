package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Code-Hex/vz/v3"
)

func installMacOS(ctx context.Context) error {
	if err := CreateVMBundle(); err != nil {
		return fmt.Errorf("failed to create VM.bundle in home directory: %w", err)
	}

	// Check if we already have restore images
	versions := ListAvailableRestoreVersions()
	if len(versions) > 0 {
		log.Println("Found existing restore images:")
		for i, ver := range versions {
			log.Printf(" %d. %s", i+1, ver)
		}
	}

	// Determine which image to use based on whether a specific version was requested
	var imagePath string
	var needsDownload bool

	// Check for the main RestoreImage.ipsw symlink first
	mainRestorePath := GetRestoreImagePath()
	log.Printf("Checking for main restore image: %s", mainRestorePath)

	// If installVersion is specified, try to find that specific version
	if installVersion != "" {
		log.Printf("Looking for specific macOS version: %s", installVersion)

		// Look through available versions for a match
		foundMatch := false
		for _, version := range versions {
			if strings.HasPrefix(version, installVersion) {
				versionedPath := GetVersionedRestorePath(version)
				if _, err := os.Stat(versionedPath); err == nil {
					log.Printf("Found matching version: %s", version)
					imagePath = versionedPath
					foundMatch = true
					break
				}
			}
		}

		if !foundMatch {
			log.Printf("Requested version %s not found, will download", installVersion)
			needsDownload = true
			// We'll download to a temporary path then rename
			tempPath := filepath.Join(filepath.Dir(mainRestorePath), "downloading.ipsw")
			imagePath = tempPath
		}
	} else {
		// No specific version requested, check for symlink first
		if _, err := os.Stat(mainRestorePath); err == nil {
			// Main symlink exists, use that
			resolvedPath, err := filepath.EvalSymlinks(mainRestorePath)
			if err == nil && resolvedPath != mainRestorePath {
				// Valid symlink
				imagePath = mainRestorePath
				log.Println("Using existing main restore image")
			} else {
				// Symlink is broken or not a symlink
				log.Println("Restore image symlink is invalid, will download latest version")
				needsDownload = true
				tempPath := filepath.Join(filepath.Dir(mainRestorePath), "downloading.ipsw")
				imagePath = tempPath
			}
		} else if len(versions) > 0 {
			// No valid symlink, but we have versioned images - use the first one (most recent)
			versionedPath := GetVersionedRestorePath(versions[0])
			imagePath = versionedPath
			log.Printf("Using most recent restore image version: %s", versions[0])

			// Create a symlink to this version
			createSymlinkToVersion(versionedPath)
		} else {
			// No images at all, need to download latest
			log.Println("No restore images found, will download latest version")
			needsDownload = true
			tempPath := filepath.Join(filepath.Dir(mainRestorePath), "downloading.ipsw")
			imagePath = tempPath
		}
	}

	// Download the restore image if needed
	if needsDownload {
		log.Println("Starting download process...")
		baseRestorePath := filepath.Join(GetVMBundlePath(), "Restore")
		// Download to a temporary file first (need to download before we can determine version)
		tempPath := filepath.Join(baseRestorePath, "downloading.ipsw")
		if err := downloadRestoreImage(ctx, tempPath); err != nil {
			return fmt.Errorf("failed to download restore image: %w", err)
		}

		// Get version information from the downloaded file
		versionStr, err := getVersionStringFromRestoreImage(tempPath)
		if err != nil {
			return fmt.Errorf("failed to determine version of downloaded image: %w", err)
		}

		log.Printf("Downloaded macOS version: %s", versionStr)

		// Get the final versioned path
		versionedPath := GetVersionedRestorePath(versionStr)

		// Move the file to its versioned location
		if err := os.Rename(tempPath, versionedPath); err != nil {
			log.Printf("Warning: Could not move downloaded image to %s: %v", versionedPath, err)
			// Continue using the downloaded path
			imagePath = tempPath
		} else {
			log.Printf("Moved restore image to: %s", versionedPath)
			imagePath = versionedPath

			// Create a symlink to this version
			createSymlinkToVersion(versionedPath)
		}
	}

	// Resolve any symlinks in the path
	resolvedPath, err := filepath.EvalSymlinks(imagePath)
	if err != nil {
		return fmt.Errorf("failed to resolve restore image path: %w", err)
	}

	// Verify the file exists
	if _, err := os.Stat(resolvedPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("restore image not found at path: %s", resolvedPath)
		}
		return fmt.Errorf("error accessing restore image: %w", err)
	}

	// Load the restore image
	log.Println("Loading restore image from:", resolvedPath)
	restoreImage, err := vz.LoadMacOSRestoreImageFromPath(resolvedPath)
	if err != nil {
		return fmt.Errorf("failed to load restore image from path %q: %w", resolvedPath, err)
	}
	configurationRequirements := restoreImage.MostFeaturefulSupportedConfiguration()
	config, err := setupVirtualMachineWithMacOSConfigurationRequirements(
		configurationRequirements,
	)
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}
	vm, err := vz.NewVirtualMachine(config)
	if err != nil {
		return err
	}

	installer, err := vz.NewMacOSInstaller(vm, resolvedPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				fmt.Println("install has been cancelled")
				return
			case <-installer.Done():
				fmt.Println("install has been completed")
				return
			case <-ticker.C:
				fmt.Printf("install: %.3f%%\r", installer.FractionCompleted()*100)
			}
		}
	}()

	return installer.Install(ctx)
}

func setupVirtualMachineWithMacOSConfigurationRequirements(macOSConfiguration *vz.MacOSConfigurationRequirements) (*vz.VirtualMachineConfiguration, error) {
	platformConfig, err := createMacInstallerPlatformConfiguration(macOSConfiguration)
	if err != nil {
		return nil, fmt.Errorf("failed to create mac platform config: %w", err)
	}
	return setupVMConfiguration(platformConfig)
}

func createMacInstallerPlatformConfiguration(macOSConfiguration *vz.MacOSConfigurationRequirements) (*vz.MacPlatformConfiguration, error) {
	hardwareModel := macOSConfiguration.HardwareModel()
	if err := CreateFileAndWriteTo(
		hardwareModel.DataRepresentation(),
		GetHardwareModelPath(),
	); err != nil {
		return nil, fmt.Errorf("failed to write hardware model data: %w", err)
	}

	machineIdentifier, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return nil, err
	}
	if err := CreateFileAndWriteTo(
		machineIdentifier.DataRepresentation(),
		GetMachineIdentifierPath(),
	); err != nil {
		return nil, fmt.Errorf("failed to write machine identifier data: %w", err)
	}

	auxiliaryStorage, err := vz.NewMacAuxiliaryStorage(
		GetAuxiliaryStoragePath(),
		vz.WithCreatingMacAuxiliaryStorage(hardwareModel),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a new mac auxiliary storage: %w", err)
	}
	return vz.NewMacPlatformConfiguration(
		vz.WithMacAuxiliaryStorage(auxiliaryStorage),
		vz.WithMacHardwareModel(hardwareModel),
		vz.WithMacMachineIdentifier(machineIdentifier),
	)
}

func downloadRestoreImage(ctx context.Context, destPath string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0777); err != nil {
		log.Printf("Failed to create directory for restore image: %v", err)
		return err
	}

	// Start the download
	log.Println("Starting macOS restore image download...")
	progress, err := vz.FetchLatestSupportedMacOSRestoreImage(ctx, destPath)
	if err != nil {
		log.Printf("Failed to fetch restore image: %v", err)
		return err
	}

	fmt.Printf("Downloading macOS restore image to %q\n", destPath)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Download has been cancelled")
			return ctx.Err()
		case <-progress.Finished():
			if err := progress.Err(); err != nil {
				log.Printf("Download failed: %v", err)
				return err
			}

			fmt.Println("Download has been completed")
			fileInfo, err := os.Stat(destPath)
			if err != nil {
				log.Printf("Warning: Cannot stat downloaded file: %v", err)
			} else {
				log.Printf("Downloaded file size: %d bytes", fileInfo.Size())
			}

			return nil
		case <-ticker.C:
			fmt.Printf("Download: %.2f%% complete\r", progress.FractionCompleted()*100)
		}
	}
}

// getVersionStringFromRestoreImage loads a restore image and returns a formatted version string
func getVersionStringFromRestoreImage(imagePath string) (string, error) {
	// Load the restore image
	restoreImage, err := vz.LoadMacOSRestoreImageFromPath(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to load restore image: %w", err)
	}
	
	// Extract version information
	buildVer := restoreImage.BuildVersion()
	osVersion := restoreImage.OperatingSystemVersion()
	versionStr := fmt.Sprintf("%d.%d.%d-%s", 
		osVersion.MajorVersion, osVersion.MinorVersion, osVersion.PatchVersion, buildVer)
	
	return versionStr, nil
}

// createSymlinkToVersion creates a symlink from the standard RestoreImage.ipsw path to the given versioned path
func createSymlinkToVersion(versionedPath string) error {
	// Get the standard symlink path
	mainRestorePath := GetRestoreImagePath()

	// Remove existing symlink if it exists
	if _, err := os.Lstat(mainRestorePath); err == nil {
		if err := os.Remove(mainRestorePath); err != nil {
			log.Printf("Warning: Could not remove existing symlink: %v", err)
			return err
		}
	}

	// Create relative path for symlink
	relPath, err := filepath.Rel(filepath.Dir(mainRestorePath), versionedPath)
	if err != nil {
		log.Printf("Warning: Could not determine relative path: %v", err)
		relPath = versionedPath // Fall back to absolute path
	}

	// Create the symlink
	if err := os.Symlink(relPath, mainRestorePath); err != nil {
		log.Printf("Warning: Could not create symlink: %v", err)
		return err
	}

	log.Printf("Created symlink from %s to %s", mainRestorePath, relPath)
	return nil
}
