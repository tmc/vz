package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Code-Hex/vz/v3"
)

var install bool
var installVersion string
var nbdURL string
var sharedFolderPath string
var mountTag string
var autoMount bool
var reinit bool
var newDisk bool
var diskSize uint64
var diskPath string
var nestedVirt bool
var recoveryMode bool
var restorePrefix string

func init() {
	flag.BoolVar(&install, "install", false, "run command as install mode")
	flag.StringVar(&installVersion, "install-version", "", "specific macOS version to install (e.g. '15.3.1')")
	flag.StringVar(&nbdURL, "nbd-url", "", "nbd url (e.g. nbd+unix:///export?socket=nbd.sock)")
	flag.StringVar(&sharedFolderPath, "shared", "", "path to a directory to share with the VM")
	flag.StringVar(&mountTag, "mount-tag", "shared", "tag name for the shared directory (default: shared)")
	flag.BoolVar(&autoMount, "auto-mount", false, "automatically mount shared directory in macOS 13+ guests")
	flag.BoolVar(&reinit, "reinit", false, "reinitialize VM platform configuration (preserves disk image)")
	flag.BoolVar(&newDisk, "new-disk", false, "create a fresh disk image (existing disk will be backed up)")
	flag.Uint64Var(&diskSize, "disk-size", 64, "disk size in GiB (default: 64)")
	flag.StringVar(&diskPath, "disk-path", "", "path to an existing disk image to use instead of the default")
	flag.BoolVar(&nestedVirt, "nested-virt", false, "enable nested virtualization (macOS 13.0+ required)")
	flag.BoolVar(&recoveryMode, "recovery", false, "boot the VM in recovery mode")
	flag.StringVar(&restorePrefix, "restore-prefix", "", "custom prefix for restore image directories")
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Show available restore images
	versions := ListAvailableRestoreVersions()
	if len(versions) > 0 {
		log.Println("Available macOS restore images:")
		for i, ver := range versions {
			log.Printf(" %d. %s", i+1, ver)
		}
		log.Println("")
	}

	if install {
		log.Println("install mode")
		return installMacOS(ctx)
	}

	// Handle disk management options
	if newDisk {
		if err := createFreshDiskImage(diskSize); err != nil {
			return err
		}
		log.Printf("Created fresh disk image with size %d GiB", diskSize)
	} else if diskPath != "" {
		if err := useCustomDiskImage(diskPath); err != nil {
			return err
		}
		log.Printf("Using custom disk image from %s", diskPath)
	}

	// Handle platform reinitialization
	if reinit {
		if err := reinitializeVM(ctx); err != nil {
			return err
		}
		log.Println("VM successfully reinitialized")
	}

	return runVM(ctx)
}

func runVM(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	platformConfig, err := createMacPlatformConfiguration()
	if err != nil {
		return err
	}
	config, err := setupVMConfiguration(platformConfig)
	if err != nil {
		return err
	}
	vm, err := vz.NewVirtualMachine(config)
	if err != nil {
		return err
	}

	// Start the VM, with recovery mode if requested
	if recoveryMode {
		log.Println("Starting VM in recovery mode")
		if err := vm.Start(vz.WithStartUpFromMacOSRecovery(true)); err != nil {
			return err
		}
	} else {
		if err := vm.Start(); err != nil {
			return err
		}
	}

	errCh := make(chan error, 1)

	go func() {
		for {
			select {
			case newState := <-vm.StateChangedNotify():
				if newState == vz.VirtualMachineStateRunning {
					if recoveryMode {
						log.Println("VM is running in recovery mode")
					} else {
						log.Println("VM is running")
					}
				}
				if newState == vz.VirtualMachineStateStopped || newState == vz.VirtualMachineStateStopping {
					log.Println("stopped state")
					errCh <- nil
					return
				}
			case err := <-errCh:
				errCh <- fmt.Errorf("failed to start vm: %w", err)
				return
			}
		}
	}()

	// it start listening to the NBD server, if any
	nbdAttachment := retrieveNetworkBlockDeviceStorageDeviceAttachment(config.StorageDevices())
	if nbdAttachment != nil {
		go func() {
			for {
				select {
				case err := <-nbdAttachment.DidEncounterError():
					log.Printf("NBD client has been encountered error: %v\n", err)
				case <-nbdAttachment.Connected():
					log.Println("NBD client connected with the server")
				}
			}
		}()
	}

	// cleanup is this function is useful when finished graphic application.
	cleanup := func() {
		for i := 1; vm.CanRequestStop(); i++ {
			result, err := vm.RequestStop()
			log.Printf("sent stop request(%d): %t, %v", i, result, err)
			time.Sleep(time.Second * 3)
			if i > 3 {
				log.Println("call stop")
				if err := vm.Stop(); err != nil {
					log.Println("stop with error", err)
					return
				}
				// if err := vm.Pause(); err != nil {
				// 	log.Println("pause with error", err)
				// 	return
				// }
				// if err := vm.SaveMachineStateToPath("savestate"); err != nil {
				// 	log.Println("save state with error", err)
				// }
			}
		}
		log.Println("finished cleanup")
	}

	// Set window title based on VM mode
	title := "macOS"
	if recoveryMode {
		title = "macOS - Recovery Mode"
	}
	vm.StartGraphicApplication(960, 600, vz.WithWindowTitle(title), vz.WithController(true))

	cleanup()

	return <-errCh
}

