package install

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type UninstallRequest struct {
	Root       string
	Apply      bool
	PurgeState bool
}

type UninstallHost interface {
	Systemctl(context.Context, ...string) error
}

// updateLocker is the optional part of a host that can hold the update lock
// every updater takes (ozal update, ozal upgrade, the update executor), so an
// uninstall never removes the binary under an update that is running.
type updateLocker interface {
	LockUpdates(path string) (release func(), err error)
}

var serviceUnits = []string{"ozal.service", "ozal-cloudflared.service", "ozal-update.service", "ozal-update.timer", "ozal-update-executor.service"}

func uninstallBinaryArtifacts(root string) ([]string, error) {
	dir := mapped(root, "/usr/local/lib/ozal")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot inspect Ozal binary directory")
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		// update.lock is left by every update; the uninstaller holds it while
		// removing, so it is ours to remove too.
		known := name == "ozal" || name == "ozal.next" || name == "ozal.upgrade.log" || name == "update.lock"
		if strings.HasPrefix(name, "ozal.bak-") && len(name) == len("ozal.bak-")+12 {
			_, decodeErr := hex.DecodeString(strings.TrimPrefix(name, "ozal.bak-"))
			known = decodeErr == nil
		}
		info, err := entry.Info()
		if err != nil || !known || !info.Mode().IsRegular() {
			return nil, errors.New("ozal binary directory contains files this uninstaller does not own")
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

func Uninstall(ctx context.Context, out io.Writer, host UninstallHost, r UninstallRequest) error {
	if runtime.GOOS != "linux" && r.Root == "" {
		return errors.New("the systemd uninstaller supports Linux only")
	}
	if r.Root != "" && (!filepath.IsAbs(r.Root) || filepath.Clean(r.Root) == string(filepath.Separator)) {
		return errors.New("staging root must be absolute and must not be the host root")
	}
	paths := make([]string, 0, len(serviceUnits)+4)
	for _, unit := range serviceUnits {
		paths = append(paths, "/etc/systemd/system/"+unit)
	}
	paths = append(paths, "/usr/local/bin/ozal", "/usr/local/lib/ozal")
	if r.PurgeState {
		paths = append(paths, "/etc/ozal", "/var/lib/ozal")
	}
	for _, path := range paths {
		actual := mapped(r.Root, path)
		// The command link is the one leaf that is expected to be a symlink.
		if path == "/usr/local/bin/ozal" {
			actual = filepath.Dir(actual)
		}
		if err := noLinks(actual); err != nil {
			return err
		}
	}
	binaryArtifacts, err := uninstallBinaryArtifacts(r.Root)
	if err != nil {
		return err
	}
	link := mapped(r.Root, "/usr/local/bin/ozal")
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New("refusing to remove /usr/local/bin/ozal because it is not the installer-owned symlink")
		}
		target, err := os.Readlink(link)
		if err != nil || target != "/usr/local/lib/ozal/ozal" {
			return errors.New("refusing to remove /usr/local/bin/ozal because its target is not the Ozal service binary")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := writeUninstallPlan(out, r, binaryArtifacts); err != nil {
		return err
	}
	if !r.Apply {
		return nil
	}
	if r.Root == "" && (os.Geteuid() != 0 || host == nil) {
		return errors.New("a host administrator and Linux service adapter are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Take the update lock before changing anything: a running update is
	// refused here, with every unit and file still in place.
	if locker, ok := host.(updateLocker); ok {
		if _, err := os.Lstat(mapped(r.Root, "/usr/local/lib/ozal")); err == nil {
			release, err := locker.LockUpdates(mapped(r.Root, "/usr/local/lib/ozal/update.lock"))
			if err != nil {
				return fmt.Errorf("an Ozal update is running or its lock is unusable; let it finish before uninstalling: %w", err)
			}
			defer release()
		}
	}
	if r.Root == "" {
		for _, unit := range serviceUnits {
			unitPath := mapped(r.Root, "/etc/systemd/system/"+unit)
			if _, err := os.Lstat(unitPath); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			if err := host.Systemctl(ctx, "stop", unit); err != nil {
				return err
			}
			if err := host.Systemctl(ctx, "disable", unit); err != nil {
				return err
			}
		}
	}
	if _, err := os.Lstat(link); err == nil {
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove Ozal command link: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, unit := range serviceUnits {
		if err := os.Remove(mapped(r.Root, "/etc/systemd/system/"+unit)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Ozal service unit: %w", err)
		}
	}
	libDir := mapped(r.Root, "/usr/local/lib/ozal")
	for _, path := range binaryArtifacts {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Ozal binary artifact: %w", err)
		}
	}
	if err := os.Remove(libDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ozal binary directory contains files this installer does not own: %w", err)
	}
	if r.PurgeState {
		for _, path := range []string{"/etc/ozal", "/var/lib/ozal"} {
			if err := os.RemoveAll(mapped(r.Root, path)); err != nil {
				return fmt.Errorf("remove Ozal configuration and state: %w", err)
			}
		}
	}
	if r.Root == "" {
		if err := host.Systemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
		_ = host.Systemctl(ctx, "reset-failed", "ozal.service", "ozal-cloudflared.service", "ozal-update.service", "ozal-update.timer", "ozal-update-executor.service")
	}
	fmt.Fprintln(out, "Ozal service and binaries removed. The ozal account and group were retained.")
	return nil
}

// writeUninstallPlan names the units and files found, and what happens to
// configuration and state, before anything is removed.
func writeUninstallPlan(out io.Writer, r UninstallRequest, binaryArtifacts []string) error {
	fmt.Fprintf(out, "Uninstall plan (apply=%t, staging=%t):\n", r.Apply, r.Root != "")
	if r.Root != "" {
		fmt.Fprintf(out, "  every path below is under %s; systemd is not called\n", r.Root)
	}
	unitAction := "stop, disable and remove"
	if r.Root != "" {
		unitAction = "remove"
	}
	var units []string
	for _, unit := range serviceUnits {
		if _, err := os.Lstat(mapped(r.Root, "/etc/systemd/system/"+unit)); err == nil {
			units = append(units, unit)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(units) == 0 {
		fmt.Fprintln(out, "  units: none found")
	} else {
		fmt.Fprintf(out, "  %s %s (/etc/systemd/system)\n", unitAction, strings.Join(units, ", "))
	}
	if _, err := os.Lstat(mapped(r.Root, "/usr/local/bin/ozal")); err == nil {
		fmt.Fprintln(out, "  remove /usr/local/bin/ozal (command link)")
	} else if !os.IsNotExist(err) {
		return err
	}
	if len(binaryArtifacts) > 0 {
		names := make([]string, 0, len(binaryArtifacts))
		for _, path := range binaryArtifacts {
			names = append(names, filepath.Base(path))
		}
		fmt.Fprintf(out, "  remove /usr/local/lib/ozal: %s\n", strings.Join(names, ", "))
	}
	if r.PurgeState {
		fmt.Fprintln(out, "  remove permanently /etc/ozal and /var/lib/ozal: configuration, admin token, state database, passkeys and sites")
	} else {
		fmt.Fprintln(out, "  keep /etc/ozal and /var/lib/ozal (add -purge-state to remove them permanently)")
	}
	fmt.Fprintln(out, "  keep the ozal account and group")
	return nil
}
