# Pull and Extract Example

This example demonstrates how to use the container-libs libraries (`common/libimage`, `image`, and `storage`) to pull a container image from a registry and extract its filesystem to a local directory.

## Overview

The example shows:
- Creating a libimage runtime with temporary storage
- Pulling an image from a container registry with optional platform selection (OS/architecture/variant)
- Mounting the image to access its filesystem layers
- Extracting the complete filesystem to a directory using the archive utilities
- Proper cleanup and resource management

## Building

From this directory:

```bash
# Build with GOWORK=off to avoid workspace vendoring issues
GOWORK=off go build -o pull-and-extract
```

Alternatively, from the repository root:

```bash
cd examples/pull-and-extract
GOWORK=off go build -o pull-and-extract
```

## Usage

```bash
./pull-and-extract [flags] <image> <output-directory>
```

### Flags

- `--platform <os/arch[/variant]>` - Specify the platform in format `os/arch` or `os/arch/variant` (e.g., `linux/amd64`, `linux/arm64/v8`)
- `--os <os>` - Specify OS (overrides `--platform`)
- `--arch <arch>` - Specify architecture (overrides `--platform`)
- `--variant <variant>` - Specify variant (overrides `--platform`)

### Examples

Pull Alpine Linux for the default platform and extract to `/tmp/alpine-rootfs`:
```bash
./pull-and-extract alpine:latest /tmp/alpine-rootfs
```

Pull Alpine Linux for AMD64 (x86_64) platform:
```bash
./pull-and-extract --platform linux/amd64 alpine:latest /tmp/alpine-amd64
```

Pull Alpine Linux for ARM64 platform with v8 variant:
```bash
./pull-and-extract --platform linux/arm64/v8 alpine:latest /tmp/alpine-arm64
```

Pull using individual OS and architecture flags:
```bash
./pull-and-extract --os linux --arch arm64 alpine:latest /tmp/alpine-arm64
```

Pull a specific image with digest:
```bash
./pull-and-extract alpine@sha256:... /tmp/alpine-rootfs
```

Pull from a specific registry for a specific platform:
```bash
./pull-and-extract --platform linux/amd64 quay.io/podman/hello /tmp/hello-rootfs
```

Pull multi-arch image for 32-bit ARM:
```bash
./pull-and-extract --platform linux/arm/v7 alpine:latest /tmp/alpine-armv7
```

## How It Works

1. **Storage Setup**: Creates a temporary directory with VFS storage driver (no special privileges needed)
2. **Platform Selection**: Parses platform flags and configures the pull options for the desired OS/architecture/variant
3. **Image Pull**: Uses `libimage.Runtime.Pull()` to fetch the image from the registry for the specified platform
4. **Image Mount**: Mounts the image layers to get a unified filesystem view
5. **Extraction**: Uses `storage/pkg/archive` to create a tar archive and extract it to the destination
6. **Cleanup**: Unmounts the image and removes temporary storage

## Key APIs Used

### libimage (common/libimage)
- `RuntimeFromStoreOptions()` - Creates a new libimage runtime
- `Runtime.Pull()` - Pulls images from registries
- `Image.Mount()` - Mounts an image to access its filesystem
- `Image.Unmount()` - Unmounts an image

### storage (storage)
- `StoreOptions` - Configures the storage backend
- `pkg/archive.Tar()` - Creates tar archives
- `pkg/archive.Untar()` - Extracts tar archives

### image (image/v5)
- `types.SystemContext` - Provides registry configuration

## Running in Containers (OpenShift/Kubernetes)

### Current Limitations

**Important**: This example currently **does not work** in OpenShift's restricted Security Context Constraints (SCC) due to the storage library's requirement for mount namespace creation, which results in the error:
```
creating mount namespace before pivot: function not implemented
```

The storage library's tar extraction process (`storage-untar` reexec command) attempts to create mount namespaces via `unshare(CLONE_NEWNS)`, which is blocked by OpenShift's restricted SCC.

### Workarounds

To use this example in OpenShift, you have these options:

1. **Run with elevated SCC** (if your cluster admin permits):
   ```yaml
   apiVersion: v1
   kind: SecurityContextConstraints
   metadata:
     name: allow-unshare
   allowPrivilegeEscalation: true
   allowedCapabilities:
   - SYS_ADMIN
   ```

2. **Run outside OpenShift**: Use this example on a regular VM or machine where you have normal user permissions

3. **Use alternative approaches**: Instead of using the storage library's mount/extract, pull images using other tools (skopeo, podman, buildah) and extract them separately

### Partial OpenShift Compatibility

The example does include several OpenShift compatibility fixes that handle other common issues:
- Detecting when HOME is unset or set to `/` and creating a temporary config directory
- Explicitly configuring SystemContext paths to avoid relying on user home directories
- Using the VFS storage driver which doesn't require special privileges
- Setting `ignore_chown_errors=true` to prevent failures when the container can't change file ownership
- Configuring identity UID/GID mappings (mapping current user to container root) to prevent chown system calls

These fixes resolve issues up to the point where the storage library needs to create mount namespaces.

### Building the Container

From the repository root:

```bash
podman build -f Containerfile -t pull-and-extract:latest .
```

### Running the Container

```bash
# Pull alpine for amd64 and extract to a volume
podman run --rm -v /tmp/output:/output pull-and-extract:latest \
  --platform linux/amd64 alpine:latest /output

# Run in OpenShift
oc run pull-extract --image=pull-and-extract:latest \
  --command -- /usr/local/bin/pull-and-extract --help
```

## Notes

- The example uses the VFS storage driver which doesn't require special privileges but is slower and uses more disk space than overlay drivers
- A temporary storage directory is created and automatically cleaned up
- The `reexec.Init()` call is required for proper operation of the storage library
- Pull progress is displayed to stdout during the image download
- **OpenShift/Kubernetes compatibility**:
  - Automatically handles missing or invalid HOME environments
  - Uses `vfs.ignore_chown_errors=true` to prevent "operation not permitted" errors when running as non-root
  - Configures identity UID/GID mappings to prevent chown system calls (maps container UID 0 → host current UID)
  - Registers custom `storage-untar` reexec command that avoids mount namespace creation
  - Configured to work with random UIDs assigned by OpenShift security constraints
  - All configuration paths explicitly set to avoid permission issues

  **Why these workarounds?**
  - **UID/GID mappings**: In OpenShift, containers run as arbitrary non-root UIDs (e.g., 1000660000) and cannot execute `chown` operations. The identity mapping tells the storage layer to preserve file ownership as-is rather than attempting to change it.
  - **Custom storage-untar**: The default storage layer uses `unshare(CLONE_NEWNS)` to create mount namespaces for secure tar extraction. OpenShift's restricted Security Context Constraints (SCC) block this syscall, causing "function not implemented" errors. Our custom command extracts tars directly without namespace isolation, which is safe for read-only image extraction.
