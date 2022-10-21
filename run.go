package build

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/licensecheck"
	"github.com/pkg/errors"
)

func (b *Builder) RunTarget(name string) error {
	if b.GO_VERSION.LessThan(b.Code.MinGoVersion) {
		return errors.Errorf("unsupported go version %v - shold be at least %v", b.GO_VERSION, b.Code.MinGoVersion)
	}

	ts, err := b.Targets.ComputeTargetRunOrder(name)
	if err != nil {
		return err
	}

	printf := func(i int, format string, a ...interface{}) {
		fmt.Printf("[%v %v/%v] %v\n", time.Now().Format("15:04:05"), i, len(ts), fmt.Sprintf(format, a...))
	}

	for i, n := range ts {
		printf(i, "Executing target %v", n)

		t := b.Targets.Get(n)
		err = t.run()

		if err != nil {
			printf(i, "ERROR executing target %v: %v", n, err)
			return err
		}

		fmt.Println()
	}

	return nil
}

func (b *Builder) RunBuild(exec ExecutableInfo, arch string) error {
	parts := strings.Split(arch, "/")
	goos := parts[0]
	goarch := parts[1]

	var cmd []interface{}

	cmd = append(cmd, "cd "+b.Code.BaseDir, "GOOS="+goos, "GOARCH="+goarch)

	if !exec.GCO {
		cmd = append(cmd, "CGO_ENABLED=0")
	}

	cmd = append(cmd, b.GO, "build")

	for _, a := range exec.BuildArgs {
		cmd = append(cmd, a)
	}

	if len(exec.LDFlags) > 0 || len(exec.LDFlagsVars) > 0 {
		ldflags := exec.LDFlags
		for k, v := range exec.LDFlagsVars {
			ldflags = append(ldflags, "-X", fmt.Sprintf(`"%v=%v"`, k, v))
		}

		cmd = append(cmd, "-ldflags", strings.Join(ldflags, " "))
	}

	output, err := b.GetOutputExecutableName(exec, arch)
	if err != nil {
		return err
	}

	cmd = append(cmd, "-o", output, exec.Path)

	return b.Console.RunInline(cmd...)
}

func (b *Builder) RunCleanZip() error {
	buildDir, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build"))
	if err != nil {
		return err
	}

	files, err := os.ReadDir(buildDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".zip") {
			continue
		}

		err = os.Remove(filepath.Join(buildDir, file.Name()))
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Builder) RunZip(exec ExecutableInfo, arch string) error {
	if !exec.Publish {
		return nil
	}

	outputExec, err := b.GetOutputExecutableName(exec, arch)
	if err != nil {
		return err
	}

	_, err = os.Stat(outputExec)
	if err != nil {
		return errors.Wrapf(err, "error accessing compiled executable %v", outputExec)
	}

	outputZip, err := b.GetOutputZipName(exec, arch)
	if err != nil {
		return err
	}

	_ = os.Remove(outputZip)

	oz, err := os.Create(outputZip)
	if err != nil {
		return err
	}
	defer oz.Close()

	oe, err := os.Open(outputExec)
	if err != nil {
		return err
	}
	defer oe.Close()

	zw := zip.NewWriter(oz)
	defer zw.Close()

	ze, err := zw.Create(filepath.Base(outputExec))
	if err != nil {
		return err
	}

	_, err = io.Copy(ze, oe)
	if err != nil {
		return err
	}

	return nil
}

func (b *Builder) GetOutputZipName(exec ExecutableInfo, arch string) (string, error) {
	name := fmt.Sprintf("%v-%v-%v.zip", exec.Name, b.Code.Version, strings.ReplaceAll(arch, "/", "_"))
	name = fixFilename(name)

	output, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build", name))
	if err != nil {
		return "", err
	}

	return output, nil
}

func (b *Builder) GetOutputExecutableName(exec ExecutableInfo, arch string) (string, error) {
	name := exec.Name
	if strings.HasPrefix(arch, "windows/") {
		name += ".exe"
	}

	output, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build", arch, name))
	if err != nil {
		return "", err
	}

	return output, nil
}

