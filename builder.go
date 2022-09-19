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

type Builder struct {
	Code        CodeInfo
	Git         GitInfo
	Executables []ExecutableInfo

	Targets       Targets
	DefaultTarget string

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
	Name    string
	Path    string
	Package string

	Archs       []string
	GCO         bool
	BuildArgs   []string
	LDFlags     []string
	LDFlagsVars map[string]string

	Publish bool
}

type GitInfo struct {
	Tag        *semver.Version
	Commit     string
	CommitDate *time.Time
}

func NewBuilder(cfg *BuilderConfig) (*Builder, error) {
	var err error

	if cfg == nil {
		cfg = NewBuilderConfig()
	}

	if cfg.BaseDir == "" {
		cfg.BaseDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	b := &Builder{}
	b.Targets.items = map[string]*Target{}

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

	err = b.initCodeInfo(cfg)
	if err != nil {
		return nil, err
	}

	err = b.createExecutables(cfg)
	if err != nil {
		return nil, err
	}

	b.createDefaultTargets()

	return b, nil
}

func (b *Builder) findGoVersion() (*semver.Version, string, string, error) {
	goVersion, err := b.Console.RunAndReturnOutput(b.GO, "version")
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
	tag, err := b.Console.RunAndReturnOutput(b.GIT, "describe", "--tags", "--dirty")
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
	result, _ := b.Console.RunAndReturnOutput(b.GIT, "log", "-1", "--format=%H")
	return result
}

func (b *Builder) findGitCommitDate() *time.Time {
	date, err := b.Console.RunAndReturnOutput(b.GIT, "log", "-1", "--format=%aI")
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
	var err error

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
		ver := "0.0.0-devel+"
		if b.Git.Commit != "" {
			ver += b.Git.Commit[:7] + "."
		}
		ver += b.Code.BuildDate.Format("20060102150405")

		b.Code.Version, err = semver.NewVersion(ver)
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Builder) createExecutables(cfg *BuilderConfig) error {
	archs, err := b.ListArchs(cfg.Archs...)
	if err != nil {
		return err
	}

	var ldflags []string
	if !cfg.PreserveSymbols {
		ldflags = append(ldflags, "-s", "-w")
	}

	ldflagsVars := map[string]string{}
	for k, v := range cfg.LDFlagsVars {
		ldflagsVars[k] = v
	}
	ldflagsVars["main.version"] = b.Code.Version.String()
	ldflagsVars["main.buildDate"] = b.Code.BuildDate.String()
	ldflagsVars["main.commit"] = b.Git.Commit

	err = b.findRelativeDirsWithMain(cfg, b.Code.BaseDir, func(path, rel string, publish bool) error {
		var name string
		if rel == "." {
			name = filepath.Base(b.Code.Package)
		} else {
			name = filepath.Base(rel)
		}

		pkg := b.Code.Package
		if rel != "." {
			pkg += "/" + filepath.ToSlash(rel)
		}

		e := ExecutableInfo{
			Name:        name,
			Path:        path,
			Package:     pkg,
			Archs:       archs,
			GCO:         cfg.GCO,
			BuildArgs:   cfg.BuildArgs,
			LDFlags:     ldflags,
			LDFlagsVars: ldflagsVars,
			Publish:     publish,
		}

		b.Executables = append(b.Executables, e)

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

func (b *Builder) findRelativeDirsWithMain(cfg *BuilderConfig, baseDir string, cb func(string, string, bool) error) error {
	ignoredDirs := map[string]int{
		"_examples": 0,
		"examples":  0,
		"internal":  0,
	}

	mainFiles := map[string]int{}
	for _, e := range cfg.MainFileNames {
		mainFiles[e] = 1
	}

	return filepath.WalkDir(baseDir,
		func(path string, dir fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if dir.IsDir() {
				if strings.HasPrefix(dir.Name(), ".") {
					return filepath.SkipDir
				}

				return nil
			}

			_, hasMain := mainFiles[dir.Name()]
			if !hasMain {
				return nil
			}

			abs := filepath.Dir(path)

			rel, err := filepath.Rel(baseDir, abs)
			if err != nil {
				return err
			}

			publish := true

			for _, d := range strings.Split(filepath.ToSlash(rel), "/") {
				_, ok := ignoredDirs[d]
				if ok {
					publish = false
					break
				}
			}

			err = cb(abs, rel, publish)
			if err != nil {
				return err
			}

			return nil
		})
}

func (b *Builder) listAvailableArchs() (map[string][]string, error) {
	list, err := b.Console.RunAndReturnOutput(b.GO, "tool", "dist", "list")
	if err != nil {
		return nil, err
	}

	arr := strings.Split(list, "\n")

	result := map[string][]string{}

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

	b.Targets.Add("test", nil, func() error {
		return b.Console.RunInline(b.GO, "test", "./...")
	})

	b.Targets.Add("clean-zip", nil, func() error {
		return b.RunCleanZip()
	})

	bt := b.Targets.Add("build", nil, nil)
	zt := b.Targets.Add("zip", []string{"clean-zip"}, nil)

	for _, exec := range b.Executables {
		bet := b.Targets.Add(bt.Name+":"+exec.Name, nil, nil)
		bt.AddDependency(bet)

		zet := b.Targets.Add(zt.Name+":"+exec.Name, nil, nil)
		zt.AddDependency(zet)

		for _, arch := range exec.Archs {
			ee := exec
			aa := arch

			beat := b.Targets.Add(bet.Name+":"+arch, nil, func() error {
				return b.RunBuild(ee, aa)
			})
			bet.AddDependency(beat)

			zeat := b.Targets.Add(zet.Name+":"+arch, []string{beat.Name}, func() error {
				return b.RunZip(ee, aa)
			})
			zet.AddDependency(zeat)
		}
	}

	b.Targets.Add("all", []string{"build", "test", "zip"}, nil)

	b.DefaultTarget = "all"
}
