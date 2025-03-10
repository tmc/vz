Example
=======

You can get knowledge build and codesign process in Makefile.

## Build

```sh
make all
```

## Run

- `./virtualization -install` install macOS to your VM.
- `./virtualization` run macOS VM.
- `./virtualization -shared /path/to/directory` run macOS VM with a shared folder.
- `./virtualization -shared /path/to/directory -mount-tag custom-name` run macOS VM with a custom mount tag.
- `./virtualization -shared /path/to/directory -auto-mount` run macOS VM with automounting enabled (macOS 13+ guests only).
- `./virtualization -shared /path/to/directory -mount-tag custom-name -auto-mount` run macOS VM with a custom mount tag and automounting.
- `./virtualization -reinit` reinitialize the VM's platform configuration while preserving the disk image.
- `./virtualization -new-disk` create a fresh disk image (existing disk will be backed up).
- `./virtualization -disk-size 128` specify disk size in GiB (default: 64, only applies to new disks).
- `./virtualization -disk-path /path/to/disk.img` use a custom disk image instead of the default.
- `./virtualization -nested-virt` enable nested virtualization support (macOS 13.0+ required).
- `./virtualization -recovery` provides instructions to boot macOS in recovery mode.
- `./virtualization -versioned-restore=false` disable organizing restore images by macOS version.
- `./virtualization -restore-prefix "Custom"` set a custom prefix for restore image directories.

### Shared Folder Usage

#### Option 1: Automatic Mounting (macOS 13+ Guests) [Coming Soon]

For macOS 13 and later guests, a `-auto-mount` flag is included for future support to automatically mount the shared directory at boot time:

```sh
./virtualization -shared /path/to/directory -auto-mount
```

**Note:** This feature is not yet fully implemented in the current version and requires updates to the VZ bindings. 

When this feature becomes available in future versions:
- The shared directory will appear at `/Volumes/My Shared Files/` by default
- With a custom mount tag: `/Volumes/custom-name/`

For now, the flag is recognized but will fall back to manual mounting.

#### Option 2: Manual Mounting (All macOS Guests)

To manually mount the shared folder inside the macOS VM:

1. Open Terminal in the VM
2. Run the following command to create a mount point:
   ```sh
   mkdir -p ~/shared
   ```
3. Mount the shared folder:
   ```sh
   mount -t virtiofs shared ~/shared
   ```

If you specified a custom mount tag with the `-mount-tag` parameter, use that tag name instead:
```sh
mount -t virtiofs custom-name ~/shared
```

The shared folder will now be accessible at `~/shared` in the VM. The directory is shared with read-write permissions, allowing you to transfer files between the host and VM.

### Custom Mount Tag Naming Guidelines

When choosing a custom mount name with the `-mount-tag` parameter, follow these guidelines:

- **Valid characters**: Use alphanumeric characters (a-z, A-Z, 0-9) and basic punctuation like hyphens (-), underscores (_), and periods (.)
- **Avoid filesystem special characters**: Do not use: `/\:*?"<>|`
- **Length**: Keep the name reasonably short (64 characters or less)
- **Avoid system names**: Do not use names that might conflict with system directories like "System" or "Library"
- **Unicode support**: Non-Latin scripts (like Chinese, Japanese, etc.) are supported, but may have compatibility issues with some applications

#### Nested Directory Support

All subdirectories within the shared folder are automatically accessible. For example, if you share `/Users/username/Documents` and it contains subdirectories like `/Users/username/Documents/Projects` and `/Users/username/Documents/Photos`, you'll be able to access them from the VM at:

```
~/shared/Projects
~/shared/Photos
```

Or with automounting:

```
/Volumes/My Shared Files/Projects
/Volumes/My Shared Files/Photos
```

No additional configuration is required - nested directories inherit the same read-write permissions as the parent directory.

### Troubleshooting Mount Issues

If you experience issues with shared directories:

1. **Check that the guest OS is supported**: Automatic mounting only works on macOS 13+ guests
2. **Verify the VM configuration**: Make sure the shared directories are properly configured
3. **Try restarting the VM**: Sometimes changes only take effect after a restart
4. **Check for invalid characters**: If your custom mount name contains invalid characters, it might cause issues
5. **Try manual mounting**: If automatic mounting fails, try mounting manually using the Terminal commands above

