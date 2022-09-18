package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

func CreateConsole(dir string) (*Console, error) {
	c := &Console{
		Dir: dir,
	}

	return c, nil
}

type Console struct {
	Dir string
}

func (r *Console) FindExecutable(cmd string) (string, error) {
	result, err := exec.LookPath(cmd)
	if err != nil {
		return "", errors.Wrapf(err, "%v executable not found in PATH", cmd)
	}

	result, err = filepath.Abs(result)
	if err != nil {
		return "", err
	}

	return result, nil
}

func (r *Console) RunInline(args ...interface{}) error {
	cmd, err := r.createCommand(args)
	if err != nil {
		return err
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	tmp := make([]string, len(args))
	for i, a := range args {
		tmp[i] = fmt.Sprint(a)
	}
	fmt.Printf("Executing '%v'\n", strings.Join(tmp, "' '"))

	return cmd.Run()
}

func (r *Console) RunAndReturnOutput(args ...interface{}) (string, error) {
	cmd, err := r.createCommand(args)
	if err != nil {
		return "", err
	}

	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrapf(err, "error calling %v %v", cmd.Path, cmd.Args)
	}

	result := string(output)
	result = strings.TrimRight(result, "\r\n")

	return result, nil
}

func (r *Console) createCommand(args []interface{}) (*exec.Cmd, error) {
	var err error
	var env []string
	var name string
	var cargs []string

	dir := r.Dir

	for i, a := range args {
		s := fmt.Sprint(a)

		switch {
		case i == 0 && strings.HasPrefix(s, "cd "):
			dir, err = filepath.Rel(r.Dir, s[3:])
			if err != nil {
				return nil, err
			}

		case name == "" && strings.IndexAny(s, "=") > 0:
			env = append(env, s)

		case name == "":
			name = s

		default:
			cargs = append(cargs, s)
		}
	}

	cmd := exec.Command(name, cargs...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	return cmd, nil
}
