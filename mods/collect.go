// Copyright 2019 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mods

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/rogpeppe/go-internal/module"

	"github.com/pkg/errors"

	"github.com/gohugoio/hugo/config"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

const vendorModulesFilename = "modules.txt"

func (h *Client) Collect() (ModulesConfig, error) {
	if len(h.imports) == 0 {
		return ModulesConfig{}, nil
	}

	c := &collector{
		Client: h,
	}

	if err := c.collect(); err != nil {
		return ModulesConfig{}, err
	}

	return ModulesConfig{
		Modules:           c.modules,
		GoModulesFilename: c.GoModulesFilename,
	}, nil

}

type ModulesConfig struct {
	Modules Modules

	// Set if this is a Go modules enabled project.
	GoModulesFilename string
}

type collected struct {
	// Pick the first and prevent circular loops.
	seen map[string]bool

	// Maps module path to a _vendor dir. These values are fetched from
	// _vendor/modules.txt, and the first (top-most) will win.
	vendored map[string]vendoredModule

	// Set if a Go modules enabled project.
	gomods goModules

	// Ordered list of collected modules, including Go Modules and theme
	// components stored below /themes.
	modules Modules
}

// Collects and creates a module tree.
type collector struct {
	*Client

	*collected
}

type vendoredModule struct {
	Dir     string
	Version string
}

func (c *collector) initModules() error {
	c.collected = &collected{
		seen:     make(map[string]bool),
		vendored: make(map[string]vendoredModule),
	}

	// We may fail later if we don't find the mods.
	return c.loadModules()
}

// TODO(bep) mod:
// - no-vendor
func (c *collector) isSeen(path string) bool {
	key := pathKey(path)
	if c.seen[key] {
		return true
	}
	c.seen[key] = true
	return false
}

func (c *collector) getVendoredDir(path string) (vendoredModule, bool) {
	v, found := c.vendored[path]
	return v, found
}

// TODO(bep) mod
const zeroVersion = ""

func (c *collector) add(owner Module, modulePath string) (Module, error) {
	var mod *goModule

	if err := c.collectModulesTXT(owner.Dir()); err != nil {
		return nil, err
	}

	var moduleDir string

	// Try _vendor first.
	vendoredModule, vendored := c.getVendoredDir(modulePath)
	if vendored {
		moduleDir = vendoredModule.Dir
	}

	if moduleDir == "" {
		mod = c.gomods.GetByPath(modulePath)
		if mod != nil {
			moduleDir = mod.Dir
		}

		if moduleDir == "" {
			if c.GoModulesFilename != "" && c.IsProbablyModule(modulePath) {
				// Try to "go get" it and reload the module configuration.
				if err := c.Get(modulePath); err != nil {
					return nil, err
				}
				if err := c.loadModules(); err != nil {
					return nil, err
				}

				mod = c.gomods.GetByPath(modulePath)
				if mod != nil {
					moduleDir = mod.Dir
				}
			}

			// Fall back to /themes/<mymodule>
			if moduleDir == "" {
				moduleDir = filepath.Join(c.themesDir, modulePath)
				if found, _ := afero.Exists(c.fs, moduleDir); !found {
					return nil, c.wrapModuleNotFound(errors.Errorf("module %q not found; either add it as a Hugo Module or store it in %q.", modulePath, c.themesDir))
				}
			}
		}
	}

	if found, _ := afero.Exists(c.fs, moduleDir); !found {
		return nil, c.wrapModuleNotFound(errors.Errorf("%q not found", moduleDir))
	}

	if !strings.HasSuffix(moduleDir, fileSeparator) {
		moduleDir += fileSeparator
	}

	var version string
	if vendored {
		version = vendoredModule.Version
	}

	ma := &moduleAdapter{
		dir:     moduleDir,
		vendor:  vendored,
		gomod:   mod,
		version: version,
		// TODO(bep) mod when vendor the owner must point to the one with
		// the _vendor dir?
		owner: owner,
	}
	if mod == nil {
		ma.path = modulePath
	}

	if err := c.applyThemeConfig(ma); err != nil {
		return nil, err
	}

	c.modules = append(c.modules, ma)
	return ma, nil

}