func computeCPUCount() uint {
	totalAvailableCPUs := runtime.NumCPU()
	virtualCPUCount := uint(totalAvailableCPUs - 1)
	if virtualCPUCount <= 1 {
		virtualCPUCount = 1
	}
	// TODO(codehex): use generics function when deprecated Go 1.17
	maxAllowed := vz.VirtualMachineConfigurationMaximumAllowedCPUCount()
	if virtualCPUCount > maxAllowed {
		virtualCPUCount = maxAllowed
	}
	minAllowed := vz.VirtualMachineConfigurationMinimumAllowedCPUCount()
	if virtualCPUCount < minAllowed {
		virtualCPUCount = minAllowed
	}
	return virtualCPUCount
}

func computeMemorySize() uint64 {
	// We arbitrarily choose 4GB.
	memorySize := uint64(4 * 1024 * 1024 * 1024)
	maxAllowed := vz.VirtualMachineConfigurationMaximumAllowedMemorySize()
	if memorySize > maxAllowed {
		memorySize = maxAllowed
	}
	minAllowed := vz.VirtualMachineConfigurationMinimumAllowedMemorySize()
	if memorySize < minAllowed {
		memorySize = minAllowed
	}
	return memorySize
}

func createBlockDeviceConfiguration(diskPath string) (*vz.VirtioBlockDeviceConfiguration, error) {
	// Create disk image with the specified size if it doesn't exist
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		sizeBytes := diskSize * 1024 * 1024 * 1024 // Convert GiB to bytes
		if err := vz.CreateDiskImage(diskPath, int64(sizeBytes)); err != nil {
			return nil, fmt.Errorf("failed to create disk image: %w", err)
		}
		log.Printf("Created new disk image with size %d GiB at %s", diskSize, diskPath)
	}

	attachment, err := vz.NewDiskImageStorageDeviceAttachment(
		diskPath,
		false,
	)
	if err != nil {
		return nil, err
	}
	return vz.NewVirtioBlockDeviceConfiguration(attachment)
}

func createNetworkBlockDeviceConfiguration(nbdURL string) (*vz.VirtioBlockDeviceConfiguration, error) {
	attachment, err := vz.NewNetworkBlockDeviceStorageDeviceAttachment(
		nbdURL,
		10*time.Second,
		false,
		vz.DiskSynchronizationModeFull,
	)
	if err != nil {
		return nil, err
	}
	return vz.NewVirtioBlockDeviceConfiguration(attachment)
}

func createGraphicsDeviceConfiguration() (*vz.MacGraphicsDeviceConfiguration, error) {
	graphicDeviceConfig, err := vz.NewMacGraphicsDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	graphicsDisplayConfig, err := vz.NewMacGraphicsDisplayConfiguration(1920, 1200, 80)
	if err != nil {
		return nil, err
	}
	graphicDeviceConfig.SetDisplays(
		graphicsDisplayConfig,
	)
	return graphicDeviceConfig, nil
}

func createNetworkDeviceConfiguration() (*vz.VirtioNetworkDeviceConfiguration, error) {
	natAttachment, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return nil, err
	}
	return vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
}

