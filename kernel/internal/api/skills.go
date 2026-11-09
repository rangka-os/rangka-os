package api

import (
	"sync"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/skills"
)

// skillCache keeps the skill library per catalogue directory pair: skills change with a release, not at run time.
var skillCache sync.Map // "<apps>\x00<modules>" → *skills.Library

// skillLibrary returns the platform skills plus the skills of every app and module in the catalogue, installed or
// not, so an agent can read an app's skill before proposing to add it (contract 0035).
func (s *Server) skillLibrary() *skills.Library {
	reg := s.Engine.Reg()
	modules := reg.ModulesDir
	if modules == "" {
		modules = meta.DefaultModulesDir(reg.AppsDir)
	}
	key := reg.AppsDir + "\x00" + modules
	if l, ok := skillCache.Load(key); ok {
		return l.(*skills.Library)
	}
	l := skills.Load(reg.AppsDir, modules)
	skillCache.Store(key, l)
	return l
}