func (c *collector) addAndRecurse(owner Module, themes ...string) error {
	for i := 0; i < len(themes); i++ {
		theme := themes[i]
		if !c.isSeen(theme) {
			tc, err := c.add(owner, theme)
			if err != nil {
				return err
			}
			if err := c.addThemeNamesFromTheme(tc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *collector) addThemeNamesFromTheme(module Module) error {
	if module.Cfg() != nil && module.Cfg().IsSet("theme") {
		v := module.Cfg().Get("theme")
		switch vv := v.(type) {
		case []string:
			return c.addAndRecurse(module, vv...)
		case []interface{}:
			return c.addAndRecurse(module, cast.ToStringSlice(vv)...)
		default:
			return c.addAndRecurse(module, cast.ToString(vv))
		}
	}

	return nil
}

func (c *collector) applyThemeConfig(tc *moduleAdapter) error {

	var (
		configFilename string
		cfg            config.Provider
		exists         bool
	)

	// Viper supports more, but this is the sub-set supported by Hugo.
	for _, configFormats := range config.ValidConfigFileExtensions {
		configFilename = filepath.Join(tc.Dir(), "config."+configFormats)
		exists, _ = afero.Exists(c.fs, configFilename)
		if exists {
			break
		}
	}

	if !exists {
		// No theme config set.
		return nil
	}

	if configFilename != "" {
		var err error
		cfg, err = config.FromFile(c.fs, configFilename)
		if err != nil {
			return err
		}
	}

	tc.configFilename = configFilename
	tc.cfg = cfg

	return nil

}

func (c *collector) collect() error {
	if err := c.initModules(); err != nil {
		return err
	}

	// Create a pseudo module for the main project.
	var version string
	var path string
	gomod := c.gomods.GetMain()
	if gomod == nil {
		path = "project"
	}

	projectMod := &moduleAdapter{
		path:    path,
		dir:     c.workingDir,
		version: version,
		gomod:   gomod,
	}

	for _, imp := range c.imports {
		if err := c.addAndRecurse(projectMod, imp); err != nil {
			return err
		}
	}

	return nil
}

func (c *collector) collectModulesTXT(dir string) error {
	vendorDir := filepath.Join(dir, vendord)
	filename := filepath.Join(vendorDir, vendorModulesFilename)

	f, err := c.fs.Open(filename)

	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		// # github.com/alecthomas/chroma v0.6.3
		line := scanner.Text()
		line = strings.Trim(line, "# ")
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return errors.Errorf("invalid modules list: %q", filename)
		}
		path := parts[0]
		if _, found := c.vendored[path]; !found {
			c.vendored[path] = vendoredModule{
				Dir:     filepath.Join(vendorDir, path),
				Version: parts[1],
			}
		}

	}
	return nil
}

func (c *collector) loadModules() error {
	modules, err := c.listGoMods()
	if err != nil {
		return err
	}
	c.gomods = modules
	return nil
}

func (c *collector) wrapModuleNotFound(err error) error {
	if c.GoModulesFilename == "" {
		return err
	}

	baseMsg := "we found a go.mod file in your project, but"

	switch c.goBinaryStatus {
	case goBinaryStatusNotFound:
		return errors.Wrap(err, baseMsg+" you need to install Go to use it. See https://golang.org/dl/.")
	case goBinaryStatusTooOld:
		return errors.Wrap(err, baseMsg+" you need to a newer version of Go to use it. See https://golang.org/dl/.")
	}

	return err

}

// In the first iteration of Hugo Modules, we do not support multiple
// major versions running at the same time, so we pick the first (upper most).
// We will investigate namespaces in future versions.
// TODO(bep) mod add a warning when the above happens.
func pathKey(p string) string {
	prefix, _, _ := module.SplitPathVersion(p)
	return strings.ToLower(prefix)
}