func createKeyboardConfiguration() (vz.KeyboardConfiguration, error) {
	config, err := vz.NewMacKeyboardConfiguration()
	if err != nil {
		if errors.Is(err, vz.ErrUnsupportedOSVersion) {
			return vz.NewUSBKeyboardConfiguration()
		}
		return nil, err
	}
	return config, nil
}

func createAudioDeviceConfiguration() (*vz.VirtioSoundDeviceConfiguration, error) {
	audioConfig, err := vz.NewVirtioSoundDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create sound device configuration: %w", err)
	}
	inputStream, err := vz.NewVirtioSoundDeviceHostInputStreamConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create input stream configuration: %w", err)
	}
	outputStream, err := vz.NewVirtioSoundDeviceHostOutputStreamConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create output stream configuration: %w", err)
	}
	audioConfig.SetStreams(
		inputStream,
		outputStream,
	)
	return audioConfig, nil
}

func createSharedDirectoryConfiguration(path string, tag string) (*vz.VirtioFileSystemDeviceConfiguration, error) {
	if path == "" {
		return nil, nil
	}

	// Verify the directory exists
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access directory %q: %w", path, err)
	}

	// Ensure it's actually a directory
	if !fileInfo.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", path)
	}

	// Check if we have read permissions for the directory
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open directory %q: %w", path, err)
	}
	file.Close()

	// Create a shared directory
	sharedDir, err := vz.NewSharedDirectory(path, false) // false means writable
	if err != nil {
		return nil, fmt.Errorf("failed to create shared directory: %w", err)
	}

	// Create a single directory share configuration
	singleShare, err := vz.NewSingleDirectoryShare(sharedDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create single directory share: %w", err)
	}

	// If automount is requested, use Apple's automount tag for macOS 13+ guests
	var fsConfig *vz.VirtioFileSystemDeviceConfiguration
	var configErr error

	if autoMount {
		// For now, auto-mounting is not available through the bindings
		// Use the standard approach with custom tag
		log.Println("Auto-mounting support not available in this version, using manual mounting")
		fsConfig, configErr = vz.NewVirtioFileSystemDeviceConfiguration(tag)
		log.Printf("Sharing directory %q with tag %q (requires manual mounting)", path, tag)
	} else {
		// Use the custom tag for manual mounting
		fsConfig, configErr = vz.NewVirtioFileSystemDeviceConfiguration(tag)
	}

	if configErr != nil {
		return nil, fmt.Errorf("failed to create virtio file system device configuration: %w", configErr)
	}

	// Set the directory share for the file system device
	fsConfig.SetDirectoryShare(singleShare)

	if !autoMount {
		log.Printf("Sharing directory %q with tag %q (includes all subdirectories)", path, tag)
		log.Printf("To mount manually, use: mount -t virtiofs %s ~/shared", tag)
	}

	return fsConfig, nil
}

