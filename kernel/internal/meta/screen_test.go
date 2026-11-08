package meta

import (
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func TestLoadScreensAndWork(t *testing.T) {
	reg := Load("../../testdata/screens/apps", "../../testdata/screens/site", nil)
	s := reg.Screen("isi_jam")
	if s == nil || s.App != "kerja" || s.Type != "entry" || s.File != "modules/kerja/screens/isi_jam.rk" || reg.Docs[s.File] != s.Doc {
		t.Fatalf("isi_jam = %+v", s)
	}
	if b := s.Block(); b["model"] != "jam_kerja" || len(s.Roles) != 2 || s.If != "user.roles.size() > 0" {
		t.Fatalf("block %v roles %v if %q", b, s.Roles, s.If)
	}
	var got []string
	for _, w := range reg.Work() {
		x := w.Screen
		if w.Home {
			x += "*"
		}
		if len(w.Roles) > 0 {
			x += "@" + w.Roles[0]
		}
		got = append(got, x+"/"+w.App)
	}
	if want := "isi_jam*@staf/kantor catat/kantor kasir@staf/kantor"; join(got) != want {
		t.Fatalf("work = %q, want %q", join(got), want)
	}
}

// A screen of a unit that is not installed is neither returned nor offered.
func TestScreenNotInstalled(t *testing.T) {
	reg := Load("../../testdata/screens/apps", "../../testdata/screens/site", nil)
	reg.Installed["kerja"] = false
	if reg.Screen("isi_jam") != nil || len(reg.Work()) != 0 {
		t.Fatal("screen of an uninstalled module is offered")
	}
}

func TestValidScreensHaveNoProblems(t *testing.T) {
	_, rep := ValidateOpts(Options{AppsDir: "../../testdata/screens/apps", SiteDir: "../../testdata/screens/site"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("problems: %+v", rep.All())
	}
}

func join(l []string) string {
	out := ""
	for i, s := range l {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
