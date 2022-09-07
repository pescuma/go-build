package build

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/pkg/errors"
	"golang.org/x/mod/modfile"
)

type BuilderConfig struct {
	BaseDir string

	Archs []string
	GCO   *bool
	PIE   *bool
}

func CreateBuilder(cfg *BuilderConfig) (*Builder, error) {
	var err error

	if cfg == nil {
		cfg = &BuilderConfig{}
	}

	if cfg.BaseDir == "" {
		cfg.BaseDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	b := &Builder{}

	b.Console, err = CreateConsole(cfg.BaseDir)
	if err != nil {
		return nil, err
	}

	b.GO, err = b.Console.FindExecutable("go")
	if err != nil {
		return nil, err
	}

	b.GO_VERSION, b.GO_GOOS, b.GO_GOARCH, err = b.findGoVersion()
	if err != nil {
		return nil, err
	}

	b.GIT, _ = b.Console.FindExecutable("git")

	if b.GIT != "" {
		b.Git.Tag = b.findGitTag()
		b.Git.Commit = b.findGitCommit()
		b.Git.CommitDate = b.findGitCommitDate()
	}

	if err = b.initCodeInfo(cfg); err != nil {
		return nil, err
	}

	b.createDefaultTargets()

	return b, nil
}

type Builder struct {
	Code        CodeInfo
	Git         GitInfo
	Executables []ExecutableInfo

	Targets TargetList

	Console *Console

	GO         string
	GO_VERSION *semver.Version
	GO_GOOS    string
	GO_GOARCH  string

	GIT string
}

type CodeInfo struct {
	BaseDir   string
	Package   string
	Version   *semver.Version
	BuildDate time.Time

	MinGoVersion *semver.Version
}

type ExecutableInfo struct {
	Name string
	Path string

	Archs []string
	GCO   *bool
	PIE   *bool
}

type GitInfo struct {
	Tag        *semver.Version
	Commit     string
	CommitDate *time.Time
}

func (b *Builder) findGoVersion() (*semver.Version, string, string, error) {
	goVersion, err := b.Console.OutputOf(b.GO, "version")
	if err != nil {
		return nil, "", "", err
	}

	goVersionEr := regexp.MustCompile(`(?i)^go version go([0-9.+-]+) (\w+)/(\w+)\n?$`)
	parts := goVersionEr.FindStringSubmatch(goVersion)
	if len(parts) != 4 {
		return nil, "", "", errors.Errorf("Unknown go version output: %v", goVersion)
	}

	version, err := semver.NewVersion(parts[1])
	if err != nil {
		return nil, "", "", err
	}

	goos := parts[2]
	goarch := parts[3]

	return version, goos, goarch, nil
}

func (b *Builder) findGitTag() *semver.Version {
	tag, err := b.Console.OutputOf(b.GIT, "describe", "--tags", "--dirty")
	if err != nil {
		return nil
	}

	ver, err := semver.NewVersion(tag)
	if err != nil {
		return nil
	}

	return ver
}

func (b *Builder) findGitCommit() string {
	result, _ := b.Console.OutputOf(b.GIT, "log", "-1", "--format=%H")
	return result
}

func (b *Builder) findGitCommitDate() *time.Time {
	date, err := b.Console.OutputOf(b.GIT, "log", "-1", "--format=%aI")
	if err != nil {
		return nil
	}

	result, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return nil
	}

	return &result
}