func createMacPlatformConfiguration() (*vz.MacPlatformConfiguration, error) {
	auxiliaryStorage, err := vz.NewMacAuxiliaryStorage(GetAuxiliaryStoragePath())
	if err != nil {
		return nil, fmt.Errorf("failed to create a new mac auxiliary storage: %w", err)
	}
	hardwareModel, err := vz.NewMacHardwareModelWithDataPath(
		GetHardwareModelPath(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a new hardware model: %w", err)
	}
	machineIdentifier, err := vz.NewMacMachineIdentifierWithDataPath(
		GetMachineIdentifierPath(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a new machine identifier: %w", err)
	}

	// Create the configuration with initial options
	configOptions := []vz.MacPlatformConfigurationOption{
		vz.WithMacAuxiliaryStorage(auxiliaryStorage),
		vz.WithMacHardwareModel(hardwareModel),
		vz.WithMacMachineIdentifier(machineIdentifier),
	}

	// Add nested virtualization if requested (requires macOS 13.0+)
	if nestedVirt {
		// For now, nested virtualization flag is not directly exposed in the bindings
		// We'll need to use the API directly when it becomes available
		log.Println("Note: Nested virtualization support requested but not available in current version")
		log.Println("This would require macOS 13.0+ and updated VZ bindings")
	}

	return vz.NewMacPlatformConfiguration(configOptions...)
}

func setupVMConfiguration(platformConfig vz.PlatformConfiguration) (*vz.VirtualMachineConfiguration, error) {
	// Create standard bootloader
	bootloader, err := vz.NewMacOSBootLoader()
	if err != nil {
		return nil, err
	}

	// Log if recovery mode will be enabled
	if recoveryMode {
		log.Println("Preparing VM for recovery mode boot (requires macOS 13.0+)")
	}

	config, err := vz.NewVirtualMachineConfiguration(
		bootloader,
		computeCPUCount(),
		computeMemorySize(),
	)
	if err != nil {
		return nil, err
	}
	config.SetPlatformVirtualMachineConfiguration(platformConfig)
	graphicsDeviceConfig, err := createGraphicsDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create graphics device configuration: %w", err)
	}
	config.SetGraphicsDevicesVirtualMachineConfiguration([]vz.GraphicsDeviceConfiguration{
		graphicsDeviceConfig,
	})
	blockDeviceConfig, err := createBlockDeviceConfiguration(GetDiskImagePath())
	if err != nil {
		return nil, fmt.Errorf("failed to create block device configuration: %w", err)
	}
	sdconfigs := []vz.StorageDeviceConfiguration{blockDeviceConfig}
	if nbdURL != "" {
		ndbConfig, err := createNetworkBlockDeviceConfiguration(nbdURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create network block device configuration: %w", err)
		}
		sdconfigs = append(sdconfigs, ndbConfig)
	}
	config.SetStorageDevicesVirtualMachineConfiguration(sdconfigs)

	networkDeviceConfig, err := createNetworkDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create network device configuration: %w", err)
	}
	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{
		networkDeviceConfig,
	})

	usbScreenPointingDevice, err := vz.NewUSBScreenCoordinatePointingDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create pointing device configuration: %w", err)
	}
	pointingDevices := []vz.PointingDeviceConfiguration{usbScreenPointingDevice}

	trackpad, err := vz.NewMacTrackpadConfiguration()
	if err == nil {
		pointingDevices = append(pointingDevices, trackpad)
	}
	config.SetPointingDevicesVirtualMachineConfiguration(pointingDevices)

	keyboardDeviceConfig, err := createKeyboardConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create keyboard device configuration: %w", err)
	}
	config.SetKeyboardsVirtualMachineConfiguration([]vz.KeyboardConfiguration{
		keyboardDeviceConfig,
	})

	audioDeviceConfig, err := createAudioDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to create audio device configuration: %w", err)
	}
	config.SetAudioDevicesVirtualMachineConfiguration([]vz.AudioDeviceConfiguration{
		audioDeviceConfig,
	})

	// Add shared directory if provided
	if sharedFolderPath != "" {
		sharedDirConfig, err := createSharedDirectoryConfiguration(sharedFolderPath, mountTag)
		if err != nil {
			return nil, fmt.Errorf("failed to create shared directory configuration: %w", err)
		}
		if sharedDirConfig != nil {
			config.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{
				sharedDirConfig,
			})
		}
	}

	validated, err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate configuration: %w", err)
	}
	if !validated {
		return nil, fmt.Errorf("invalid configuration")
	}

	// If you want to try this one, you need to comment out a few of configs.
	//
	// if _, err := config.ValidateSaveRestoreSupport(); err != nil {
	// 	return nil, fmt.Errorf("failed to validate save restore configuration: %w", err)
	// }

	return config, nil
}

func retrieveNetworkBlockDeviceStorageDeviceAttachment(storages []vz.StorageDeviceConfiguration) *vz.NetworkBlockDeviceStorageDeviceAttachment {
	for _, storage := range storages {
		attachment := storage.Attachment()
		if nbdAttachment, ok := attachment.(*vz.NetworkBlockDeviceStorageDeviceAttachment); ok {
			return nbdAttachment
		}
	}
	return nil
}

