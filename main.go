package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.podman.io/common/libimage"
	"go.podman.io/common/pkg/config"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/idtools"
	"go.podman.io/storage/pkg/reexec"
)

func main() {
	// Required for proper operation of storage library
	// This must be called early to handle reexec commands like storage-untar
	if reexec.Init() {
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Define command-line flags
	var (
		platform = flag.String("platform", "", "Specify platform in format os/arch[/variant] (e.g., linux/amd64, linux/arm64/v8)")
		osFlag   = flag.String("os", "", "Specify OS (overrides platform)")
		arch     = flag.String("arch", "", "Specify architecture (overrides platform)")
		variant  = flag.String("variant", "", "Specify variant (overrides platform)")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <image>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s alpine:latest /tmp/alpine-rootfs\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --platform linux/amd64 alpine:latest /tmp/alpine-rootfs\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --platform linux/arm64/v8 alpine:latest /tmp/alpine-rootfs\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --os linux --arch arm64 alpine:latest /tmp/alpine-rootfs\n", os.Args[0])
	}

	flag.Parse()

	// Parse positional arguments
	if flag.NArg() < 1 {
		flag.Usage()
		return fmt.Errorf("insufficient arguments")
	}

	imageName := flag.Arg(0)

	// Parse platform string if provided
	var platformOS, platformArch, platformVariant string
	if *platform != "" {
		parts := strings.Split(*platform, "/")
		if len(parts) >= 2 {
			platformOS = parts[0]
			platformArch = parts[1]
			if len(parts) >= 3 {
				platformVariant = parts[2]
			}
		} else {
			return fmt.Errorf("invalid platform format %q, expected os/arch or os/arch/variant", *platform)
		}
	}

	// Individual flags override platform
	if *osFlag != "" {
		platformOS = *osFlag
	}
	if *arch != "" {
		platformArch = *arch
	}
	if *variant != "" {
		platformVariant = *variant
	}

	// Create a temporary directory for storage
	tempDir, err := os.MkdirTemp("", "image-storage-*")
	if err != nil {
		return fmt.Errorf("creating temporary storage directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	fmt.Printf("Using temporary storage at: %s\n", tempDir)

	// Ensure HOME is set to a writable location for container environments
	// (OpenShift, Kubernetes, etc. may not have a proper HOME set)
	if home := os.Getenv("HOME"); home == "" || home == "/" {
		configDir := filepath.Join(tempDir, "config")
		if err := os.MkdirAll(configDir, 0700); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
		os.Setenv("HOME", configDir)
		fmt.Printf("Set HOME to: %s\n", configDir)
	}

	// Configure storage options
	// Use vfs driver for simplicity (works everywhere, no special privileges needed)
	// Set ignore_chown_errors for OpenShift/Kubernetes compatibility where
	// containers run as non-root users and can't change file ownership
	//
	// Additionally, set up identity UID/GID mappings to prevent chown operations
	// This maps the current user's UID/GID to itself (no actual remapping)
	// which tells the storage layer not to attempt chown operations
	currentUID := os.Getuid()
	currentGID := os.Getgid()

	storeOptions := &storage.StoreOptions{
		RunRoot:         filepath.Join(tempDir, "run"),
		GraphRoot:       filepath.Join(tempDir, "root"),
		GraphDriverName: "vfs",
		GraphDriverOptions: []string{
			"vfs.ignore_chown_errors=true",
		},
		// Set up identity mappings: map current UID/GID to itself
		// This prevents chown attempts which would fail in restricted environments
		UIDMap: []idtools.IDMap{
			{
				ContainerID: 0,
				HostID:      currentUID,
				Size:        1,
			},
		},
		GIDMap: []idtools.IDMap{
			{
				ContainerID: 0,
				HostID:      currentGID,
				Size:        1,
			},
		},
	}

	// Create a system context for registry operations
	// Configure explicit paths for container environments
	systemContext := &types.SystemContext{
		// Use system-wide registries.conf if it exists
		SystemRegistriesConfPath: "/etc/containers/registries.conf",
		// Don't look for additional registries.conf.d files that may not exist
		SystemRegistriesConfDirPath: "/dev/null",
		// Use a temp directory for any auth files to avoid permission issues
		AuthFilePath:         filepath.Join(tempDir, "auth.json"),
		BlobInfoCacheDir:     filepath.Join(tempDir, "cache"),
		BigFilesTemporaryDir: tempDir,
	}

	// Create the libimage runtime
	runtime, err := libimage.RuntimeFromStoreOptions(&libimage.RuntimeOptions{
		SystemContext: systemContext,
	}, storeOptions)
	if err != nil {
		return fmt.Errorf("creating libimage runtime: %w", err)
	}
	defer runtime.Shutdown(false)

	// Display what we're pulling
	if platformOS != "" || platformArch != "" || platformVariant != "" {
		platformStr := fmt.Sprintf("%s/%s", platformOS, platformArch)
		if platformVariant != "" {
			platformStr += "/" + platformVariant
		}
		fmt.Printf("Pulling image: %s (platform: %s)\n", imageName, platformStr)
	} else {
		fmt.Printf("Pulling image: %s (platform: default)\n", imageName)
	}

	// Pull the image from the registry
	pullOptions := &libimage.PullOptions{}
	pullOptions.Writer = os.Stdout // Show pull progress

	// Set platform options if specified
	if platformOS != "" {
		pullOptions.OS = platformOS
	}
	if platformArch != "" {
		pullOptions.Architecture = platformArch
	}
	if platformVariant != "" {
		pullOptions.Variant = platformVariant
	}

	pulledImages, err := runtime.Pull(ctx, imageName, config.PullPolicyMissing, pullOptions)
	if err != nil {
		return fmt.Errorf("pulling image %q: %w", imageName, err)
	}

	if len(pulledImages) == 0 {
		return fmt.Errorf("no images pulled")
	}

	image := pulledImages[0]

	// Display image information
	inspectData, err := image.Inspect(ctx, nil)
	if err == nil && inspectData != nil {
		fmt.Printf("\nSuccessfully pulled image: %s\n", imageName)
		fmt.Printf("  ID: %s\n", image.ID())
		fmt.Printf("  OS: %s\n", inspectData.Os)
		fmt.Printf("  Architecture: %s\n", inspectData.Architecture)
	} else {
		fmt.Printf("\nSuccessfully pulled image: %s (ID: %s)\n", imageName, image.ID())
	}

	// Mount the image to access its filesystem
	fmt.Printf("Mounting image...\n")
	mountPoint, err := image.Mount(ctx, nil, "")
	if err != nil {
		return fmt.Errorf("mounting image: %w", err)
	}
	defer func() {
		fmt.Printf("Unmounting image...\n")
		if err := image.Unmount(false); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to unmount image: %v\n", err)
		}
	}()

	fmt.Printf("Image mounted at: %s\n", mountPoint)

	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return err
	}

	for _, e := range entries {
		fmt.Println(e.Name())
	}

	b, _ := os.ReadFile(filepath.Join(mountPoint, "etc/os-release"))
	fmt.Println(string(b))

	return nil
}
