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
	"github.com/gohugoio/hugo/config"
)

var _ Module = (*moduleAdapter)(nil)

type Config struct {
	// Decode: support :default =>
	// ^assets$|
	IncludeDirs string
}

type Module interface {

	// Optional config read from the configFilename above.
	Cfg() config.Provider

	// Optional configuration filename (e.g. "/themes/mytheme/config.json").
	// This will be added to the special configuration watch list when in
	// server mode.
	ConfigFilename() string

	// Directory holding files for this module.
	Dir() string

	// Returns whether this is a Go Module.
	IsGoMod() bool

	// In the dependency tree, this is the first module that defines this module
	// as a dependency.
	Owner() Module

	// Replaced by this module.
	Replace() Module

	// Returns the path to this module.
	// This will either be the module path, e.g. "github.com/gohugoio/myshortcodes",
	// or the path below your /theme folder, e.g. "mytheme".
	Path() string

	// Returns whether Dir points below the _vendor dir.
	Vendor() bool

	// The module version.
	Version() string
}

type Modules []Module

type moduleAdapter struct {
	// Set if not a Go module.
	path string
	dir  string

	// Set if a Go module.
	gomod *goModule

	// May be set for all.
	version        string
	vendor         bool
	owner          Module
	configFilename string
	cfg            config.Provider
}

func (m *moduleAdapter) Cfg() config.Provider {
	return m.cfg
}

func (m *moduleAdapter) ConfigFilename() string {
	return m.configFilename
}

func (m *moduleAdapter) Dir() string {
	// This may point to the _vendor dir.
	return m.dir
}

func (m *moduleAdapter) IsGoMod() bool {
	return m.gomod != nil
}

func (m *moduleAdapter) Owner() Module {
	return m.owner
}

func (m *moduleAdapter) Replace() Module {
	if m.IsGoMod() && !m.Vendor() && m.gomod.Replace != nil {
		return &moduleAdapter{
			gomod: m.gomod.Replace,
			owner: m.owner,
			dir:   m.gomod.Replace.Dir,
		}
	}
	return nil
}

func (m *moduleAdapter) Path() string {
	if m.gomod != nil {
		return m.gomod.Path
	}
	return m.path
}

func (m *moduleAdapter) Vendor() bool {
	return m.vendor
}

func (m *moduleAdapter) Version() string {
	return m.version
}
