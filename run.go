package build

import (
	"fmt"
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

	rel, err := filepath.Rel(b.Code.BaseDir, exec.Path)
	if err != nil {
		return err
	}

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

	name := exec.Name
	if goos == "windows" {
		name += ".exe"
	}

	output, err := filepath.Abs(filepath.Join(b.Code.BaseDir, "build", goos, goarch, rel, name))
	if err != nil {
		return err
	}

	cmd = append(cmd, "-o", output, exec.Path)

	return b.Console.RunInline(cmd...)
}