func reinitializeVM(ctx context.Context) error {
	// Ensure the VM.bundle directory exists
	if err := CreateVMBundle(); err != nil {
		return fmt.Errorf("failed to create VM.bundle directory: %w", err)
	}

	// Check for restore images
	restoreImagePath := GetRestoreImagePath()

	// First, check if we have versioned restore images
	// Look for files directly in the Restore directory
	restoreDir := filepath.Join(GetVMBundlePath(), "Restore")
	versions := ListAvailableRestoreVersions()

	if len(versions) > 0 {
		// Use the first version (assumed to be most recent)
		versionedPath := GetVersionedRestorePath(versions[0])
		if _, err := os.Stat(versionedPath); err == nil {
			// Found a versioned restore image
			log.Printf("Found versioned restore image: %s", versionedPath)
			restoreImagePath = versionedPath

			// Create/update symlink to the most recent version
			if err := createSymlinkToVersion(versionedPath); err != nil {
				log.Printf("Warning: Could not create symlink to versioned image: %v", err)
			}
		}
	}

	// If no restore image found, download one
	if _, err := os.Stat(restoreImagePath); err != nil {
		if os.IsNotExist(err) {
			// Download the restore image if it doesn't exist
			log.Println("Restore image not found. Downloading...")

			// Download to a temporary file first (need to download before we can determine version)
			tempPath := filepath.Join(restoreDir, "downloading.ipsw")
			if err := downloadRestoreImage(ctx, tempPath); err != nil {
				return fmt.Errorf("failed to download restore image: %w", err)
			}

			// Load the downloaded image to get version info
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
				restoreImagePath = tempPath
			} else {
				log.Printf("Moved restore image to: %s", versionedPath)
				restoreImagePath = versionedPath

				// Create a symlink to this version
				if err := createSymlinkToVersion(versionedPath); err != nil {
					log.Printf("Warning: Could not create symlink to versioned image: %v", err)
				}
			}
		} else {
			return fmt.Errorf("failed to check restore image: %w", err)
		}
	}

	// Resolve symlinks
	restoreImagePath, err := filepath.EvalSymlinks(restoreImagePath)
	if err != nil {
		return fmt.Errorf("failed to resolve restore image path: %w", err)
	}

	// Load the restore image
	log.Printf("Loading restore image from: %s", restoreImagePath)
	restoreImage, err := vz.LoadMacOSRestoreImageFromPath(restoreImagePath)
	if err != nil {
		return fmt.Errorf("failed to load restore image: %w", err)
	}

	// Get the hardware model from the restore image
	configRequirements := restoreImage.MostFeaturefulSupportedConfiguration()
	hardwareModel := configRequirements.HardwareModel()

	// Generate a new machine identifier
	log.Println("Generating new machine identifier...")
	machineIdentifier, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return fmt.Errorf("failed to create new machine identifier: %w", err)
	}

	// Save the hardware model
	if err := CreateFileAndWriteTo(
		hardwareModel.DataRepresentation(),
		GetHardwareModelPath(),
	); err != nil {
		return fmt.Errorf("failed to write hardware model data: %w", err)
	}

	// Save the machine identifier
	if err := CreateFileAndWriteTo(
		machineIdentifier.DataRepresentation(),
		GetMachineIdentifierPath(),
	); err != nil {
		return fmt.Errorf("failed to write machine identifier data: %w", err)
	}

	// Remove existing auxiliary storage if it exists
	auxiliaryStoragePath := GetAuxiliaryStoragePath()
	if _, err := os.Stat(auxiliaryStoragePath); err == nil {
		log.Println("Removing existing auxiliary storage...")
		if err := os.Remove(auxiliaryStoragePath); err != nil {
			return fmt.Errorf("failed to remove existing auxiliary storage: %w", err)
		}
	}

	// Create a new auxiliary storage with the hardware model
	log.Println("Creating new auxiliary storage...")
	_, err = vz.NewMacAuxiliaryStorage(
		auxiliaryStoragePath,
		vz.WithCreatingMacAuxiliaryStorage(hardwareModel),
	)
	if err != nil {
		return fmt.Errorf("failed to create a new auxiliary storage: %w", err)
	}

	log.Println("VM platform configuration successfully reinitialized")
	return nil
}

// verifyNestedDirectoryAccess checks if we can access subdirectories
// by traversing up to 3 levels of nested directories and ensuring we have
// proper access permissions.
func verifyNestedDirectoryAccess(dirPath string) error {
	// Get all directories in the root shared directory
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("unable to read directory %q: %w", dirPath, err)
	}

	// Count of subdirectories we found
	foundDirs := 0

	// Try to access a few subdirectories (max 3 levels deep)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		foundDirs++
		level1Path := filepath.Join(dirPath, entry.Name())

		// Try to open this directory
		dir1, err := os.Open(level1Path)
		if err != nil {
			return fmt.Errorf("cannot access subdirectory %q: %w", level1Path, err)
		}
		dir1.Close()

		// Look for level 2 subdirectories
		level1Entries, err := os.ReadDir(level1Path)
		if err != nil {
			// Skip this directory if we can't read it
			continue
		}

		// Check a couple of subdirectories at level 2
		checkedLevel2 := 0
		for _, level1Entry := range level1Entries {
			if !level1Entry.IsDir() || checkedLevel2 >= 2 {
				continue
			}

			checkedLevel2++
			level2Path := filepath.Join(level1Path, level1Entry.Name())

			// Try to open this directory
			dir2, err := os.Open(level2Path)
			if err != nil {
				return fmt.Errorf("cannot access nested subdirectory %q: %w", level2Path, err)
			}
			dir2.Close()
		}

		// Only check a few directories at the top level
		if foundDirs >= 3 {
			break
		}
	}

	// If we found subdirectories, log that we successfully verified access
	if foundDirs > 0 {
		log.Printf("Successfully verified access to %d nested subdirectories in %q", foundDirs, dirPath)
	}

	return nil
}