func (b *Builder) initCodeInfo(cfg *BuilderConfig) error {
	b.Code.BaseDir = cfg.BaseDir

	modFile := filepath.Join(b.Code.BaseDir, "go.mod")
	modContent, err := os.ReadFile(modFile)
	if err != nil {
		return errors.Wrapf(err, "Error loading go.mod. This should be run from the project folder.")
	}

	ast, err := modfile.ParseLax(modFile, modContent, nil)
	if err != nil {
		return err
	}

	b.Code.Package = ast.Module.Mod.Path

	b.Code.MinGoVersion, err = semver.NewVersion(ast.Go.Version)
	if err != nil {
		return err
	}

	if b.Git.CommitDate != nil {
		b.Code.BuildDate = *b.Git.CommitDate
	} else {
		b.Code.BuildDate = time.Now()
	}

	if b.Git.Tag != nil {
		b.Code.Version = b.Git.Tag
	} else {
		b.Code.Version, _ = semver.NewVersion("0.0.0-devel+" + b.Code.BuildDate.Format("200601021504"))
	}

	archs, err := b.ListArchs(cfg.Archs...)
	if err != nil {
		return err
	}

	err = b.findRelativeDirsWithMain(b.Code.BaseDir, func(rel string) error {
		var name string
		if rel == "." {
			name = filepath.Base(b.Code.Package)
		} else {
			name = filepath.Base(rel)
		}

		b.Executables = append(b.Executables, ExecutableInfo{
			Name:  name,
			Path:  rel,
			Archs: archs,
			GCO:   cfg.GCO,
			PIE:   cfg.PIE,
		})

		return nil
	})
	if err != nil {
		return err
	}

	return nil

}

func (b *Builder) ListArchs(desired ...string) ([]string, error) {
	available, err := b.listAvailableArchs()
	if err != nil {
		return nil, err
	}

	var result []string

	if len(desired) > 0 {
		for _, a := range desired {
			l, ok := available[a]
			if !ok {
				return nil, errors.Errorf("OS/ARCH not available: '%v'", a)
			}

			result = append(result, l...)
		}
	} else {
		for k := range available {
			if strings.Index(k, "/") > 0 {
				result = append(result, k)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})

	return result, nil
}

func (b *Builder) findRelativeDirsWithMain(baseDir string, cb func(string) error) error {
	ignoredDirs := map[string]int{
		"_examples": 0,
		"examples":  0,
		"internal":  0,
	}

	return filepath.WalkDir(baseDir,
		func(path string, dir fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if dir.IsDir() {
				_, ok := ignoredDirs[dir.Name()]
				if ok || strings.HasPrefix(dir.Name(), ".") {
					return filepath.SkipDir
				}

				return nil
			}

			if dir.Name() != "main.go" {
				return nil
			}

			rel, err := filepath.Rel(baseDir, filepath.Dir(path))
			if err != nil {
				return err
			}

			err = cb(rel)
			if err != nil {
				return err
			}

			return nil
		})
}

func (b *Builder) listAvailableArchs() (map[string][]string, error) {
	list, err := b.Console.OutputOf(b.GO, "tool", "dist", "list")
	if err != nil {
		return nil, err
	}

	arr := strings.Split(list, "\n")

	result := make(map[string][]string)

	add := func(k, v string) {
		_, ok := result[k]
		if !ok {
			result[k] = nil
		}
		result[k] = append(result[k], v)
	}

	for _, a := range arr {
		goos := strings.Split(a, "/")[0]
		add(goos, a)
		add(a, a)
	}

	return result, nil
}

func (b *Builder) createDefaultTargets() {
	b.Targets.Add("generate", nil, func() error {
		return b.Console.RunInline(b.GO, "generate", "./...")
	})

	execTargets := make([]string, len(b.Executables))

	for i, e := range b.Executables {
		archTargets := make([]string, len(e.Archs))

		for j, a := range e.Archs {
			name := "build:" + e.Name + ":" + a
			archTargets[j] = name
			b.Targets.Add(name, nil, func() error {
				return b.Console.RunInline(b.GO, "build", e.Path)
			})
		}

		name := "build:" + e.Name
		execTargets[i] = name
		b.Targets.Add(name, archTargets, nil)
	}

	b.Targets.Add("build", execTargets, nil)

	b.Targets.Add("test", nil, func() error {
		return b.Console.RunInline(b.GO, "test")
	})

	b.Targets.Add("all", []string{"build", "test"}, nil)
}

func (b *Builder) RunTarget(name string) error {
	t, ok := b.Targets.items[name]
	if !ok {
		return errors.Errorf("Unknown target: %v", name)
	}

	return t.run()
}