## Nested Virtualization

Nested virtualization allows running virtual machines inside your macOS VM. This feature requires macOS 13.0 or later on your host machine.

A command-line flag is included for future support:

```sh
./virtualization -nested-virt
```

**Note:** This feature is not yet fully implemented in the current version and requires updates to the VZ bindings. When supported in future versions, it will allow you to run virtualization software inside the VM, such as:
- UTM
- VMware Fusion
- Parallels Desktop
- Docker Desktop
- VirtualBox

The flag is included as a placeholder for future updates. Nested virtualization typically adds overhead and may reduce performance compared to a single layer of virtualization.

## Recovery Mode

macOS Recovery provides tools to help troubleshoot your Mac and reinstall macOS. The `-recovery` flag helps you boot into recovery mode:

```sh
./virtualization -recovery
```

When this flag is used:
1. The VM's window title will display "macOS - Recovery Mode"
2. The VM will boot directly into recovery mode using Apple's native Virtualization Framework
3. No need to press and hold keys - recovery mode starts automatically

This feature uses `VZMacOSVirtualMachineStartOptions` with recovery mode enabled, which requires macOS 13.0 or later on the host machine.

Recovery mode allows you to:
- Reinstall or upgrade macOS
- Repair disks with Disk Utility
- Restore from a Time Machine backup
- Get help online by browsing the Apple Support website
- Use Terminal to fix advanced issues

## Disk Management

You can manage the VM's disk image using the following options:

### Creating a New Disk

To create a fresh disk image (existing one will be backed up automatically):

```sh
./virtualization -new-disk
```

To specify a custom disk size in GiB:

```sh
./virtualization -new-disk -disk-size 128
```

### Using a Custom Disk Image

If you have an existing disk image you want to use:

```sh
./virtualization -disk-path /path/to/your/disk.img
```

The specified disk image will be used, and any existing default disk will be backed up.

## VM Reinitialization

If your VM experiences issues with boot, or you need to reset the machine identity for licensing purposes, you can use the `-reinit` flag to regenerate the platform configuration:

```sh
./virtualization -reinit
```

This operation:
1. Preserves your disk image with all installed applications and data
2. Generates a new machine identifier (similar to getting a new Mac)
3. Recreates the auxiliary storage
4. Uses the same hardware model compatible with your macOS version

Reinitialization is useful when:
- VM fails to boot but the disk image is intact
- You encounter activation or Apple ID issues
- You need a fresh machine identity for testing

## Versioned Restore Images

By default, the VM organizes restore images by macOS version. This feature helps you manage multiple macOS installations and keep track of which versions you have downloaded.

Each version is stored in a separate directory with the following format:
```
~/VM.bundle/Restore/macOS-[major].[minor].[patch]-[build]/RestoreImage.ipsw
```

For example:
```
~/VM.bundle/Restore/macOS-14.3.1-23D56/RestoreImage.ipsw
```

### Customizing Versioned Restore Images

You can control this feature with the following flags:

- **Disable versioned organization:**
  ```sh
  ./virtualization -versioned-restore=false
  ```
  With this flag, all restore images will be stored in the main VM.bundle directory.

- **Custom prefix for version directories:**
  ```sh
  ./virtualization -restore-prefix "Ventura"
  ```
  This would create directories like `~/VM.bundle/Restore/Ventura-13.4.1-22F82/RestoreImage.ipsw`.

### Benefits of Versioned Restore Images

- **Organize multiple macOS versions**: Keep multiple restore images organized by version
- **Save download time**: Reuse existing restore images when reinitializing VMs
- **Test different macOS versions**: Easily switch between different macOS versions
- **Save disk space**: Avoid downloading the same restore image multiple times

## Resources

The following resources are created in the `VM.bundle` directory in your home folder:
- `Disk.img` - Main disk image containing macOS and all your data
- `AuxiliaryStorage` - Storage for VM's persistent platform data
- `HardwareModel` - Definition of the virtual Mac hardware
- `MachineIdentifier` - Unique identifier for the virtual Mac
- `Restore/` - Directory containing versioned macOS restore images
  - `macOS-[version]-[build]/RestoreImage.ipsw` - Versioned macOS installation images