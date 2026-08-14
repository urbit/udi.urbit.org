package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func stageDataDirectory(root, stage string) (string, error) {
	source := filepath.Join(root, "data")
	destination := filepath.Join(stage, "data")
	if err := copyDirectory(source, destination); err != nil {
		return "", fmt.Errorf("stage data directory: %w", err)
	}
	return destination, nil
}

func publishDataAndSite(root, output, stage string) error {
	dataBackup := filepath.Join(root, ".udi-previous-data")
	siteBackup := output + ".udi-previous"
	if err := swapDirectory(filepath.Join(root, "data"), filepath.Join(stage, "data"), dataBackup); err != nil {
		return fmt.Errorf("publish refreshed data: %w", err)
	}
	if err := swapDirectory(output, filepath.Join(stage, "dist"), siteBackup); err != nil {
		rollbackErr := rollbackDirectory(filepath.Join(root, "data"), dataBackup)
		if rollbackErr != nil {
			return fmt.Errorf("publish refreshed site: %v; rollback data: %w", err, rollbackErr)
		}
		return fmt.Errorf("publish refreshed site; data restored: %w", err)
	}
	if err := os.RemoveAll(dataBackup); err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove previous data backup %s: %v\n", dataBackup, err)
	}
	if err := os.RemoveAll(siteBackup); err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove previous site backup %s: %v\n", siteBackup, err)
	}
	return nil
}

func publishData(root, stage string) error {
	backup := filepath.Join(root, ".udi-previous-data")
	if err := swapDirectory(filepath.Join(root, "data"), filepath.Join(stage, "data"), backup); err != nil {
		return fmt.Errorf("publish discovered repository data: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove previous data backup %s: %v\n", backup, err)
	}
	return nil
}

func swapDirectory(current, replacement, backup string) error {
	if _, err := os.Stat(replacement); err != nil {
		return fmt.Errorf("inspect replacement directory %s: %w", replacement, err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove stale backup %s: %w", backup, err)
	}
	hadCurrent := false
	if _, err := os.Stat(current); err == nil {
		hadCurrent = true
		if err := os.Rename(current, backup); err != nil {
			return fmt.Errorf("stage current directory %s: %w", current, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect current directory %s: %w", current, err)
	}
	if err := os.Rename(replacement, current); err != nil {
		if hadCurrent {
			if restoreErr := os.Rename(backup, current); restoreErr != nil {
				return fmt.Errorf("replace directory %s: %v; restore previous directory: %w", current, err, restoreErr)
			}
		}
		return fmt.Errorf("replace directory %s: %w", current, err)
	}
	return nil
}

func rollbackDirectory(current, backup string) error {
	if err := os.RemoveAll(current); err != nil {
		return fmt.Errorf("remove published directory %s: %w", current, err)
	}
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("inspect backup directory %s: %w", backup, err)
	}
	if err := os.Rename(backup, current); err != nil {
		return fmt.Errorf("restore backup directory %s: %w", current, err)
	}
	return nil
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("resolve relative path %s: %w", path, err)
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to copy symbolic link %s", path)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create directory %s: %w", target, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to copy non-regular file %s", path)
		}
		if err := copyRegularFile(path, target, info.Mode().Perm()); err != nil {
			return err
		}
		return nil
	})
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close %s: %w", destination, err)
	}
	return nil
}
