package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Keep package selection repository-relative; only execution crosses a
// committed nested module boundary. Go -C also keeps shell and JSON plans usable.
func routeGoModuleCommands(repo string, commands []testCommand) ([]testCommand, error) {
	var owned map[string]bool
	result := []testCommand{}
	for _, command := range commands {
		if len(command.Argv) < 3 || command.Argv[0] != "go" || command.Argv[1] != "test" {
			result = append(result, command)
			continue
		}
		if owned == nil {
			tree, err := gitOutput(repo, "ls-tree", "-r", "-z", "HEAD")
			if err != nil {
				return nil, err
			}
			owned = map[string]bool{}
			for _, entry := range strings.Split(tree, "\x00") {
				metadata, name, ok := strings.Cut(entry, "\t")
				if ok && strings.HasPrefix(metadata, "100644 blob ") && path.Base(name) == "go.mod" {
					owned[path.Dir(name)] = true
				}
			}
		}
		start := 2
		for start < len(command.Argv) && strings.HasPrefix(command.Argv[start], "-") {
			switch command.Argv[start] {
			case "-run", "-skip", "-tags":
				start++
			}
			start++
		}
		if start == len(command.Argv) {
			result = append(result, command)
			continue
		}
		groups := map[string][]string{}
		order := []string{}
		for _, pattern := range command.Argv[start:] {
			module, relative, err := ownedGoPackage(repo, pattern, owned)
			if err != nil {
				return nil, err
			}
			if _, ok := groups[module]; !ok {
				order = append(order, module)
			}
			groups[module] = append(groups[module], relative)
		}
		for _, module := range order {
			routed := command
			args := append([]string(nil), command.Argv[:start]...)
			if module != "." {
				args = append([]string{"go", "-C", module}, command.Argv[1:start]...)
			}
			routed.Argv = append(args, groups[module]...)
			result = append(result, routed)
		}
	}
	return result, nil
}

func ownedGoPackage(repo, pattern string, owned map[string]bool) (string, string, error) {
	if pattern != "." && !strings.HasPrefix(pattern, "./") || strings.Contains(pattern, "\\") {
		return "", "", fmt.Errorf("Go package escapes repository: %q", pattern)
	}
	directory := strings.TrimPrefix(pattern, "./")
	recursive := strings.HasSuffix(directory, "/...") || directory == "..."
	if recursive {
		directory = strings.TrimSuffix(directory, "...")
	}
	directory = path.Clean(directory)
	if directory == ".." || strings.HasPrefix(directory, "../") || strings.HasPrefix(directory, "/") || strings.Contains(directory, ":") {
		return "", "", fmt.Errorf("Go package escapes repository: %q", pattern)
	}
	// Inspect every ancestor before selecting the nearest module: a linked
	// parent must not make a committed descendant appear to be an owned cwd.
	for ancestor := directory; ; ancestor = path.Dir(ancestor) {
		physical := filepath.Join(repo, filepath.FromSlash(ancestor))
		if info, err := os.Lstat(physical); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("Go package traverses a linked directory: %q", pattern)
		} else if err != nil && !os.IsNotExist(err) {
			return "", "", err
		}
		if ancestor == "." {
			break
		}
	}
	for ancestor := directory; ; ancestor = path.Dir(ancestor) {
		physical := filepath.Join(repo, filepath.FromSlash(ancestor))
		mod := filepath.Join(physical, "go.mod")
		if info, err := os.Lstat(mod); err == nil {
			if !info.Mode().IsRegular() || ancestor != "." && !owned[ancestor] {
				return "", "", fmt.Errorf("Go module is not a committed regular manifest: %s", ancestor)
			}
			relative := directory
			if ancestor != "." {
				relative = strings.TrimPrefix(directory, ancestor)
				relative = strings.TrimPrefix(relative, "/")
			}
			if recursive {
				relative = path.Join(relative, "...")
			}
			return ancestor, packagePattern(relative), nil
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
		if ancestor == "." {
			// Existing graph-free planning fixtures and removed packages retain
			// root execution; no new working directory is granted without go.mod.
			return ".", pattern, nil
		}
	}
}
