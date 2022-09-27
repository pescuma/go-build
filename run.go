package build

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