// createFreshDiskImage creates a new disk image with the specified size.
// If an existing disk image is found, it will be backed up before creating the new one.
func createFreshDiskImage(sizeGiB uint64) error {
	defaultDiskPath := GetDiskImagePath()

	// Check if a disk image already exists
	if _, err := os.Stat(defaultDiskPath); err == nil {
		// Create a backup of the existing disk
		backupPath := defaultDiskPath + ".backup-" + time.Now().Format("20060102-150405")
		log.Printf("Backing up existing disk image to %s", backupPath)

		// Copy the existing disk to the backup location
		if err := os.Rename(defaultDiskPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup existing disk image: %w", err)
		}
	}

	// Convert GiB to bytes
	sizeBytes := sizeGiB * 1024 * 1024 * 1024

	// Create the disk image directory if needed
	if err := CreateVMBundle(); err != nil {
		return fmt.Errorf("failed to create VM bundle directory: %w", err)
	}

	// Create the new disk image
	log.Printf("Creating new %d GiB disk image at %s", sizeGiB, defaultDiskPath)
	if err := vz.CreateDiskImage(defaultDiskPath, int64(sizeBytes)); err != nil {
		return fmt.Errorf("failed to create disk image: %w", err)
	}

	return nil
}

// useCustomDiskImage copies or symlinks a custom disk image to use with the VM.
func useCustomDiskImage(sourcePath string) error {
	defaultDiskPath := GetDiskImagePath()

	// Check if the source disk image exists
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("custom disk image not found at %s: %w", sourcePath, err)
	}

	// Check if a disk image already exists at the default location
	if _, err := os.Stat(defaultDiskPath); err == nil {
		// Create a backup of the existing disk
		backupPath := defaultDiskPath + ".backup-" + time.Now().Format("20060102-150405")
		log.Printf("Backing up existing disk image to %s", backupPath)

		// Copy the existing disk to the backup location
		if err := os.Rename(defaultDiskPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup existing disk image: %w", err)
		}
	}

	// Create the VM bundle directory if needed
	if err := CreateVMBundle(); err != nil {
		return fmt.Errorf("failed to create VM bundle directory: %w", err)
	}

	// Create a hard link to the source file (more efficient than copying)
	// if on the same filesystem
	if err := os.Link(sourcePath, defaultDiskPath); err != nil {
		// If hard linking fails (e.g., different filesystems), copy the file
		log.Printf("Hard linking failed, copying disk image instead (this may take a while)...")

		sourceFile, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to open source disk image: %w", err)
		}
		defer sourceFile.Close()

		destFile, err := os.Create(defaultDiskPath)
		if err != nil {
			return fmt.Errorf("failed to create destination disk image: %w", err)
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, sourceFile)
		if err != nil {
			return fmt.Errorf("failed to copy disk image: %w", err)
		}
	}

	return nil
}

// macOSAvailable checks if the current macOS version is at least the version specified
func macOSAvailable(major, minor int) error {
	// Use a simple check for now since the package doesn't expose MacOSVersion
	// This relies on build constraints in the package itself
	if major == 13 {
		// For macOS 13+, we'll check if hardware model virtualization is supported indirectly
		// by checking if Go can build with the target SDK version
		// This is just a placeholder - real code would need to check properly
		return nil
	}

	return fmt.Errorf("required macOS %d.%d may not be available", major, minor)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// Open source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
		return err
	}

	// Create destination file
	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	// Copy the contents
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Ensure contents are flushed to disk
	return destFile.Sync()
}