func (b *Builder) RunLicenseCheck() error {
	modCacheRoot, err := b.loadModCacheRoot()
	if err != nil {
		return err
	}

	deps, err := b.loadDependencies()
	if err != nil {
		return err
	}

	for _, dep := range deps {
		err = b.fillLicenseInfo(dep, modCacheRoot)
		if err != nil {
			return err
		}
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Path < deps[j].Path
	})

	for _, dep := range deps {
		var names []string
		for _, l := range dep.Licenses {
			if l.Name != "" {
				names = append(names, l.Name)
			}
		}

		license := strings.Join(names, ", ")
		if license == "" {
			license = "Unknown"
		}

		fmt.Printf("%v %v : %v\n", dep.Path, dep.Version, license)
	}

	return nil
}

func (b *Builder) loadModCacheRoot() (string, error) {
	root, err := b.Console.RunAndReturnOutput(b.GO, "env", "GOMODCACHE")
	if err != nil {
		return "", err
	}

	root = addSeparatorAtEnd(root)

	return root, nil
}

func (b *Builder) loadDependencies() ([]*modDependency, error) {
	output, err := b.Console.RunAndReturnOutput(b.GO, "mod", "download", "-json")
	if err != nil {
		return nil, err
	}

	var deps []*modDependency

	dec := json.NewDecoder(strings.NewReader(output))
	for {
		var dep modDependency

		err = dec.Decode(&dep)

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, err
		}

		dep.Dir = addSeparatorAtEnd(dep.Dir)

		deps = append(deps, &dep)
	}

	return deps, nil
}

func (b *Builder) fillLicenseInfo(dep *modDependency, modCacheRoot string) error {
	licenseFileNames, err := b.findLicenseFilesSearchingParents(dep, modCacheRoot)
	if err != nil {
		return err
	}

	for _, licenseFileName := range licenseFileNames {
		data, err := os.ReadFile(licenseFileName)
		if err != nil {
			return err
		}

		license := licenseInfo{
			Contents: string(data),
		}

		cov := licensecheck.Scan(data)
		if cov.Percent >= 75 { // Same as pkg.go.dev
			license.Name = cov.Match[0].ID
		}

		dep.Licenses = append(dep.Licenses, license)
	}

	return nil
}

func (b *Builder) findLicenseFilesSearchingParents(dep *modDependency, modCacheRoot string) ([]string, error) {
	path := dep.Dir
	for len(path) > len(modCacheRoot) {
		fileNames, err := b.findLicenseFiles(path)
		if err != nil {
			return nil, err
		}

		if len(fileNames) > 0 {
			return fileNames, nil
		}

		// Try parent folder
		path = addSeparatorAtEnd(filepath.Dir(path))
	}

	return nil, nil
}

var licenseFiles = map[string]bool{
	"copying":            true,
	"licence":            true,
	"license":            true,
	"licence-2.0":        true,
	"license-2.0":        true,
	"licence-apache":     true,
	"license-apache":     true,
	"licence-apache-2.0": true,
	"license-apache-2.0": true,
	"licence-mit":        true,
	"license-mit":        true,
	"licenceInfo":        true,
	"licenseInfo":        true,
	"licenceInfo-2.0":    true,
	"licenseInfo-2.0":    true,
	"licenceInfo-apache": true,
	"licenseInfo-apache": true,
	"licenceInfo-mit":    true,
	"licenseInfo-mit":    true,
	"mit-licence":        true,
	"mit-license":        true,
	"mit-licenceInfo":    true,
	"mit-licenseInfo":    true,
}

var licenseExtensions = map[string]bool{
	"":          true,
	".code":     true,
	".docs":     true,
	".markdown": true,
	".md":       true,
	".mit":      true,
	".rst":      true,
	".txt":      true,
}

func (b *Builder) findLicenseFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var result []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)
		if !licenseFiles[name[:len(name)-len(ext)]] || !licenseExtensions[ext] {
			continue
		}

		result = append(result, filepath.Join(path, entry.Name()))
	}

	return result, nil
}

type modDependency struct {
	Path     string
	Version  string
	Dir      string
	Licenses []licenseInfo
}

type licenseInfo struct {
	Name     string
	Contents string
}

func addSeparatorAtEnd(dir string) string {
	if dir == "" {
		return dir
	}

	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}

	return dir
}
